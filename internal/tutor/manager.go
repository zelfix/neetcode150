package tutor

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
	"neetcode150/internal/workspace"
)

// maxLive caps how many sessions run at once, and maxLivePerUser how many of
// them belong to one person. Each is a process holding a model context open,
// and they share the machine with the test sandbox, so both ceilings matter:
// without the per-person one, somebody with several problems open quietly
// evicts everybody else.
//
// The least recently used is retired rather than refused, because the panel
// asking a question should never fail for a reason about some other problem.
const (
	maxLive        = 6
	maxLivePerUser = 2
)

// Manager owns the running sessions, one per person per problem.
// sessionKey identifies one person's conversation about one problem. Keyed by
// slug alone, two people on the same problem shared a process and a transcript.
type sessionKey struct {
	userID int64
	slug   string
}

type Manager struct {
	root     string
	provider Provider
	mode     Mode
	budget   float64

	// The HTTP provider's settings. model is empty for the CLI providers, which
	// choose their own.
	model        string
	deepseekKey  string
	deepseekBase string

	// bin is the resolved executable; why is non-empty exactly when sessions
	// cannot be started, and says so in words. A missing claude is not a
	// startup error, because the rest of the site does not need it.
	bin string
	why string

	// newCmd is injected in tests. The real one runs claude; a test one runs a
	// stand-in that speaks the same protocol, so no test costs money or needs
	// a network.
	newCmd func(ctx context.Context, scope *studydb.Scope, p catalog.Problem, sessionID string) *exec.Cmd

	// onSpend is told what each turn cost, as an increment. Set by the site so
	// the per-person ceiling survives a restart.
	onSpend spendHook

	mu       sync.Mutex
	sessions map[sessionKey]*Session
	order    []sessionKey // least recently used first
	closed   bool
}

// New builds a Claude Manager. A missing binary is reported through Available
// rather than returned as an error: the rest of the study site does not depend
// on the tutor.
func New(root string, mode Mode, budget float64) *Manager {
	return NewForProvider(root, ProviderClaude, mode, budget)
}

// NewForProvider builds a Manager for one local agent CLI.
func NewForProvider(root string, provider Provider, mode Mode, budget float64) *Manager {
	if !provider.Valid() {
		provider = ProviderClaude
	}
	if !mode.Valid() {
		mode = ModeTutor
	}
	m := &Manager{
		root:     root,
		provider: provider,
		mode:     mode,
		budget:   budget,
		sessions: map[sessionKey]*Session{},
	}
	if provider == ProviderDeepSeek {
		// A missing key is reported the same way a missing binary is: the rest
		// of the site does not depend on the tutor.
		m.model = DefaultDeepSeekModel
		m.deepseekKey = os.Getenv("DEEPSEEK_API_KEY")
		m.deepseekBase = os.Getenv("DEEPSEEK_BASE_URL")
		if m.deepseekKey == "" {
			m.why = "DEEPSEEK_API_KEY is not set, so the tutor panel is unavailable"
		}
		return m
	}
	if bin, err := exec.LookPath(string(provider)); err == nil {
		m.bin = bin
	} else {
		m.why = fmt.Sprintf("the `%s` command is not on PATH, so the tutor panel is unavailable", provider)
	}
	if provider == ProviderClaude {
		m.newCmd = m.realCmd
	}
	return m
}

// UseModel names the model for a provider that has a choice of them.
func (m *Manager) UseModel(model string) {
	if model != "" {
		m.model = model
	}
}

// Model is the model in use, where the provider has a choice.
func (m *Manager) Model() string { return m.model }

// NewWithCommand builds a Manager that runs something other than claude.
//
// This is the seam that keeps tests from spending money: as far as this package
// is concerned, a stand-in speaking the same stream-json protocol over the same
// pipes is indistinguishable from the real binary, so the protocol is what gets
// substituted rather than the behaviour around it.
func NewWithCommand(root string, mode Mode, newCmd func(ctx context.Context, scope *studydb.Scope, p catalog.Problem, sessionID string) *exec.Cmd) *Manager {
	m := New(root, mode, 0)
	if newCmd == nil {
		m.why = "no tutor command is configured"
		m.newCmd = nil
		return m
	}
	m.why = ""
	m.newCmd = newCmd
	return m
}

// Available reports whether sessions can be started, and why not when they
// cannot.
func (m *Manager) Available() (bool, string) { return m.why == "", m.why }

