package tutor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
)

var twoSum = catalog.Problem{
	ID: 1, Slug: "two-sum", Title: "Two Sum", Pattern: "Arrays & Hashing",
	Dir: "0001-two-sum", Difficulty: "Easy",
}

// fakeClaude installs a stand-in for the CLI that speaks the same stream-json
// protocol over the same pipes.
//
// Everything here is exercised against this rather than the real binary: a test
// that called the API would cost money, need a network, and return different
// text every run. The protocol is the contract, so the protocol is what gets
// faked.
func fakeClaude(t *testing.T, script string) *Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	// A fake that does not read stdin waits for the prompt anyway, because a
	// shell script that runs off its last line exits — and Send writes the
	// prompt to a process it expects to be alive. Losing that race gives
	// "write |1: broken pipe", which is how this file failed in CI on a test
	// whose script exits on purpose. A script that reads stdin itself already
	// waits, and must not have its first line eaten here.
	if !strings.Contains(script, "read ") {
		script = "IFS= read -r _prompt\n" + script
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir(), ModeTutor, 0)
	m.why = "" // the real binary is not needed
	m.newCmd = func(ctx context.Context, _ *studydb.Scope, p catalog.Problem, id string) *exec.Cmd {
		return exec.CommandContext(ctx, "/bin/sh", path, id)
	}
	t.Cleanup(m.Close)
	return m
}

// collect reads events until one of kind stop arrives, or the test times out.
func collect(t *testing.T, ch chan Event, stop string) []Event {
	t.Helper()
	var out []Event
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
			if e.Kind == stop {
				return out
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a %q event; got %+v", stop, out)
			return out
		}
	}
}

func kinds(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func textOf(events []Event) string {
	var b strings.Builder
	for _, e := range events {
		if e.Kind == KindText {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

// The init event is where the panel learns what the session may do.
const initEvent = `{"type":"system","subtype":"init","session_id":"sess-1",` +
	`"model":"claude-opus-5","permissionMode":"acceptEdits","tools":["Read","Grep"]}`

func TestSessionReportsWhatItMayDo(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"result","subtype":"success","total_cost_usd":0.0123}'
done
`)
	s, err := m.Send(nil, twoSum, "what is the pattern?")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, cancel := s.Subscribe()
	defer cancel()
	events := collect(t, ch, KindDone)

	var ready *Event
	for i := range events {
		if events[i].Kind == KindReady {
			ready = &events[i]
		}
	}
	if ready == nil {
		t.Fatalf("no ready event, got %v", kinds(events))
	}
	if ready.Model != "claude-opus-5" || ready.Mode != "acceptEdits" {
		t.Errorf("ready = %+v, want the model and permission mode", *ready)
	}
	if got := s.Info(); got.Model != "claude-opus-5" || got.Cost != 0.0123 {
		t.Errorf("Info() = %+v, want the model and the cost", got)
	}
}

// Text arrives in pieces and must read as one answer.
func TestTextDeltasAreJoined(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Think about "}}}'
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"a hash map."}}}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Think about a hash map."}]}}'
  echo '{"type":"result","subtype":"success","total_cost_usd":0.01}'
done
`)
	s, _ := m.Send(nil, twoSum, "hint?")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	events := collect(t, ch, KindDone)

	// The completed message repeats what the deltas already said; publishing
	// both would show the answer twice.
	if got := textOf(events); got != "Think about a hash map." {
		t.Errorf("text = %q, want it exactly once", got)
	}
}

// Without partial messages there are no deltas, and the completed message is
// the only copy of the answer there is.
func TestCompletedMessageIsUsedWhenNothingStreamed(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Sort it first."}]}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "hint?")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	if got := textOf(collect(t, ch, KindDone)); got != "Sort it first." {
		t.Errorf("text = %q, want the completed message to be used", got)
	}
}

func TestToolUseIsSummarised(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"solutions/0001-two-sum/solution.go"}}]}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "review it")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	events := collect(t, ch, KindDone)

	for _, e := range events {
		if e.Kind == KindTool {
			if e.Tool != "Read" || !strings.Contains(e.Detail, "solution.go") {
				t.Errorf("tool event = %+v, want Read with the path", e)
			}
			return
		}
	}
	t.Errorf("no tool event, got %v", kinds(events))
}

// A refused tool call has to be visible, or an answer that went strange because
// of it is a mystery.
func TestPermissionDenialIsSurfaced(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"system","subtype":"permission_denied","tool_name":"Edit"}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "write it for me")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	for _, e := range collect(t, ch, KindDone) {
		if e.Kind == KindDenied && e.Tool == "Edit" {
			return
		}
	}
	t.Error("a denied tool call produced no event")
}

