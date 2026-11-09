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
	"testing"
	"time"

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

	s, err := m.Send(db.Owner(), twoSum, "where do I start?")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, stop := s.Subscribe()
	defer stop()
	if got := textOf(collect(t, ch, KindDone)); got != "What does the map hold?" {
		t.Fatalf("reply = %q", got)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("no bearer token was sent: %q", gotAuth)
	}

	// 10k miss at $0.66/M plus 50 out at $1.98/M, halved off-peak.
	want := pricesFor(DefaultDeepSeekModel, time.Now())
	expect := 10000*want.cacheMiss/1e6 + 50*want.output/1e6
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

	s, err := m.Send(scope, twoSum, "a question")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, stop := s.Subscribe()
	defer stop()
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
	s, err := m.Send(nil, twoSum, "q")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, stop := s.Subscribe()
	defer stop()
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
	cheap, dear := u.cost(DefaultDeepSeekModel, off), u.cost(DefaultDeepSeekModel, peak)
	if dear < cheap*1.99 || dear > cheap*2.01 {
		t.Errorf("peak %.5f is not double off-peak %.5f", dear, cheap)
	}
	// A hit is far cheaper than a miss, which is the whole reason for the split.
	allMiss := deepseekUsage{PromptTokens: 1_000_000, PromptCacheMiss: 1_000_000}
	if allMiss.cost(DefaultDeepSeekModel, off) < u.cost(DefaultDeepSeekModel, off)*5 {
		t.Error("a fully uncached prompt should cost far more than a mostly cached one")
	}
	// An unknown model must over-estimate rather than under-charge.
	if u.cost("deepseek-who", off) < u.cost("deepseek-flash", off) {
		t.Error("an unknown model priced below the cheapest known one")
	}
	// A response that reports no split at all is treated as uncached.
	noSplit := deepseekUsage{PromptTokens: 1_000_000, CompletionTokens: 0}
	if noSplit.cost(DefaultDeepSeekModel, off) != allMiss.cost(DefaultDeepSeekModel, off) {
		t.Error("an unsplit usage report should be charged as a miss")
	}
}

func TestDeepSeekReportsAServiceFailureInWords(t *testing.T) {
	root := deepseekRoot(t)
	m, _ := deepseekManager(t, root, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	})
	s, err := m.Send(nil, twoSum, "q")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, stop := s.Subscribe()
	defer stop()
	events := collect(t, ch, KindError)
	last := events[len(events)-1]
	if !strings.Contains(last.Text, "429") && !strings.Contains(last.Text, "rate limited") {
		t.Errorf("the failure was not explained: %q", last.Text)
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
	s, err := first.Send(scope, twoSum, "my first question")
	if err != nil {
		t.Fatal(err)
	}
	_, ch, stop := s.Subscribe()
	collect(t, ch, KindDone)
	stop()
	first.Close()

	second, _ := deepseekManager(t, root, handler)
	s2, err := second.Send(scope, twoSum, "my second question")
	if err != nil {
		t.Fatal(err)
	}
	_, ch2, stop2 := s2.Subscribe()
	defer stop2()
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