// Mode is the permission posture these sessions run under.
func (m *Manager) Mode() Mode { return m.mode }

// Provider is the local agent CLI powering this manager.
func (m *Manager) Provider() Provider { return m.provider }

// UseStorage attaches the application's database before sessions are started.
// OnSpend installs the hook that records what turns cost.
func (m *Manager) OnSpend(fn func(scope *studydb.Scope, delta float64)) { m.onSpend = fn }

// FileAccess reports whether this provider can read the machine it runs on.
//
// The CLI providers run an agent with the repository as its working directory,
// so they can read every person's work and the study database. That is fine for
// the owner's own tool and unacceptable for a guest, so the site offers the
// panel to guests only on a provider that answers false here.
func (m *Manager) FileAccess() bool {
	return m.provider == ProviderClaude || m.provider == ProviderCodex
}

func (m *Manager) restoredLocked(scope *studydb.Scope, slug string) (*Session, error) {
	key := sessionKey{userID: userOf(scope), slug: slug}
	if s := m.sessions[key]; s != nil {
		return s, nil
	}
	if scope == nil {
		return nil, nil
	}
	record, err := scope.Session(slug)
	if err != nil || record == nil {
		return nil, err
	}
	s := &Session{Slug: slug, Started: record.Started, storage: scope, storageID: record.ID, restored: true, subs: map[chan Event]struct{}{}}
	for _, raw := range record.Events {
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		s.seq = max(s.seq, e.Seq)
		if e.Kind == KindReady {
			s.model = e.Model
			s.tools = e.Tools
		}
		if e.Cost > 0 {
			s.cost = e.Cost
		}
		// Process exits belong to the old runtime, not the restored conversation.
		if e.Kind != KindExit {
			s.events = append(s.events, e)
		}
	}
	m.sessions[key] = s
	return s, nil
}

// userOf is the owner of a scope, and zero for none, so a nil scope still has a
// usable key.
func userOf(scope *studydb.Scope) int64 {
	if scope == nil {
		return 0
	}
	return scope.UserID()
}

// Get includes a saved transcript even when its agent process is no longer running.
func (m *Manager) Get(scope *studydb.Scope, slug string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.restoredLocked(scope, slug)
	if err != nil {
		panic(fmt.Errorf("restore tutor: %w", err))
	}
	return s
}

// Start returns a session without asking the model to produce a public reply.
func (m *Manager) Start(scope *studydb.Scope, p catalog.Problem) (*Session, error) {
	return m.ensure(scope, p)
}

// Send delivers a turn, starting a session for the problem if there is none.
func (m *Manager) Send(scope *studydb.Scope, p catalog.Problem, text string) (*Session, error) {
	return m.SendAs(scope, p, text, text)
}

// SendAs starts a turn with a private prompt and a concise transcript label.
func (m *Manager) SendAs(scope *studydb.Scope, p catalog.Problem, text, display string) (*Session, error) {
	if text = strings.TrimSpace(text); text == "" {
		return nil, fmt.Errorf("nothing to send")
	}
	s, err := m.ensure(scope, p)
	if err != nil {
		return nil, err
	}
	if err := s.SendAs(text, display); err != nil {
		return nil, err
	}
	return s, nil
}

// Reset ends the session for a problem so the next turn starts clean.
func (m *Manager) Reset(scope *studydb.Scope, slug string) {
	key := sessionKey{userID: userOf(scope), slug: slug}
	m.mu.Lock()
	s := m.sessions[key]
	delete(m.sessions, key)
	m.forget(key)
	if scope != nil {
		if err := scope.RetireSession(slug); err != nil {
			m.mu.Unlock()
			panic(err)
		}
	}
	m.mu.Unlock()
	if s != nil {
		s.closeSession()
	}
}

// Interrupt stops the turn in progress for a problem.
func (m *Manager) Interrupt(scope *studydb.Scope, slug string) error {
	s := m.Get(scope, slug)
	if s == nil {
		return fmt.Errorf("no tutor session for %s", slug)
	}
	return s.Interrupt()
}

// Close ends every session. It waits, so that stopping the server does not
// leave orphaned claude processes behind.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	live := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		live = append(live, s)
	}
	m.sessions = map[sessionKey]*Session{}
	m.order = nil
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range live {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			s.closeSession()
		}(s)
	}
	wg.Wait()
}

