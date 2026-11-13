// Package tutor runs a local coding agent, one session per problem, and turns
// its event stream into something a browser panel can render.
//
// Claude runs in print mode with streaming JSON on both ends:
//
//	claude -p --input-format stream-json --output-format stream-json --verbose
//
// That shape matters. In this mode the process is long-lived: it reads one
// newline-delimited user message from stdin, works, emits events, and goes back
// to waiting rather than exiting. So a conversation is one process, not one
// process per turn, and closing stdin is what ends it. Codex uses its
// app-server JSONL protocol instead.
package tutor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
)

// LearningPolicy applies to conversation and assessment alike.
const LearningPolicy = `This is a learning project, not an interview. Encourage questions, thinking aloud, experiments, and self-correction, especially on a first solve. Questions about the statement, the solution language's syntax, or the user's own reasoning are not algorithmic hints and must not lower the rating or raise the hint rung. Count a hint only when substantive algorithmic guidance was actually supplied that the user needed to make progress; distinguish it from clarification or confirmation of an approach they already found. Rate independence and demonstrated understanding, not speed, number of questions, polished wording, or ordinary thinking time. An independently completed correct solution with sound understanding merits 4 even after questions or self-correction; use 3 for substantive assistance or remaining uncertainty about solving independently, and 5 for demonstrated fluent unaided recall, not a stopwatch threshold. Ratings schedule practice; they are not rewards or punishments. On repeat solves, use earlier learning evidence to avoid asking the same questions already answered correctly and confidently. Prior understanding guides which questions to skip, but current code and reasoning must still support today's rating. Ask only about a new gap, changed approach, regression, or uncertainty; skip follow-up questions when sufficient evidence is already available. Never expose earlier solutions during a cold solve. The scaffold's own output is not the user's work and must not be reported as a fault in it: an unused generated import (Python's typing names, Go helper types for a shape this problem does not need), the generated TODO comment, and an empty personal test table are all artefacts of the generator, and the statement's examples are already asserted by the generated practice suite beside it. Do not raise them as defects, do not let them affect the rating, and bring them up only if the user asks or the code genuinely depends on one.`

// Mode is how much the browser session is allowed to do.
type Mode string

const (
	// ModeFull is the terminal equivalent: nothing is withheld.
	//
	// There are no interactive permission prompts without a terminal, so
	// "behave like the terminal" means approving everything up front. Note what
	// that implies: with Bash available, a path rule against writing solutions
	// would be decoration, because `echo ... > solutions/x.go` never goes
	// through a file-writing tool. In this mode the repository's rule against
	// solving problems for the user rests on CLAUDE.md and the seed prompt —
	// on asking, not on a mechanism.
	ModeFull Mode = "full"

	// ModeTutor is read-only. That lets the tutor inspect the user's work while
	// making the repository's no-solutions-edits rule a tool restriction.
	ModeTutor Mode = "tutor"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeFull || m == ModeTutor }

