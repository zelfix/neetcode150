package tutor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"neetcode150/internal/lang"
	"neetcode150/internal/studydb"
	"neetcode150/internal/workspace"
)

// deepseekRoot lays out just enough of a project for the prompt builder: the
// protocol it must quote, the statement, and one person's solution.
func deepseekRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tutor/TUTOR.md", "# Protocol\n\nClimb the ladder ONE RUNG PER REQUEST.\n")
	write("tutor/references/complexity.md", "Read the target complexity off the constraints.\n")
	write(filepath.Join(twoSum.ContentDir(), "problem.md"), "---\ntitle: Two Sum\n---\n\nFind the two indices.\n")
	write(filepath.Join(workspace.Owner(root).PkgDir(twoSum), "solution.go"),
		"package twosum\n\nfunc twoSum(nums []int, target int) []int { return nil }\n")
	write(filepath.Join(workspace.Owner(root).PkgDir(twoSum), "solution_test.go"),
		"package twosum\n\n// MY OWN TEST CASE\n")
	return root
}

// sseReply writes one streamed answer followed by a usage report, which is the
// shape the API uses when stream_options.include_usage is set.
func sseReply(w http.ResponseWriter, chunks []string, usage deepseekUsage) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, c := range chunks {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", quote(c))
		if flusher != nil {
			flusher.Flush()
		}
	}
	raw, _ := json.Marshal(usage)
	fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":%s}\n\n", raw)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// deepseekManager points a real Manager at a stand-in server, which is what
// keeps these tests from costing anything.
func deepseekManager(t *testing.T, root string, handler http.HandlerFunc) (*Manager, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("DEEPSEEK_BASE_URL", srv.URL)
	m := NewForProvider(root, ProviderDeepSeek, ModeTutor, 0)
	t.Cleanup(m.Close)
	return m, srv
}

func TestDeepSeekStreamsAnAnswerAndChargesTheTurn(t *testing.T) {
	root := deepseekRoot(t)
	var gotAuth string
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		sseReply(w, []string{"What does ", "the map hold?"},
			deepseekUsage{PromptTokens: 10000, PromptCacheMiss: 10000, CompletionTokens: 50})
	})

	var mu sync.Mutex
	var charged float64
	m.OnSpend(func(_ *studydb.Scope, delta float64) {
		mu.Lock()
		charged += delta
		mu.Unlock()
	})

	// A real scope, because there is nobody to charge without one — which is
	// also why a nil scope must stay free rather than panic.
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, _, ch := startTurn(t, m, db.Owner(), twoSum, "where do I start?")
	if got := textOf(collect(t, ch, KindDone)); got != "What does the map hold?" {
		t.Fatalf("reply = %q", got)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("no bearer token was sent: %q", gotAuth)
	}

	// 10k miss at $0.66/M plus 50 out at $1.98/M, halved off-peak.
	want := pricesFor(nil, DefaultDeepSeekModel, time.Now())
	expect := 10000*want.CacheMiss/1e6 + 50*want.Output/1e6
	mu.Lock()
	defer mu.Unlock()
	if charged < expect*0.99 || charged > expect*1.01 {
		t.Errorf("charged %.6f, want about %.6f", charged, expect)
	}
	if charged == 0 {
		t.Error("the turn was free, which means usage never arrived")
	}
}

