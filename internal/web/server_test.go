package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/progress"
)

// testSite builds a small site on a temporary project root.
func testSite(t *testing.T) (*Server, string, *progress.Store) {
	t.Helper()
	root := t.TempDir()

	problems := []catalog.Problem{
		{ID: 217, Slug: "contains-duplicate", Title: "Contains Duplicate", Difficulty: "Easy",
			Pattern: "Arrays & Hashing", PatternSlug: "arrays-hashing", Order: 0, Dir: "0217-contains-duplicate",
			Blind75: true, LeetCodeURL: "https://leetcode.com/problems/contains-duplicate/", VideoID: "vid"},
		{ID: 1, Slug: "two-sum", Title: "Two Sum", Difficulty: "Easy",
			Pattern: "Arrays & Hashing", PatternSlug: "arrays-hashing", Order: 1, Dir: "0001-two-sum",
			LeetCodeURL: "https://leetcode.com/problems/two-sum/"},
		{ID: 125, Slug: "valid-palindrome", Title: "Valid Palindrome", Difficulty: "Medium",
			Pattern: "Two Pointers", PatternSlug: "two-pointers", PatternIdx: 1, Order: 2, Dir: "0125-valid-palindrome",
			LeetCodeURL: "https://leetcode.com/problems/valid-palindrome/"},
	}
	cat := catalog.New(problems)

	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("content/problems/0001-two-sum/problem.md",
		"---\ntitle: Two Sum\n---\n\n# 1. Two Sum\n\nFind the two indices.\n\n```go\nx := 1\n```\n")
	write("content/problems/0001-two-sum/hints.md",
		"Read the constraints.\n\n## Hint 1 — pattern\n\nA hash map.\n\n## Hint 2 — invariant\n\nOne pass.\n")
	write("content/problems/0001-two-sum/notes.md", "My approach was brute force first.\n")
	write("content/patterns/arrays-hashing.md", "---\ntitle: Arrays & Hashing\n---\n\nTrade memory for passes.\n")

	prog, err := progress.Load(filepath.Join(root, "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(root, cat, content.NewStore(root, content.NewRenderer()), prog)
	if err != nil {
		t.Fatal(err)
	}
	srv.now = func() time.Time { return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) }
	return srv, root, prog
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestEveryPageRenders(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()

	for _, path := range []string{"/", "/problems/", "/problems/two-sum", "/patterns/", "/patterns/arrays-hashing", "/review/", "/highlight.css", "/static/app.css"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", path)
		}
	}
}

func TestUnknownSlugsAre404(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	for _, path := range []string{"/problems/nope", "/patterns/nope"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestPathsWithoutATrailingSlashRedirect(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	for _, path := range []string{"/problems", "/patterns", "/review"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s = %d, want 301", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != path+"/" {
			t.Errorf("GET %s redirected to %q", path, got)
		}
	}
}

func TestProblemPageShowsStatementHintsAndNotes(t *testing.T) {
	srv, _, _ := testSite(t)
	body := get(t, srv.Handler(), "/problems/two-sum").Body.String()

	for _, want := range []string{
		"Find the two indices", // the statement
		"Read the constraints", // the hint preamble, shown ungated
		"Hint 1 — pattern",     // the ladder
		"Hint 2 — invariant",
		"My approach was brute force", // your notes
		`href="/patterns/arrays-hashing"`,
		"https://leetcode.com/problems/two-sum/",
		"solutions/0001-two-sum/solution.go",
		`data-lang="go"`, // the code block was highlighted
	} {
		if !strings.Contains(body, want) {
			t.Errorf("problem page is missing %q", want)
		}
	}
	// The page prints its own title, so the statement's own H1 must not repeat it.
	if n := strings.Count(body, "Two Sum</h1>"); n != 1 {
		t.Errorf("title appears in %d H1 headings, want exactly 1", n)
	}
	// Each hint must sit behind its own reveal rather than being shown outright.
	if n := strings.Count(body, "<details class=\"hint\">"); n != 2 {
		t.Errorf("got %d hint reveals, want 2", n)
	}
	// The prev/next pager should link the neighbours in curriculum order.
	if !strings.Contains(body, `href="/problems/contains-duplicate"`) {
		t.Error("pager does not link the previous problem")
	}
	if !strings.Contains(body, `href="/problems/valid-palindrome"`) {
		t.Error("pager does not link the next problem")
	}
}

func TestProblemPageExplainsWhatIsMissing(t *testing.T) {
	srv, _, _ := testSite(t)
	// contains-duplicate has no files written at all.
	body := get(t, srv.Handler(), "/problems/contains-duplicate").Body.String()
	for _, want := range []string{"No statement on disk yet", "No hints written yet", "Nothing here yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty state missing: %q", want)
		}
	}
}