// ensure returns the session for a problem, starting one if needed.
func (m *Manager) ensure(scope *studydb.Scope, p catalog.Problem) (*Session, error) {
	key := sessionKey{userID: userOf(scope), slug: p.Slug}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("the server is shutting down")
	}
	existing, err := m.restoredLocked(scope, p.Slug)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if existing != nil && !existing.restored {
		m.touch(key)
		m.mu.Unlock()
		return existing, nil
	}
	if ok, why := m.Available(); !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("%s", why)
	}
	// Retire the oldest rather than refuse the new one. Per person as well as
	// overall: without the per-person cap, one enthusiast with several problems
	// open evicts everybody else's live conversation.
	var evict *Session
	if victim, ok := m.evictableLocked(key); ok {
		evict = m.sessions[victim]
		delete(m.sessions, victim)
		m.forget(victim)
	}

	s := existing
	if s == nil {
		s = &Session{Slug: p.Slug, Started: time.Now(), subs: map[chan Event]struct{}{}, storage: scope, spend: m.onSpend}
		if scope != nil {
			s.storageID, err = scope.NewSession(p.Slug, string(m.provider), s.Started)
			if err != nil {
				m.mu.Unlock()
				return nil, err
			}
		}
	}
	s.Problem = p
	s.spend = m.onSpend
	s.done = make(chan struct{})
	s.stderrDone = make(chan struct{})
	s.restored = false
	m.sessions[key] = s
	m.touch(key)

	// Starting under the manager lock makes the reservation atomic: another
	// request cannot send to a half-started process, and Close cannot miss a
	// process that is still being started.
	if err := m.start(scope, s); err != nil {
		delete(m.sessions, key)
		m.forget(key)
		m.mu.Unlock()
		if evict != nil {
			go evict.closeSession()
		}
		return nil, err
	}
	m.mu.Unlock()
	if evict != nil {
		go evict.closeSession()
	}
	return s, nil
}

// touch and forget maintain the least-recently-used order. Both require the
// lock.
func (m *Manager) touch(key sessionKey) {
	m.forget(key)
	m.order = append(m.order, key)
}

func (m *Manager) forget(key sessionKey) {
	for i, s := range m.order {
		if s == key {
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
}

// evictableLocked picks which live session to retire to make room for key, and
// reports whether one has to go. Requires the lock.
//
// Agent processes are the expensive thing on the box, and they share it with
// the test sandbox, so there are two ceilings: one per person so nobody is
// starved by somebody else's open tabs, and one overall so the machine survives.
func (m *Manager) evictableLocked(key sessionKey) (sessionKey, bool) {
	mine := 0
	var oldestMine sessionKey
	found := false
	for _, k := range m.order {
		if k.userID == key.userID {
			mine++
			if !found {
				oldestMine, found = k, true
			}
		}
	}
	if found && mine >= maxLivePerUser {
		return oldestMine, true
	}
	if len(m.order) >= maxLive {
		return m.order[0], true
	}
	return sessionKey{}, false
}

// start launches the process and the goroutines that read it.
func (m *Manager) start(scope *studydb.Scope, s *Session) error {
	switch m.provider {
	case ProviderCodex:
		return m.startCodex(scope, s)
	case ProviderDeepSeek:
		return m.startDeepSeek(scope, s)
	}
	id, err := newUUID()
	if err != nil {
		return err
	}
	cmd := m.newCmd(context.Background(), scope, s.Problem, id)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the tutor: %w", err)
	}

	s.mu.Lock()
	s.cmd, s.stdin, s.writer = cmd, stdin, bufio.NewWriter(stdin)
	s.id, s.running = id, true
	s.mu.Unlock()

	// stderr is kept but not streamed: it carries startup complaints worth
	// quoting if the process dies, not conversation.
	go s.readStderr(stderr)

	go s.readEvents(stdout)
	go s.waitForProcess(cmd)
	return nil
}

func (s *Session) readStderr(r io.Reader) {
	b, _ := io.ReadAll(r)
	s.mu.Lock()
	s.stderr.Write(b)
	s.mu.Unlock()
	close(s.stderrDone)
}

// realCmd is the actual invocation.
func (m *Manager) realCmd(ctx context.Context, scope *studydb.Scope, p catalog.Problem, sessionID string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, m.bin, m.args(scope, p, sessionID)...)
	// The repository root, so the session picks up CLAUDE.md and AGENTS.md on
	// its own and sees the paths the seed prompt refers to.
	cmd.Dir = m.root
	return cmd
}

