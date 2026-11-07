package tutor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// maxLine is the longest stdout line accepted. A single event carrying a whole
// assistant message can be far past bufio's default 64 KiB, and the symptom of
// getting this wrong is a conversation that stops mid-sentence.
const maxLine = 8 << 20

// rawEvent is the subset of the CLI's stream-json output this package reads.
// Unknown fields are ignored by encoding/json, so new event types upstream are
// harmless rather than breaking.
type rawEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init
	SessionID      string   `json:"session_id"`
	Model          string   `json:"model"`
	Tools          []string `json:"tools"`
	PermissionMode string   `json:"permissionMode"`

	// system/permission_denied
	ToolName string `json:"tool_name"`

	// stream_event, where text arrives in pieces
	Event *struct {
		Type  string `json:"type"`
		Delta *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`

	// assistant: the completed message, which is where tool calls are visible
	Message *struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`

	// result
	Result    string  `json:"result"`
	IsError   bool    `json:"is_error"`
	TotalCost float64 `json:"total_cost_usd"`
}

// readEvents consumes the process's stdout until it closes.
func (s *Session) readEvents(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev rawEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// One unreadable line must not end a conversation. Skipping is the
			// right call: the stream is a sequence of independent events, and
			// the next one is very likely fine.
			continue
		}
		s.translate(ev)
	}
	if err := sc.Err(); err != nil {
		s.publish(Event{Kind: KindError, Text: fmt.Sprintf("lost the tutor's output: %v", err)})
	}
}

// translate turns one CLI event into at most one panel event.
func (s *Session) translate(ev rawEvent) {
	switch ev.Type {
	case "system":
		switch ev.Subtype {
		case "init":
			s.mu.Lock()
			if ev.SessionID != "" {
				s.id = ev.SessionID
			}
			s.model, s.tools = ev.Model, ev.Tools
			s.mu.Unlock()
			s.publish(Event{
				Kind:  KindReady,
				Model: ev.Model,
				Mode:  ev.PermissionMode,
				Tools: ev.Tools,
			})
		case "permission_denied":
			// Worth showing rather than swallowing: an answer that went strange
			// because a tool was refused is otherwise a mystery.
			s.publish(Event{Kind: KindDenied, Tool: ev.ToolName,
				Text: fmt.Sprintf("%s was not permitted in this mode", ev.ToolName)})
		}

	case "stream_event":
		if ev.Event == nil || ev.Event.Delta == nil {
			return
		}
		if ev.Event.Delta.Type == "text_delta" && ev.Event.Delta.Text != "" {
			s.mu.Lock()
			s.streamed.WriteString(ev.Event.Delta.Text)
			s.mu.Unlock()
			s.publish(Event{Kind: KindText, Text: ev.Event.Delta.Text})
		}

	case "assistant":
		if ev.Message == nil {
			return
		}
		// Partial messages already delivered the prose, so re-publishing the
		// completed text would double it. But if no deltas arrived — an older
		// CLI, or the flag not taking effect — this is the only copy there is,
		// so fall back to it rather than show nothing.
		//
		// Checked per block and without clearing what was streamed: one turn can
		// arrive as several assistant messages, and only the blocks that were
		// not streamed are missing from the panel.
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if !s.alreadyStreamed(c.Text) {
					s.publish(Event{Kind: KindText, Text: c.Text})
				}
			case "tool_use":
				s.publish(Event{Kind: KindTool, Tool: c.Name, Detail: summarizeTool(c.Input)})
			}
		}

	case "result":
		s.mu.Lock()
		// total_cost_usd is the cost of the session so far, not of this turn, so
		// the budget needs the difference. Summing these fields across turns —
		// the obvious reading — overstates a five-turn session threefold.
		var delta float64
		if ev.TotalCost > 0 {
			delta = ev.TotalCost - s.cost
			s.cost = ev.TotalCost
		}
		cost := s.cost
		s.streamed.Reset() // the turn is over; the next one starts clean
		s.mu.Unlock()
		if delta > 0 {
			s.recordSpend(delta)
		}

		if ev.IsError || strings.HasPrefix(ev.Subtype, "error") {
			text := ev.Result
			if text == "" {
				text = "the turn ended with an error (" + ev.Subtype + ")"
			}
			s.publish(Event{Kind: KindError, Text: text, Cost: cost})
			return
		}
		s.publish(Event{Kind: KindDone, Cost: cost})
	}
}

// alreadyStreamed reports whether a completed block's text has already gone
// out as deltas. Empty text counts as delivered: there is nothing to show.
func (s *Session) alreadyStreamed(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.streamed.String(), trimmed)
}

// summarizeTool reduces a tool's input to one line for the panel.
//
// The point is to show what the tutor looked at — reading your solution is
// worth seeing — without pasting whole file contents into a sidebar.
func summarizeTool(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	// In rough order of how well each identifies what happened.
	for _, key := range []string{"file_path", "path", "command", "pattern", "query", "url", "prompt"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return firstLine(v, 120)
		}
	}
	return ""
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