func TestProblemListRendersEveryProblemWithFilterData(t *testing.T) {
	srv, _, _ := testSite(t)
	body := get(t, srv.Handler(), "/problems/").Body.String()
	if n := strings.Count(body, `class="row"`); n != 3 {
		t.Errorf("got %d rows, want 3", n)
	}
	for _, want := range []string{`data-difficulty="Easy"`, `data-difficulty="Medium"`, `data-status="new"`, `data-blind="true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("filter attribute missing: %q", want)
		}
	}
	if n := strings.Count(body, "data-group-section"); n != 2 {
		t.Errorf("got %d pattern sections, want 2", n)
	}
}

func TestStatusPostUpdatesProgressAndRedirects(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()

	form := url.Values{"status": {"solved"}, "from": {"/problems/two-sum"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/status", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/problems/two-sum" {
		t.Errorf("redirected to %q", got)
	}
	// The change must be on disk, not just in memory.
	reloaded, err := progress.Load(filepath.Join(root, "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get("two-sum").Status; got != progress.Solved {
		t.Errorf("stored status = %q, want solved", got)
	}
	// And it must show up on the page.
	if body := get(t, h, "/problems/").Body.String(); !strings.Contains(body, `data-status="solved"`) {
		t.Error("the list still shows the old status")
	}
}

func TestRatePostSchedulesAReview(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	form := url.Values{"confidence": {"4"}, "from": {"/review/"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/rate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST rate = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/review/" {
		t.Errorf("rating from the review queue should return there, got %q", got)
	}
	if e := prog.Get("two-sum"); e.NextReview == nil || e.Status != progress.Solved {
		t.Errorf("rating did not schedule a review: %+v", e)
	}
}

func TestBadMutationsAreRejected(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	cases := []struct {
		path string
		form url.Values
		want int
	}{
		{"/problems/two-sum/status", url.Values{"status": {"brilliant"}}, http.StatusBadRequest},
		{"/problems/two-sum/rate", url.Values{"confidence": {"9"}}, http.StatusBadRequest},
		{"/problems/two-sum/rate", url.Values{"confidence": {"abc"}}, http.StatusBadRequest},
		{"/problems/nope/status", url.Values{"status": {"solved"}}, http.StatusNotFound},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("POST %s %v = %d, want %d", tc.path, tc.form, rec.Code, tc.want)
		}
	}
}

func TestRedirectBackRefusesAnOffsiteTarget(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	form := url.Values{"status": {"solved"}, "from": {"//evil.test/"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/status", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Location"); got != "/problems/two-sum" {
		t.Errorf("a protocol-relative target should be ignored, got %q", got)
	}
}

func TestDashboardSuggestsTheNextProblemInOrder(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	body := get(t, h, "/").Body.String()
	if !strings.Contains(body, "Contains Duplicate") {
		t.Error("dashboard should suggest the first problem in the roadmap")
	}
	// One cell per problem makes up the roadmap meter.
	if n := strings.Count(body, `class="cell `); n != 3 {
		t.Errorf("got %d roadmap cells, want 3", n)
	}

	if _, err := prog.SetStatus("contains-duplicate", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}
	body = get(t, h, "/").Body.String()
	if !strings.Contains(body, "Two Sum") {
		t.Error("after solving the first, the dashboard should move on")
	}
	if !strings.Contains(body, `class="cell is-solved"`) {
		t.Error("the solved problem's cell should be filled")
	}
}

func TestReviewQueueListsWhatIsDue(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	if body := get(t, h, "/review/").Body.String(); !strings.Contains(body, "Nothing due today") {
		t.Error("an empty queue should say so")
	}

	if _, err := prog.SetStatus("two-sum", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}
	// Move the clock past the one-day first interval.
	srv.now = func() time.Time { return time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC) }

	body := get(t, h, "/review/").Body.String()
	if !strings.Contains(body, "Two Sum") {
		t.Error("the due problem is not listed")
	}
	if !strings.Contains(body, `name="confidence"`) {
		t.Error("the queue should let you rate a problem in place")
	}
}
