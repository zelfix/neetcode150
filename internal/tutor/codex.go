package tutor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"neetcode150/internal/studydb"
)

// codexClient speaks the Codex app-server JSONL protocol over stdio. One
// app-server process owns one thread, which gives each problem a persistent
// conversation and lets the panel interrupt a live turn.
type codexClient struct {
	session *Session

	mu        sync.Mutex
	writer    *bufio.Writer
	nextID    int
	pending   map[int]chan codexResponse
	threadID  string
	turnID    string
	doneTurns map[string]bool

	sendMu sync.Mutex
	first  bool
	seed   string
}

type codexResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *codexError     `json:"error"`
}

type codexError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type codexMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *codexError     `json:"error"`
}

func (m *Manager) startCodex(scope *studydb.Scope, s *Session) error {
	cmd := exec.CommandContext(context.Background(), m.bin, "app-server", "--listen", "stdio://")
	cmd.Dir = m.root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting Codex app-server: %w", err)
	}

	s.mu.Lock()
	s.cmd, s.stdin, s.writer, s.running = cmd, stdin, bufio.NewWriter(stdin), true
	s.codex = &codexClient{
		session:   s,
		writer:    s.writer,
		pending:   map[int]chan codexResponse{},
		doneTurns: map[string]bool{},
		first:     true,
		seed:      m.seed(scope, s.Problem),
	}
	s.mu.Unlock()

	go s.readStderr(stderr)
	go s.readCodexEvents(stdout)
	go s.waitForProcess(cmd)

	c := s.codex
	if err := c.call("initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "neetcode150",
			"title":   "NeetCode 150 tutor",
			"version": "1",
		},
	}, nil); err != nil {
		s.closeSession()
		return fmt.Errorf("initializing Codex: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		s.closeSession()
		return fmt.Errorf("initializing Codex: %w", err)
	}

	sandbox := "read-only"
	if m.mode == ModeFull {
		sandbox = "danger-full-access"
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := c.call("thread/start", map[string]any{
		"cwd":            m.root,
		"approvalPolicy": "never",
		"sandbox":        sandbox,
	}, &result); err != nil {
		s.closeSession()
		return fmt.Errorf("starting the Codex conversation: %w", err)
	}
	if result.Thread.ID == "" {
		s.closeSession()
		return fmt.Errorf("the Codex app-server started a thread without an ID")
	}
	c.mu.Lock()
	c.threadID = result.Thread.ID
	c.mu.Unlock()
	s.mu.Lock()
	s.id = c.threadID
	s.mu.Unlock()
	s.publish(Event{Kind: KindReady, Mode: sandbox})
	return nil
}

// waitForProcess reaps the agent and publishes why it went.
//
// Both readers are drained first, and the order is load-bearing rather than
// tidy: Wait closes the pipes StdoutPipe and StderrPipe returned as soon as it
// sees the process exit, and os/exec says in as many words that calling it
// before the reads have finished is incorrect. Reaping first lost a fast
// exit's stderr — so the panel reported "the tutor session ended: exit status
// 3" without the line that said why — and could truncate the event stream the
// same way. It failed about one run in three on a loaded CI machine and never
// on a developer's.
func (s *Session) waitForProcess(cmd *exec.Cmd) {
	<-s.stderrDone
	<-s.eventsDone
	err := cmd.Wait()
	s.mu.Lock()
	s.running = false
	trailing := strings.TrimSpace(s.stderr.String())
	s.mu.Unlock()

	msg := "the tutor session ended"
	if err != nil {
		msg = fmt.Sprintf("the tutor session ended: %v", err)
	}
	if trailing != "" {
		msg += "\n" + lastLines(trailing, 8)
	}
	s.publish(Event{Kind: KindExit, Text: msg})
	close(s.done)
}

func (c *codexClient) call(method string, params any, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	response := make(chan codexResponse, 1)
	c.pending[id] = response
	if err := c.writeLocked(map[string]any{"method": method, "id": id, "params": params}); err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case got := <-response:
		if got.Error != nil {
			return fmt.Errorf("%s: %s", method, got.Error.Message)
		}
		if out != nil && len(got.Result) > 0 {
			if err := json.Unmarshal(got.Result, out); err != nil {
				return fmt.Errorf("decoding %s response: %w", method, err)
			}
		}
		return nil
	case <-c.session.done:
		return fmt.Errorf("the Codex app-server exited during %s", method)
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("timed out waiting for Codex to handle %s", method)
	}
}