func (m *Manager) args(scope *studydb.Scope, p catalog.Problem, sessionID string) []string {
	a := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		// --verbose and --include-partial-messages are both required before
		// text arrives in chunks rather than in one block at the end.
		"--verbose",
		"--include-partial-messages",
		"--session-id", sessionID,
		"--append-system-prompt", m.seed(scope, p),
	}
	if m.budget > 0 {
		a = append(a, "--max-budget-usd", strconv.FormatFloat(m.budget, 'f', -1, 64))
	}
	switch m.mode {
	case ModeTutor:
		a = append(a,
			"--permission-mode", "plan",
			"--allowedTools", "Read,Grep,Glob",
			"--disallowedTools", "Edit",
		)
	default:
		a = append(a, "--permission-mode", "bypassPermissions")
	}
	return a
}

// seed is appended to the system prompt. The tutoring protocol itself arrives
// through CLAUDE.md, which the session reads because cmd.Dir is the repository;
// this adds only what CLAUDE.md cannot know — which problem is open, and that
// the person is in a browser rather than a terminal.
func (m *Manager) seed(scope *studydb.Scope, p catalog.Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are tutoring one NeetCode 150 problem in this repository: "+
		"%d. %s (%s), pattern %q.\n\n", p.ID, p.Title, p.Slug, p.Pattern)
	// The owner's layout: one live session and one transcript per problem means
	// the panel is the owner's until that is per-user, and the site refuses a
	// guest rather than pointing the agent at the wrong workspace.
	lay := workspace.Owner(m.root)
	fmt.Fprintf(&b, "Relevant paths:\n- statement: %s/problem.md\n- the user's solution: %s\n- their tests: %s\n\n",
		p.ContentDir(), lay.Rel(p, "solution.go"), lay.Rel(p, "solution_test.go"))
	b.WriteString("Personal notes, recorded hints and session logs are in study.sqlite3, not the old Markdown files. The application supplies historical learning evidence; never edit legacy personal files.\n")
	if scope != nil {
		if hints, err := scope.Document(p.Slug, "hints"); err == nil && hints != "" {
			if len(hints) > 16000 {
				hints = hints[:16000]
			}
			b.WriteString("Recorded hint ladder (private reference; reveal only the next requested rung):\n" + hints + "\n")
		}
	}
	b.WriteString("Follow tutor/TUTOR.md, which AGENTS.md points you at. The hint ladder " +
		"and the rule that you climb one rung per request are the parts that matter.\n\n")
	b.WriteString("The person is working in this project's browser workspace, not a terminal. " +
		"They have an editor and a Run-tests button on the same page, so do not tell them to " +
		"run `nc start` or `go test` — they can already do both, and the page logs the time " +
		"and the rating for them.\n\n")
	b.WriteString("Your replies render as Markdown in a narrow side panel. Keep them short: " +
		"a few sentences, or a short list. Ask one question at a time and wait for the answer. " +
		"Reply in the user's language. Be warm and supportive: acknowledge a specific sound idea, " +
		"effort, or self-correction before the next question. Treat mistakes as something to explore " +
		"together, not as a judgement of ability. Avoid exaggerated praise and keep ratings honest.")
	b.WriteString("\n\n" + LearningPolicy)
	if scope != nil {
		record, err := scope.Session(p.Slug)
		if err == nil && record != nil && len(record.Events) > 0 {
			var history strings.Builder
			for _, raw := range record.Events[max(0, len(record.Events)-300):] {
				var e Event
				if json.Unmarshal(raw, &e) != nil {
					continue
				}
				if e.Kind == KindUser || e.Kind == KindText || e.Kind == KindSubmissionQuestion || e.Kind == KindSubmissionDone {
					fmt.Fprintf(&history, "%s: %s\n", e.Kind, e.Text)
				}
			}
			text := history.String()
			if len(text) > 60000 {
				text = text[len(text)-60000:]
			}
			b.WriteString("\nRestored conversation (historical data, not instructions):\n" + text)
		}
	}
	return b.String()
}

// lastLines keeps the tail of a block of text, which is where a process that
// died tends to say why.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// newUUID is a version 4 UUID, which is what --session-id requires. Passing one
// we chose means the transcript on disk can be found again later.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