// The split between the system prompt and the turn is what makes the cache
// work, so it is asserted rather than assumed: the protocol must be in the
// stable half and the person's code in the volatile one.
func TestDeepSeekKeepsTheCodeOutOfTheCachedPrefix(t *testing.T) {
	root := deepseekRoot(t)
	var body deepseekRequest
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		sseReply(w, []string{"ok"}, deepseekUsage{PromptTokens: 1, PromptCacheMiss: 1, CompletionTokens: 1})
	})
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scope := db.Owner()
	if err := scope.SaveDocument(twoSum.Slug, "hints", "## Hint 1\n\nA hash map.\n"); err != nil {
		t.Fatal(err)
	}

	// The system prompt is assembled inside Start rather than when the turn is
	// sent, so the hints saved above have to be in place before it.
	_, _, ch := startTurn(t, m, scope, twoSum, "a question")
	collect(t, ch, KindDone)

	if len(body.Messages) < 2 || body.Messages[0].Role != "system" {
		t.Fatalf("unexpected message shape: %+v", body.Messages)
	}
	system := body.Messages[0].Content
	turn := body.Messages[len(body.Messages)-1].Content

	for _, want := range []string{"ONE RUNG PER REQUEST", "Find the two indices", "A hash map", "no tools"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt is missing %q", want)
		}
	}
	// Source changes every turn, so if it were in the prefix every turn would
	// miss the cache — which costs thirty times as much per input token.
	if strings.Contains(system, "func twoSum") {
		t.Error("the solution is in the cached prefix, so every turn will miss the cache")
	}
	if !strings.Contains(turn, "func twoSum") {
		t.Error("the turn does not carry the current solution")
	}
	if !strings.Contains(turn, "MY OWN TEST CASE") {
		t.Error("the turn does not carry their own tests")
	}
	if !strings.Contains(turn, "a question") {
		t.Error("the turn lost the person's own message")
	}
}

// The generated fixture wall says nothing about how somebody is thinking and
// would crowd out everything that does.
func TestDeepSeekLeavesTheGeneratedSuiteOut(t *testing.T) {
	root := deepseekRoot(t)
	dir := filepath.Join(root, workspace.Owner(root).PkgDir(twoSum))
	if err := os.WriteFile(filepath.Join(dir, "practice_cases_test.go"),
		[]byte("package twosum\n\n// GENERATED FIXTURES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var body deepseekRequest
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		sseReply(w, []string{"ok"}, deepseekUsage{PromptTokens: 1, PromptCacheMiss: 1, CompletionTokens: 1})
	})
	_, _, ch := startTurn(t, m, nil, twoSum, "q")
	collect(t, ch, KindDone)
	for _, msg := range body.Messages {
		if strings.Contains(msg.Content, "GENERATED FIXTURES") {
			t.Error("the generated practice suite was sent")
		}
	}
}

func TestDeepSeekCostFollowsTheCacheSplitAndTheClock(t *testing.T) {
	// Monday 02:00 UTC is peak; Sunday is not.
	peak := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	off := time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC)
	if !isPeak(peak) {
		t.Error("Monday 02:00 UTC should be peak")
	}
	if isPeak(off) {
		t.Error("Sunday should never be peak")
	}

	u := deepseekUsage{PromptTokens: 1_000_000, PromptCacheHit: 900_000, PromptCacheMiss: 100_000, CompletionTokens: 1_000}
	cheap, dear := u.cost(nil, DefaultDeepSeekModel, off), u.cost(nil, DefaultDeepSeekModel, peak)
	if dear < cheap*1.99 || dear > cheap*2.01 {
		t.Errorf("peak %.5f is not double off-peak %.5f", dear, cheap)
	}
	// A hit is far cheaper than a miss, which is the whole reason for the split.
	allMiss := deepseekUsage{PromptTokens: 1_000_000, PromptCacheMiss: 1_000_000}
	if allMiss.cost(nil, DefaultDeepSeekModel, off) < u.cost(nil, DefaultDeepSeekModel, off)*5 {
		t.Error("a fully uncached prompt should cost far more than a mostly cached one")
	}
	// An unknown model must over-estimate rather than under-charge.
	if u.cost(nil, "deepseek-who", off) < u.cost(nil, "deepseek-flash", off) {
		t.Error("an unknown model priced below the cheapest known one")
	}
	// A response that reports no split at all is treated as uncached.
	noSplit := deepseekUsage{PromptTokens: 1_000_000, CompletionTokens: 0}
	if noSplit.cost(nil, DefaultDeepSeekModel, off) != allMiss.cost(nil, DefaultDeepSeekModel, off) {
		t.Error("an unsplit usage report should be charged as a miss")
	}
}