// Event is one thing worth showing in the panel.
//
// Claude emits dozens of event types; this is the handful the UI needs. The
// translation happens here rather than in JavaScript so the page is not coupled
// to the CLI's wire format, and so a new event type upstream cannot put noise
// on screen.
type Event struct {
	Seq  int    `json:"seq"`
	Kind string `json:"kind"`

	Text   string   `json:"text,omitempty"`
	Tool   string   `json:"tool,omitempty"`
	Detail string   `json:"detail,omitempty"`
	Model  string   `json:"model,omitempty"`
	Mode   string   `json:"mode,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	Cost   float64  `json:"cost,omitempty"`
}

// The event kinds the panel understands.
const (
	KindReady              = "ready"               // the session started; carries model, mode, tools
	KindUser               = "user"                // the user's own turn, kept so a reload shows it
	KindText               = "text"                // a chunk of assistant prose
	KindTool               = "tool"                // the assistant used a tool
	KindDenied             = "denied"              // a tool call was refused by the permission rules
	KindDone               = "done"                // the turn finished; carries the cost so far
	KindError              = "error"               // something went wrong in or around the process
	KindExit               = "exit"                // the process is gone
	KindInternalUser       = "internal-user"       // a private evaluation prompt
	KindInternalText       = "internal-text"       // private model output used by the web workflow
	KindSubmissionQuestion = "submission-question" // a question from the submit flow
	KindSubmissionDone     = "submission-done"     // final rating is saved
)

// subBuffer is how many events a slow browser may fall behind before it is
// dropped. A dropped subscriber reconnects and replays the transcript, so the
// cost of being wrong here is one reconnect, while blocking the reader would
// stall the session for everyone.
const subBuffer = 256

// Session is one local agent process and conversation, for one problem.
type Session struct {
	storage        *studydb.Scope
	storageID      int64
	restored       bool
	persistenceErr error
	Slug           string
	Problem        catalog.Problem
	Role           Role
	Started        time.Time

	mu      sync.Mutex
	id      string
	model   string
	tools   []string
	cost    float64
	seq     int
	events  []Event
	subs    map[chan Event]struct{}
	running bool
	// streamed is the text the turn in progress has already delivered in
	// pieces, so a completed message does not publish it a second time.
	//
	// This holds the text rather than a bool on purpose. A flag is consumed by
	// the first completed message of the turn, and a CLI that splits one reply
	// across several of them — a thinking block, then a tool call, then the
	// prose — would let the later blocks through in full, printing the whole
	// answer twice. Comparing the text cannot be fooled that way.
	streamed     strings.Builder
	internalTurn bool
	spend        spendHook

	cmd        *exec.Cmd
	codex      *codexClient
	deep       *deepseekClient
	stdin      interface{ Close() error }
	writer     *bufio.Writer
	stderr     strings.Builder
	stderrDone chan struct{}
	// eventsDone closes when the stdout reader has drained the pipe. Wait
	// closes both pipes when it sees the process exit, so reaping before the
	// readers finish truncates whatever was still in flight — see
	// waitForProcess.
	eventsDone chan struct{}
	done       chan struct{}
}

// Info is a snapshot of a session, for rendering the panel's header.
type Info struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Tools   []string `json:"tools"`
	Cost    float64  `json:"cost"`
	Turns   int      `json:"turns"`
	Running bool     `json:"running"`
}

// Info returns a snapshot. Safe to call from any goroutine.
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := 0
	for _, e := range s.events {
		if e.Kind == KindUser {
			turns++
		}
	}
	return Info{ID: s.id, Model: s.model, Tools: s.tools, Cost: s.cost, Turns: turns, Running: s.running}
}

// Transcript returns a stable copy of the session events for evaluations.
func (s *Session) Transcript() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// Subscribe returns the transcript so far and a channel of everything after it.
//
// Both come from under one lock, so an event cannot slip between the replay and
// the live stream and be lost or shown twice.
func (s *Session) Subscribe() (replay []Event, ch chan Event, cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	replay = make([]Event, len(s.events))
	copy(replay, s.events)
	ch = make(chan Event, subBuffer)
	s.subs[ch] = struct{}{}
	return replay, ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// publish records an event and hands it to every live subscriber.
func (s *Session) publish(e Event) {
	s.mu.Lock()
	s.publishLocked(e)
	s.mu.Unlock()
}

// publishLocked records an event while the caller already holds s.mu.
func (s *Session) publishLocked(e Event) {
	if s.internalTurn {
		switch e.Kind {
		case KindUser:
			e.Kind = KindInternalUser
			e.Text = ""
		case KindText:
			e.Kind = KindInternalText
		}
		if e.Kind == KindDone || e.Kind == KindError || e.Kind == KindExit {
			s.internalTurn = false
		}
	}
	s.seq++
	e.Seq = s.seq
	if s.storage != nil {
		data, err := json.Marshal(e)
		if err == nil {
			err = s.storage.AppendEvent(s.storageID, e.Seq, data)
		}
		if err != nil {
			s.persistenceErr = err
			e.Kind = KindError
			e.Text = "Could not save tutor conversation: " + err.Error()
		}
	}
	s.events = append(s.events, e)
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
			// This browser is too far behind to keep up; drop it rather than
			// block the process reader.
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// Send delivers one user turn to the running process.
func (s *Session) Send(text string) error {
	return s.SendAs(text, text)
}

// SendAs sends a full instruction while showing a short label in the transcript.
func (s *Session) SendAs(text, display string) error {
	if s.deep != nil {
		return s.deep.sendAs(text, display)
	}
	if s.codex != nil {
		return s.codex.sendAs(text, display)
	}
	// The shape the CLI expects on stdin, one JSON object per line.
	msg := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{Type: "user"}
	msg.Message.Role = "user"
	msg.Message.Content = text

	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("the tutor session for %s is not running", s.Slug)
	}
	// A new turn starts with nothing delivered. The result event resets this
	// too; doing it here as well means a turn that never reported one cannot
	// leave text behind that suppresses the next turn's reply.
	s.streamed.Reset()
	if _, err := s.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("sending to the tutor: %w", err)
	}
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("sending to the tutor: %w", err)
	}
	// Keep the local transcript ordered before a very fast child can publish
	// its response, and do not record a turn that was never accepted.
	s.publishLocked(Event{Kind: KindUser, Text: display})
	return s.persistenceErr
}

// SendInternal sends an evaluation prompt without showing it in the tutor chat.
func (s *Session) SendInternal(text string) error {
	s.mu.Lock()
	s.internalTurn = true
	s.mu.Unlock()
	if err := s.SendAs(text, ""); err != nil {
		s.mu.Lock()
		s.internalTurn = false
		s.mu.Unlock()
		return err
	}
	return nil
}

// RecordSubmissionAnswer keeps the user's reply for the evaluator without
// starting a second, public model turn alongside the private assessment.
func (s *Session) RecordSubmissionAnswer(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("nothing to send")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("the tutor session for %s is not running", s.Slug)
	}
	s.publishLocked(Event{Kind: KindUser, Text: text})
	return s.persistenceErr
}

// PublishSubmissionQuestion exposes a decision made by the private evaluator.
func (s *Session) PublishSubmissionQuestion(text string) {
	s.publish(Event{Kind: KindSubmissionQuestion, Text: text})
}

// PublishSubmissionDone exposes the final rating after it has been saved.
func (s *Session) PublishSubmissionDone(text string) {
	s.publish(Event{Kind: KindSubmissionDone, Text: text})
}

// Interrupt ends the turn in progress without ending the session: SIGINT is
// handled as "stop what you are doing", where SIGTERM would kill the process.
func (s *Session) Interrupt() error {
	if s.deep != nil {
		return s.deep.interrupt()
	}
	if s.codex != nil {
		return s.codex.interrupt()
	}
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("no tutor session to interrupt")
	}
	return cmd.Process.Signal(os.Interrupt)
}

// closeSession ends the process: stdin EOF is the documented clean exit, with a
// kill as the backstop for one that ignores it.
func (s *Session) closeSession() {
	if s.restored {
		return
	}
	s.mu.Lock()
	stdin, cmd, done := s.stdin, s.cmd, s.done
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// spendHook is told what a turn cost, as an increment.
//
// Providers report cost in opposite shapes — the Claude CLI gives a running
// total for the session, an HTTP API gives usage for the one request — so the
// session converts its own shape into an increment and this is the only thing
// the budget ever sees.
type spendHook func(scope *studydb.Scope, delta float64)

// recordSpend reports what the turn just cost.
//
// Written as it arrives rather than at the end of a session: cost lives only in
// memory, so a restart would otherwise hand everybody their allowance back.
func (s *Session) recordSpend(delta float64) {
	s.mu.Lock()
	hook, scope := s.spend, s.storage
	s.mu.Unlock()
	if hook == nil || scope == nil {
		return
	}
	hook(scope, delta)
}