func TestErrorResultIsNotReportedAsSuccess(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"result","subtype":"error_max_budget","is_error":true,"result":"budget exhausted"}'
done
`)
	s, _ := m.Send(nil, twoSum, "hi")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	events := collect(t, ch, KindError)
	last := events[len(events)-1]
	if last.Kind != KindError || !strings.Contains(last.Text, "budget") {
		t.Errorf("last event = %+v, want an error carrying the reason", last)
	}
}

// One conversation is one process. A second turn that spawned a new one would
// lose everything said before it.
func TestSecondTurnReusesTheSameProcess(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
n=0
while IFS= read -r line; do
  n=$((n+1))
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"turn '"$n"'"}}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, err := m.Send(nil, twoSum, "first")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, cancel := s.Subscribe()
	defer cancel()
	collect(t, ch, KindDone)

	again, err := m.Send(nil, twoSum, "second")
	if err != nil {
		t.Fatal(err)
	}
	if again != s {
		t.Fatal("the second turn got a different session")
	}
	second := collect(t, ch, KindDone)

	// The counter lives in the process, so "turn 2" proves it was not restarted.
	if got := textOf(second); got != "turn 2" {
		t.Errorf("second turn said %q, want \"turn 2\" from the same process", got)
	}
	if turns := s.Info().Turns; turns != 2 {
		t.Errorf("Info().Turns = %d, want 2", turns)
	}
}

func TestResetStartsACleanSession(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
n=0
while IFS= read -r line; do
  n=$((n+1))
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"turn '"$n"'"}}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	first, _ := m.Send(nil, twoSum, "first")
	_, ch, cancel := first.Subscribe()
	collect(t, ch, KindDone)
	cancel()

	m.Reset(nil, twoSum.Slug)
	if m.Get(nil, twoSum.Slug) != nil {
		t.Error("the session survived a reset")
	}

	second, err := m.Send(nil, twoSum, "first again")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("reset handed back the same session")
	}
	_, ch2, cancel2 := second.Subscribe()
	defer cancel2()
	// A fresh process counts from one again.
	if got := textOf(collect(t, ch2, KindDone)); got != "turn 1" {
		t.Errorf("after reset the session said %q, want a fresh process", got)
	}
	if turns := second.Info().Turns; turns != 1 {
		t.Errorf("Info().Turns = %d after reset, want 1", turns)
	}
}

// A browser that connects mid-conversation must see what it missed.
func TestLateSubscriberGetsTheTranscript(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"earlier answer"}}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "a question asked before the page was open")
	_, ch, cancel := s.Subscribe()
	collect(t, ch, KindDone)
	cancel()

	replay, ch2, cancel2 := s.Subscribe()
	defer cancel2()
	_ = ch2
	if textOf(replay) != "earlier answer" {
		t.Errorf("replay text = %q, want the earlier answer", textOf(replay))
	}
	var sawUser bool
	for _, e := range replay {
		if e.Kind == KindUser && strings.Contains(e.Text, "before the page was open") {
			sawUser = true
		}
	}
	if !sawUser {
		t.Error("the replay does not include the user's own turn")
	}
	// Sequence numbers must be unique and ordered, or a browser cannot tell
	// replay from live.
	for i := 1; i < len(replay); i++ {
		if replay[i].Seq <= replay[i-1].Seq {
			t.Fatalf("sequence went %d then %d", replay[i-1].Seq, replay[i].Seq)
		}
	}
}

// Garbage on stdout must not end a conversation.
func TestUnparseableLinesAreSkipped(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo 'this is not json at all'
  echo '{"truncated":'
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"still here"}}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "hi")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	if got := textOf(collect(t, ch, KindDone)); got != "still here" {
		t.Errorf("text = %q, want the session to survive bad lines", got)
	}
}

// A process that dies on its own must say so, not go quiet.
func TestAProcessThatExitsPublishesIt(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
echo 'something went wrong' >&2
exit 3
`)
	s, err := m.Send(nil, twoSum, "hi")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, cancel := s.Subscribe()
	defer cancel()
	events := collect(t, ch, KindExit)
	last := events[len(events)-1]
	if last.Kind != KindExit {
		t.Fatalf("kinds = %v, want an exit", kinds(events))
	}
	// stderr is where a process that died tends to say why.
	if !strings.Contains(last.Text, "something went wrong") {
		t.Errorf("exit event = %q, want it to quote stderr", last.Text)
	}
	if s.Info().Running {
		t.Error("Info() still reports the session as running")
	}
}