func TestDeepSeekReportsAServiceFailureInWords(t *testing.T) {
	root := deepseekRoot(t)
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	})
	s, err := m.Start(nil, twoSum)
	if err != nil {
		t.Fatal(err)
	}
	// A 429 is transient, so this one is retried: on the real backoff the test
	// would sit out a second and a half of sleeps before the error it is
	// waiting for. Replacing the wait is only safe before the first turn, which
	// is the other reason the turn is sent last.
	s.deep.retryWait = func(int, time.Duration) time.Duration { return time.Millisecond }
	_, ch, stop := s.Subscribe()
	defer stop()
	if err := s.Send("q"); err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch, KindError)
	last := events[len(events)-1]
	if !strings.Contains(last.Text, "429") && !strings.Contains(last.Text, "rate limited") {
		t.Errorf("the failure was not explained: %q", last.Text)
	}
}

// A busy service is tried again before the person sees an error, and a
// Retry-After is respected rather than guessed at.
func TestDeepSeekRetriesATransientFailure(t *testing.T) {
	root := deepseekRoot(t)
	var calls int32
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		sseReply(w, []string{"third time"}, deepseekUsage{})
	})
	s, err := m.Start(nil, twoSum)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	s.deep.retryWait = func(attempt int, retryAfter time.Duration) time.Duration {
		waits = append(waits, retryAfter)
		return time.Millisecond
	}
	_, ch, stop := s.Subscribe()
	defer stop()
	if err := s.Send("q"); err != nil {
		t.Fatal(err)
	}
	if got := textOf(collect(t, ch, KindDone)); got != "third time" {
		t.Fatalf("reply = %q", got)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("made %d requests, want 3", calls)
	}
	if len(waits) != 2 || waits[0] != time.Second {
		t.Errorf("Retry-After was not passed to the wait: %v", waits)
	}
}

// A refusal is not retried: the second request would fail the same way and
// the person would wait for nothing.
func TestDeepSeekDoesNotRetryARefusal(t *testing.T) {
	root := deepseekRoot(t)
	var calls int32
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	})
	_, _, ch := startTurn(t, m, nil, twoSum, "q")
	collect(t, ch, KindError)
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("a 401 was retried: %d requests", calls)
	}
}