func (c *codexClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeLocked(map[string]any{"method": method, "params": params})
}

func (c *codexClient) writeLocked(message any) error {
	b, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := c.writer.Write(append(b, '\n')); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (s *Session) readCodexEvents(r io.Reader) {
	defer close(s.eventsDone)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg codexMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if len(msg.ID) != 0 {
			if msg.Method != "" {
				s.codex.rejectServerRequest(msg.ID)
				continue
			}
			s.codex.resolve(msg)
			continue
		}
		s.codex.translate(msg)
	}
	if err := sc.Err(); err != nil {
		s.publish(Event{Kind: KindError, Text: fmt.Sprintf("lost Codex output: %v", err)})
	}
}

func (c *codexClient) resolve(msg codexMessage) {
	var id int
	if err := json.Unmarshal(msg.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	response := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if response != nil {
		response <- codexResponse{Result: msg.Result, Error: msg.Error}
	}
}

func (c *codexClient) rejectServerRequest(id json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.writeLocked(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    -32601,
			"message": "this tutor does not support server-initiated requests",
		},
	})
}

func (c *codexClient) translate(msg codexMessage) {
	var params struct {
		Delta string `json:"delta"`
		Turn  struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
		} `json:"item"`
		TurnID string `json:"turnId"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
	}

	switch msg.Method {
	case "item/agentMessage/delta":
		if params.Delta == "" {
			return
		}
		c.session.mu.Lock()
		c.session.streamed.WriteString(params.Delta)
		c.session.mu.Unlock()
		c.session.publish(Event{Kind: KindText, Text: params.Delta})
	case "item/completed":
		if params.Item.Type != "agentMessage" || strings.TrimSpace(params.Item.Text) == "" {
			return
		}
		if !c.session.alreadyStreamed(params.Item.Text) {
			c.session.publish(Event{Kind: KindText, Text: params.Item.Text})
		}
	case "item/started":
		if params.Item.Type == "commandExecution" && params.Item.Command != "" {
			c.session.publish(Event{Kind: KindTool, Tool: "command", Detail: firstLine(params.Item.Command, 120)})
		}
	case "turn/started":
		if params.Turn.ID != "" {
			c.mu.Lock()
			c.turnID = params.Turn.ID
			c.mu.Unlock()
		}
	case "turn/completed":
		c.mu.Lock()
		if params.Turn.ID != "" {
			c.doneTurns[params.Turn.ID] = true
		}
		c.turnID = ""
		c.mu.Unlock()
		c.session.mu.Lock()
		c.session.streamed.Reset() // the turn is over; the next one starts clean
		c.session.mu.Unlock()
		switch params.Turn.Status {
		case "completed", "interrupted":
			c.session.publish(Event{Kind: KindDone})
		default:
			message := "Codex turn ended with status " + params.Turn.Status
			if params.Turn.Error != nil && params.Turn.Error.Message != "" {
				message = params.Turn.Error.Message
			}
			c.session.publish(Event{Kind: KindError, Text: message})
		}
	case "error":
		if params.Error != nil && params.Error.Message != "" {
			c.session.publish(Event{Kind: KindError, Text: params.Error.Message})
		}
	}
}

func (c *codexClient) sendAs(text, display string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.mu.Lock()
	active := c.turnID != ""
	threadID := c.threadID
	first := c.first
	c.mu.Unlock()
	if active {
		return fmt.Errorf("the tutor is still responding to the previous question")
	}

	c.session.publish(Event{Kind: KindUser, Text: display})
	if first {
		text = c.seed + "\n\nThe user's question is: " + text
		c.mu.Lock()
		c.first = false
		c.mu.Unlock()
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := c.call("turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]string{{"type": "text", "text": text}},
	}, &result); err != nil {
		if first {
			c.mu.Lock()
			c.first = true
			c.mu.Unlock()
		}
		return err
	}
	if result.Turn.ID != "" {
		c.mu.Lock()
		if c.turnID == "" && !c.doneTurns[result.Turn.ID] {
			c.turnID = result.Turn.ID
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *codexClient) interrupt() error {
	c.mu.Lock()
	threadID, turnID := c.threadID, c.turnID
	c.mu.Unlock()
	if turnID == "" {
		return fmt.Errorf("no Codex turn to interrupt")
	}
	return c.call("turn/interrupt", map[string]string{
		"threadId": threadID,
		"turnId":   turnID,
	}, nil)
}