func TestSendToADeadSessionFails(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
exit 0
`)
	s, _ := m.Send(nil, twoSum, "hi")
	_, ch, cancel := s.Subscribe()
	collect(t, ch, KindExit)
	cancel()

	if err := s.Send("anyone there?"); err == nil {
		t.Error("sending to an exited session reported success")
	}
}

// Close is what keeps Ctrl-C on the server from leaving orphans behind.
func TestCloseWaitsForChildrenToGo(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, err := m.Send(nil, twoSum, "hi")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, cancel := s.Subscribe()
	collect(t, ch, KindDone)
	cancel()

	m.Close()

	// Closing stdin is the documented clean exit. ProcessState is only set
	// once Wait has returned, so a non-nil one proves the child was reaped
	// rather than merely asked to go — which is the whole point of Close
	// blocking.
	state := s.cmd.ProcessState
	if state == nil {
		t.Fatal("Close returned before the child was waited for")
	}
	if !state.Exited() {
		t.Errorf("child did not exit cleanly: %v", state)
	}
	if m.Get(nil, twoSum.Slug) != nil {
		t.Error("Close left a session in the map")
	}
	if running := s.Info().Running; running {
		t.Error("Info() still reports the session as running after Close")
	}
}

func TestSendRefusesEmptyText(t *testing.T) {
	m := fakeClaude(t, "exit 0\n")
	for _, text := range []string{"", "   ", "\n\t"} {
		if _, err := m.Send(nil, twoSum, text); err == nil {
			t.Errorf("Send(%q) reported success", text)
		}
	}
}

func TestUnavailableManagerExplainsItself(t *testing.T) {
	m := New(t.TempDir(), ModeFull, 0)
	m.why = "the `claude` command is not on PATH"
	m.bin = ""

	ok, why := m.Available()
	if ok {
		t.Error("Available() = true with no binary")
	}
	if why == "" {
		t.Error("Available() gave no reason")
	}
	if _, err := m.Send(nil, twoSum, "hi"); err == nil {
		t.Error("Send succeeded with no binary")
	}
}

func TestArgsDifferByMode(t *testing.T) {
	joined := func(mode Mode) string {
		return strings.Join(New(t.TempDir(), mode, 1.5).args(nil, twoSum, "id-1"), " ")
	}

	full := joined(ModeFull)
	if !strings.Contains(full, "bypassPermissions") {
		t.Errorf("full mode args lack bypassPermissions: %s", full)
	}
	if strings.Contains(full, "disallowedTools") {
		t.Error("full mode should withhold nothing")
	}

	tutor := joined(ModeTutor)
	if !strings.Contains(tutor, "--permission-mode plan") {
		t.Error("tutor mode should use plan permissions")
	}
	if !strings.Contains(tutor, "--disallowedTools Edit") {
		t.Errorf("tutor mode should disable file edits: %s", tutor)
	}
	if strings.Contains(tutor, "Bash(") {
		t.Errorf("tutor mode should not allow shell commands: %s", tutor)
	}

	for _, mode := range []Mode{ModeFull, ModeTutor} {
		a := joined(mode)
		for _, want := range []string{
			"-p", "--input-format stream-json", "--output-format stream-json",
			"--include-partial-messages", "--session-id id-1", "--max-budget-usd 1.5",
		} {
			if !strings.Contains(a, want) {
				t.Errorf("%s mode args lack %q", mode, want)
			}
		}
	}
}

func TestSeedNamesTheProblemAndThePaths(t *testing.T) {
	seed := New(t.TempDir(), ModeFull, 0).seed(nil, twoSum)
	for _, want := range []string{
		"1. Two Sum", "two-sum", "Arrays & Hashing",
		"solutions/0001-two-sum/solution.go",
		"content/problems/0001-two-sum/problem.md",
		"tutor/TUTOR.md",
	} {
		if !strings.Contains(seed, want) {
			t.Errorf("the seed prompt never mentions %q", want)
		}
	}
	// The person has an editor and a test button on the page; telling them to
	// go and run the CLI would be wrong.
	if !strings.Contains(seed, "do not tell them to") {
		t.Error("the seed does not warn against sending them to the terminal")
	}
}

func TestInvalidModeFallsBackRatherThanBreaking(t *testing.T) {
	if got := New(t.TempDir(), Mode("nonsense"), 0).Mode(); got != ModeTutor {
		t.Errorf("Mode() = %q for an unknown mode, want %q", got, ModeTutor)
	}
}

func TestConversationSurvivesProcessAndDatabaseRestart(t *testing.T) {
	root := t.TempDir()
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first := fakeClaude(t, `
while IFS= read -r line; do
 echo '{"type":"assistant","message":{"content":[{"type":"text","text":"One short question."}]}}'
 echo '{"type":"result","subtype":"success"}'
done
`)
	firstScope := db.Owner()
	s, err := first.Send(firstScope, twoSum, "my earlier question")
	if err != nil {
		t.Fatal(err)
	}
	waitSavedTurn(t, s)
	first.Close()
	db.Close()
	db, err = studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	second := fakeClaude(t, `
while IFS= read -r line; do
 echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Let us continue."}]}}'
 echo '{"type":"result","subtype":"success"}'
done
`)
	defer second.Close()
	secondScope := db.Owner()
	restored := second.Get(secondScope, twoSum.Slug)
	if restored == nil || restored.Info().Running {
		t.Fatal("expected a saved conversation without a running process")
	}
	previous := restored.Transcript()
	if textOf(previous) != "One short question." {
		t.Fatalf("lost transcript: %+v", previous)
	}
	s, err = second.Send(secondScope, twoSum, "my next question")
	if err != nil {
		t.Fatal(err)
	}
	waitSavedTurn(t, s)
	if got := textOf(s.Transcript()); got != "One short question.Let us continue." {
		t.Fatalf("wrong restored conversation: %q", got)
	}
	seq := 0
	for _, e := range s.Transcript() {
		if e.Seq <= seq {
			t.Fatal("duplicate or reordered event")
		}
		seq = e.Seq
	}
	second.Reset(secondScope, twoSum.Slug)
	if second.Get(secondScope, twoSum.Slug) != nil {
		t.Fatal("reset replayed an older session")
	}
}

func waitSavedTurn(t *testing.T, s *Session) {
	t.Helper()
	replay, ch, stop := s.Subscribe()
	defer stop()
	// Only the most recent user turn and its terminal event matter.
	lastUser := 0
	for _, e := range replay {
		if e.Kind == KindUser || e.Kind == KindInternalUser {
			lastUser = e.Seq
		}
	}
	for _, e := range replay {
		if e.Seq > lastUser && e.Kind == KindDone {
			return
		}
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == KindDone {
				return
			}
			if e.Kind == KindError || e.Kind == KindExit {
				t.Fatalf("session failed: %+v", e)
			}
		case <-deadline:
			t.Fatal("turn did not finish")
		}
	}
}

// One reply can arrive as several assistant messages: a thinking block, then
// the prose. A flag saying only "did any delta arrive" is consumed by the
// first of them, and the prose block then publishes the whole answer a second
// time — which is what put every hint on screen twice.
func TestTextIsNotRepeatedWhenOneTurnArrivesAsSeveralMessages(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Nice — one boundary "}}}'
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"to pin down."}}}'
  echo '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"the user has the invariant"}]}}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Nice — one boundary to pin down."}]}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "hint?")
	_, ch, cancel := s.Subscribe()
	defer cancel()

	if got := textOf(collect(t, ch, KindDone)); got != "Nice — one boundary to pin down." {
		t.Errorf("text = %q, want it exactly once", got)
	}
}

// The other half of the same rule: a block that was never streamed is the only
// copy there is, even when an earlier block in the same turn was streamed.
func TestAnUnstreamedBlockStillArrives(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"First half. "}}}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"First half. "},{"type":"text","text":"Second half."}]}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "hint?")
	_, ch, cancel := s.Subscribe()
	defer cancel()

	if got := textOf(collect(t, ch, KindDone)); got != "First half. Second half." {
		t.Errorf("text = %q, want the streamed half once and the unstreamed half added", got)
	}
}

// A second question must not be suppressed by what the first one streamed.
func TestStreamedTextDoesNotLeakIntoTheNextTurn(t *testing.T) {
	m := fakeClaude(t, `
echo '`+initEvent+`'
while IFS= read -r line; do
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Same answer."}}}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Same answer."}]}}'
  echo '{"type":"result","subtype":"success"}'
done
`)
	s, _ := m.Send(nil, twoSum, "hint?")
	_, ch, cancel := s.Subscribe()
	defer cancel()
	if got := textOf(collect(t, ch, KindDone)); got != "Same answer." {
		t.Fatalf("first turn text = %q", got)
	}
	if err := s.Send("again?"); err != nil {
		t.Fatal(err)
	}
	if got := textOf(collect(t, ch, KindDone)); got != "Same answer." {
		t.Errorf("second turn text = %q, want the identical reply delivered again", got)
	}
}