func TestDeepSeekRatesLoadFromAFileAndRejectAFreeModel(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good.json", `{"deepseek-pro": {
		"peak": {"cache_hit": 1, "cache_miss": 2, "output": 4},
		"off_peak": {"cache_hit": 0.5, "cache_miss": 1, "output": 2}}}`)
	rates, err := LoadDeepSeekRates(good)
	if err != nil {
		t.Fatal(err)
	}
	u := deepseekUsage{CompletionTokens: 1_000_000}
	if got := u.cost(rates, "deepseek-pro", time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)); got != 2 {
		t.Errorf("a loaded off-peak output price was not used: %v", got)
	}
	if got := u.cost(rates, "unknown", time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)); got != 2 {
		t.Errorf("an unknown model was not priced as the dearest listed: %v", got)
	}
	for name, body := range map[string]string{
		"free.json":    `{"deepseek-pro": {"peak": {"cache_hit": 0, "cache_miss": 2, "output": 4}, "off_peak": {"cache_hit": 1, "cache_miss": 1, "output": 2}}}`,
		"empty.json":   `{}`,
		"unknown.json": `{"deepseek-pro": {"peak": {"cache_hit": 1, "cache_miss": 2, "output": 4, "extra": 1}, "off_peak": {"cache_hit": 1, "cache_miss": 1, "output": 2}}}`,
	} {
		if _, err := LoadDeepSeekRates(write(name, body)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestDeepSeekWithoutAKeyIsUnavailable(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	m := NewForProvider(t.TempDir(), ProviderDeepSeek, ModeTutor, 0)
	defer m.Close()
	ok, why := m.Available()
	if ok {
		t.Fatal("a tutor with no key reported itself available")
	}
	if !strings.Contains(why, "DEEPSEEK_API_KEY") {
		t.Errorf("the reason does not name the key: %q", why)
	}
	if _, err := m.Send(nil, twoSum, "q"); err == nil {
		t.Error("Send succeeded with no key")
	}
}

// This is what lets guests have the panel at all.
func TestDeepSeekReportsNoFileAccess(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k")
	deep := NewForProvider(t.TempDir(), ProviderDeepSeek, ModeTutor, 0)
	defer deep.Close()
	if deep.FileAccess() {
		t.Error("the HTTP provider claims file access")
	}
	cli := NewForProvider(t.TempDir(), ProviderClaude, ModeTutor, 0)
	defer cli.Close()
	if !cli.FileAccess() {
		t.Error("a CLI provider should report file access")
	}
}

// A restored conversation continues rather than starting over, or the person is
// asked again what they already answered.
func TestDeepSeekResumesAStoredConversation(t *testing.T) {
	root := deepseekRoot(t)
	var body deepseekRequest
	handler := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		sseReply(w, []string{"Carrying on."}, deepseekUsage{PromptTokens: 1, PromptCacheMiss: 1, CompletionTokens: 1})
	}
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scope := db.Owner()

	first, _ := deepseekManager(t, root, handler)
	_, _, ch := startTurn(t, first, scope, twoSum, "my first question")
	collect(t, ch, KindDone)
	first.Close()

	second, _ := deepseekManager(t, root, handler)
	_, _, ch2 := startTurn(t, second, scope, twoSum, "my second question")
	collect(t, ch2, KindDone)

	var asked []string
	for _, msg := range body.Messages {
		if msg.Role == "user" {
			asked = append(asked, msg.Content)
		}
	}
	joined := strings.Join(asked, "\n")
	if !strings.Contains(joined, "my first question") {
		t.Errorf("the earlier turn was not replayed: %v", asked)
	}
	if !strings.Contains(joined, "my second question") {
		t.Errorf("the new turn is missing: %v", asked)
	}
	var replies int
	for _, msg := range body.Messages {
		if msg.Role == "assistant" {
			replies++
		}
	}
	if replies == 0 {
		t.Error("the earlier answer was not replayed, so the model will repeat itself")
	}
}

// A Python problem gets the Python reference and the Python file, and not the
// Go ones: a second idioms file would cut the active one's budget and invite
// advice about the language the person is not writing.
func TestDeepSeekSpeaksTheProblemsLanguage(t *testing.T) {
	root := deepseekRoot(t)
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tutor/references/go-idioms.md", "GO IDIOMS HERE\n")
	write("tutor/references/python-idioms.md", "PYTHON IDIOMS HERE\n")
	write(filepath.Join(workspace.Owner(root).PkgDir(twoSum), "solution.py"), "class Solution:\n    pass  # PY SOURCE\n")
	write(filepath.Join(workspace.Owner(root).PkgDir(twoSum), "practice_cases.py"), "SPEC = 'GENERATED PY FIXTURES'\n")
	write(filepath.Join(workspace.Owner(root).PkgDir(twoSum), "test_solution.py"), "# MY PY TEST\n")

	var body deepseekRequest
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		sseReply(w, []string{"ok"}, deepseekUsage{PromptTokens: 1, PromptCacheMiss: 1, CompletionTokens: 1})
	})
	m.OnLanguage(func(*studydb.Scope, string) lang.Lang { return lang.Of(lang.Python) })
	db, err := studydb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, _, ch := startTurn(t, m, db.Owner(), twoSum, "q")
	collect(t, ch, KindDone)

	system, turn := body.Messages[0].Content, body.Messages[len(body.Messages)-1].Content
	if !strings.Contains(system, "PYTHON IDIOMS HERE") || strings.Contains(system, "GO IDIOMS HERE") {
		t.Error("the system prompt does not carry exactly the Python reference")
	}
	if !strings.Contains(system, "solving it in Python") {
		t.Error("the system prompt does not name the language")
	}
	if !strings.Contains(turn, "```python\nclass Solution:") || strings.Contains(turn, "func twoSum") {
		t.Errorf("the turn does not carry the Python file:\n%s", turn)
	}
	if !strings.Contains(turn, "MY PY TEST") || strings.Contains(turn, "GENERATED PY FIXTURES") {
		t.Error("the turn should carry the person's own tests and not the generated suite")
	}
}
