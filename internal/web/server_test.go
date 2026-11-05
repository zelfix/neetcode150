package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/progress"
	"neetcode150/internal/scaffold"
	"neetcode150/internal/tutor"
	"os/exec"

	"neetcode150/internal/studydb"
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
	write("content/problems/0001-two-sum/signature.txt", "func twoSum(nums []int, target int) []int {\n    \n}\n")

	prog, err := progress.Load(filepath.Join(root, "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(root, cat, content.NewStore(root, content.NewRenderer()), prog)
	if err != nil {
		t.Fatal(err)
	}
	srv.UseTutor(tutor.NewWithCommand(root, tutor.ModeTutor, nil))
	t.Cleanup(func() { srv.CloseTutor(); prog.Close() })
	srv.now = func() time.Time { return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) }
	return srv, root, prog
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// postForm submits a form the way a browser does from one of this site's own
// pages. Sec-Fetch-Site is what the mutation guard reads, so a request without
// it is testing the guard rather than the handler behind it.
func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	h.ServeHTTP(rec, req)
	return rec
}

func TestEveryPageRenders(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()

	for _, path := range []string{"/", "/problems/", "/problems/two-sum", "/patterns/", "/patterns/arrays-hashing", "/review/", "/coach/", "/highlight.css", "/static/app.css"} {
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
	if !strings.Contains(body, `href="/problems/contains-duplicate/solve"`) {
		t.Error("pager does not open the previous problem's workspace")
	}
	if !strings.Contains(body, `href="/problems/valid-palindrome/solve"`) {
		t.Error("pager does not open the next problem's workspace")
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

	rec := postForm(t, h, "/problems/two-sum/status",
		url.Values{"status": {"solved"}, "from": {"/problems/two-sum"}})

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

	rec := postForm(t, h, "/problems/two-sum/rate",
		url.Values{"confidence": {"4"}, "from": {"/review/"}})

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
		rec := postForm(t, h, tc.path, tc.form)
		if rec.Code != tc.want {
			t.Errorf("POST %s %v = %d, want %d", tc.path, tc.form, rec.Code, tc.want)
		}
	}
}

func TestRedirectBackRefusesAnOffsiteTarget(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	rec := postForm(t, h, "/problems/two-sum/status",
		url.Values{"status": {"solved"}, "from": {"//evil.test/"}})

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

// The site writes files and runs `go test`, so a mutation must be provably from
// one of its own pages. Binding to localhost is not that proof: any page in the
// same browser can POST here, and a form-encoded POST needs no preflight, so it
// arrives and takes effect even though the attacker cannot read the reply.
func TestMutationsRequireASameOriginRequest(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	form := url.Values{"status": {"solved"}}

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"a browser form post from our own page", map[string]string{
			"Sec-Fetch-Site": "same-origin",
		}, http.StatusSeeOther},
		{"no provenance headers at all, as from curl", nil, http.StatusForbidden},
		{"another site's page", map[string]string{
			"Sec-Fetch-Site": "cross-site",
		}, http.StatusForbidden},
		{"a sibling subdomain", map[string]string{
			"Sec-Fetch-Site": "same-site",
		}, http.StatusForbidden},
		// "none" means the user typed the URL, which no mutation ever is.
		{"user-initiated navigation", map[string]string{
			"Sec-Fetch-Site": "none",
		}, http.StatusForbidden},
		// Older clients send no Sec-Fetch-Site, so Origin is the fallback.
		{"matching Origin, no Sec-Fetch-Site", map[string]string{
			"Origin": "http://example.com",
		}, http.StatusSeeOther},
		{"mismatched Origin", map[string]string{
			"Origin": "http://evil.test",
		}, http.StatusForbidden},
		// A forged Sec-Fetch-Site would have to come from outside a browser, and
		// then the header is the attacker's own word; nothing more can be done
		// about that at this layer, but Origin must not rescue a bad one.
		{"cross-site beats a matching Origin", map[string]string{
			"Sec-Fetch-Site": "cross-site",
			"Origin":         "http://example.com",
		}, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/status",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			// httptest.NewRequest sets Host to example.com.
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// ---------- the solving workspace ----------

// postJSON calls the editor's API the way the page does: JSON, same-origin.
func postJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestWorkspaceOffersToScaffoldWhenThereIsNoSolution(t *testing.T) {
	srv, _, _ := testSite(t)
	rec := get(t, srv.Handler(), "/problems/two-sum/solve")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET solve = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data-start") {
		t.Error("no start button on a problem with no solution file")
	}
	// A disabled, empty editor says nothing. The record takes its place until
	// there is something to type into it.
	if strings.Contains(body, "data-editor") {
		t.Error("the editor is live before the attempt started")
	}
	if !strings.Contains(body, `class="record"`) {
		t.Error("nothing fills the work column before Start")
	}
	// The statement belongs on the page too, or there is nothing to solve from.
	if !strings.Contains(body, "Find the two indices") {
		t.Error("the statement is missing from the workspace")
	}
}

func TestCreateScaffoldsMarksInProgressAndStartsTheClock(t *testing.T) {
	srv, root, prog := testSite(t)
	h := srv.Handler()

	rec := postJSON(t, h, "/problems/two-sum/create", struct{}{})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST create = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[struct {
		Code     string   `json:"code"`
		TestCode string   `json:"testCode"`
		Created  []string `json:"created"`
	}](t, rec)

	if !strings.Contains(got.Code, "func twoSum") {
		t.Errorf("scaffold does not carry the signature: %q", got.Code)
	}
	// notes.md is already in the fixture, so only two files are new — which is
	// the point: an existing file is never overwritten, and notes someone has
	// already written must survive a scaffold.
	want := []string{"solutions/0001-two-sum/solution.go", "solutions/0001-two-sum/solution_test.go"}
	if !slices.Equal(got.Created, want) {
		t.Errorf("created %v, want %v", got.Created, want)
	}
	if notes, _ := os.ReadFile(filepath.Join(root, "content/problems/0001-two-sum/notes.md")); !strings.Contains(string(notes), "brute force") {
		t.Error("the existing notes were overwritten by the scaffold")
	}
	for _, rel := range []string{"solutions/0001-two-sum/solution.go", "solutions/0001-two-sum/solution_test.go"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s not on disk: %v", rel, err)
		}
	}
	e := prog.Get("two-sum")
	if e.Status != progress.Attempted {
		t.Errorf("status = %q, want attempted", e.Status)
	}
	if _, running := e.Elapsed(srv.now()); !running {
		t.Error("the clock was not started")
	}
	// And the editor now appears instead of the create button.
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); !strings.Contains(body, "data-editor") {
		t.Error("the workspace still shows the empty state")
	}
}

func TestSavePreservesDraftAndPersists(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	// A draft must keep the exact spacing the user is still editing.
	ugly := "package twosum\nfunc twoSum(nums []int,target int) []int {\nreturn nil\n}\n"
	rec := postJSON(t, h, "/problems/two-sum/solution", map[string]string{"code": ugly})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST solution = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[struct {
		Code        string `json:"code"`
		FormatError string `json:"formatError"`
	}](t, rec)
	if got.FormatError != "" {
		t.Errorf("valid source reported a format error: %s", got.FormatError)
	}
	if got.Code != ugly {
		t.Errorf("draft was changed: %q", got.Code)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "solutions", "0001-two-sum", "solution.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != got.Code {
		t.Error("what was stored differs from what was returned to the editor")
	}
}

// A save in the middle of typing is the normal case. Refusing it would lose
// keystrokes to protect a file that is allowed to be mid-edit.
func TestSaveKeepsUnparseableDraftWithoutDiagnostics(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	broken := "package twosum\nfunc twoSum(nums []int, target int) []int {\n\treturn nil\n"
	rec := postJSON(t, h, "/problems/two-sum/solution", map[string]string{"code": broken})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST solution = %d", rec.Code)
	}
	got := decode[struct {
		Code        string `json:"code"`
		FormatError string `json:"formatError"`
	}](t, rec)
	if got.FormatError != "" {
		t.Error("an unfinished draft should not report syntax diagnostics")
	}
	if got.Code != broken {
		t.Error("unparseable source was altered instead of being kept verbatim")
	}
	onDisk, _ := os.ReadFile(filepath.Join(root, "solutions", "0001-two-sum", "solution.go"))
	if string(onDisk) != broken {
		t.Errorf("stored %q, want it kept byte for byte", string(onDisk))
	}
}

func TestSaveRefusesToTruncate(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	path := filepath.Join(root, "solutions", "0001-two-sum", "solution.go")
	before, _ := os.ReadFile(path)

	for _, body := range []any{struct{}{}, map[string]string{"code": ""}, map[string]string{"code": "  \n\t"}} {
		rec := postJSON(t, h, "/problems/two-sum/solution", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %v = %d, want 400", body, rec.Code)
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Error("an empty request changed the file")
	}
}

func TestSaveNeedsAFileToSaveInto(t *testing.T) {
	srv, _, _ := testSite(t)
	rec := postJSON(t, srv.Handler(), "/problems/two-sum/solution",
		map[string]string{"code": "package twosum\n"})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("saving with no scaffold = %d, want 500", rec.Code)
	}
}

// Running must save first, or the result describes code that is no longer on
// screen. The fake runner reads the file to prove the order.
func TestRunTestsSavesBeforeItRuns(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	var sawRoot, sawPkg, sawOnDisk string
	srv.runTests = func(_ context.Context, root, pkgDir string) (testRun, error) {
		sawRoot, sawPkg = root, pkgDir
		b, _ := os.ReadFile(filepath.Join(root, pkgDir, "solution.go"))
		sawOnDisk = string(b)
		return testRun{Output: "ok  \tneetcode150/solutions/0001-two-sum\t0.21s\n", OK: true, Ran: true, Passed: 1}, nil
	}

	code := "package twosum\n\nfunc twoSum(nums []int, target int) []int {\n\treturn []int{0, 1}\n}\n"
	rec := postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": code})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST test = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[struct {
		Output   string `json:"output"`
		Passed   bool   `json:"passed"`
		RunError string `json:"runError"`
	}](t, rec)

	if !got.Passed || got.RunError != "" {
		t.Errorf("got %+v, want a pass", got)
	}
	if !strings.Contains(got.Output, "0001-two-sum") {
		t.Errorf("output not passed through: %q", got.Output)
	}
	if sawOnDisk != code {
		t.Errorf("the runner saw %q on disk, want the code just posted", sawOnDisk)
	}
	// The package path must come from the catalog, never from the request.
	if sawRoot != root || sawPkg != filepath.Join("solutions", "0001-two-sum") {
		t.Errorf("runner called with root %q, pkg %q", sawRoot, sawPkg)
	}
}

func TestRunTestsSurfacesAFailureAndARunnerError(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	code := "package twosum\n\nfunc twoSum(nums []int, target int) []int {\n\treturn nil\n}\n"

	srv.runTests = func(context.Context, string, string) (testRun, error) {
		return testRun{Output: "--- FAIL: TestTwoSum\n", Ran: true, Failed: 1,
			Cases: []studydb.FailedCase{{Kind: "edge", Name: "empty input"}}}, nil
	}
	got := decode[struct {
		Output   string `json:"output"`
		Passed   bool   `json:"passed"`
		RunError string `json:"runError"`
	}](t, postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": code}))
	if got.Passed || got.RunError != "" || !strings.Contains(got.Output, "FAIL") {
		t.Errorf("a failing run came back as %+v", got)
	}

	// A run that never happened must not read as a pass with no output.
	srv.runTests = func(context.Context, string, string) (testRun, error) {
		return testRun{}, errors.New("could not run go test: exec: \"go\": not found")
	}
	got = decode[struct {
		Output   string `json:"output"`
		Passed   bool   `json:"passed"`
		RunError string `json:"runError"`
	}](t, postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": code}))
	if got.Passed || !strings.Contains(got.RunError, "not found") {
		t.Errorf("a runner error came back as %+v", got)
	}
}

// The file-writing and code-running endpoints carry the stricter guard, so a
// form-encoded post cannot reach them even from this origin.
func TestWorkspaceEndpointsRequireJSON(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()

	for _, path := range []string{"create", "solution", "test"} {
		t.Run(path+" rejects a form body", func(t *testing.T) {
			rec := postForm(t, h, "/problems/two-sum/"+path, url.Values{"code": {"package x"}})
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("= %d, want 415", rec.Code)
			}
		})
		t.Run(path+" rejects a cross-origin request", func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/"+path,
				strings.NewReader(`{"code":"package x"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("= %d, want 403", rec.Code)
			}
		})
	}
}

// ---------- getting into the workspace ----------

func TestWorkActionNamesWhatTheButtonWillDo(t *testing.T) {
	due := func(v problemView) problemView { v.IsDue = true; return v }
	with := func(st progress.Status) problemView {
		return problemView{Entry: progress.Entry{Status: st}}
	}
	started := time.Now()
	active := with(progress.Attempted)
	active.Entry.AttemptStartedAt = &started

	cases := []struct {
		name        string
		view        problemView
		hasSolution bool
		want        string
	}{
		{"untouched", with(progress.New), false, "Start"},
		{"scaffolded and in progress", active, true, "Continue"},
		{"cancelled attempt", with(progress.Attempted), true, "Start"},
		{"solved", with(progress.Solved), true, "Open workspace"},
		{"shaky", with(progress.Shaky), true, "Open workspace"},
		// The solved workspace opens inactive and offers Start before exposing
		// the old source in its editor.
		{"due for review", due(with(progress.Solved)), true, "Open workspace"},
		// Marked solved but the file is gone — believe the disk.
		{"solved with no file", with(progress.Solved), false, "Start"},
	}
	for _, tc := range cases {
		if got := workAction(tc.view, tc.hasSolution); got != tc.want {
			t.Errorf("%s: workAction = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProblemPageLeadsIntoTheWorkspace(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	body := get(t, h, "/problems/two-sum").Body.String()
	if !strings.Contains(body, `href="/problems/two-sum/solve"`) {
		t.Error("no link into the workspace")
	}
	if !strings.Contains(body, ">Start <") {
		t.Error("an untouched problem should offer Start")
	}

	// Once there is a scaffold, the verb changes with the state.
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if body := get(t, h, "/problems/two-sum").Body.String(); !strings.Contains(body, ">Continue <") {
		t.Error("a problem in progress should offer Continue")
	}

	if _, err := prog.Rate("two-sum", 4, srv.now()); err != nil {
		t.Fatal(err)
	}
	if body := get(t, h, "/problems/two-sum").Body.String(); !strings.Contains(body, ">Open workspace <") {
		t.Error("a solved problem should offer the workspace, not Start")
	}
}

func TestDashboardCardStartsTheWork(t *testing.T) {
	srv, _, _ := testSite(t)
	body := get(t, srv.Handler(), "/").Body.String()
	if !strings.Contains(body, `class="go-solve" href="/problems/contains-duplicate/solve"`) {
		t.Error("the pick-up card does not lead into the workspace")
	}
	if !strings.Contains(body, "Read it first") {
		t.Error("the card should still offer the statement")
	}
}

// Opening a review in the browser shows the old solution, which the review flow
// says not to do. The page has to say so rather than quietly break the protocol.
func TestWorkspaceWarnsWhenTheProblemIsDueForReview(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	// Not due yet: no warning.
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); strings.Contains(body, "solve-warn") {
		t.Error("warned about a review on a problem that is not due")
	}

	// Rate it, then look from far enough in the future that it has come back.
	if _, err := prog.Rate("two-sum", 4, srv.now()); err != nil {
		t.Fatal(err)
	}
	srv.now = func() time.Time { return time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC) }

	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if !strings.Contains(body, "solve-warn") {
		t.Fatal("no warning on a problem due for review")
	}
	// Starting archives the old attempt and begins a cold solve.
	if !strings.Contains(body, "data-start") {
		t.Error("the due problem offers no way to begin a fresh attempt")
	}
	// And it must say the old attempt is still readable, or archiving looks
	// like throwing work away.
	if !strings.Contains(body, "Your previous work stays in History.") {
		t.Error("the warning does not say the archived attempt can be read back")
	}
}

// ---------- re-solving from the browser ----------

// solvedFixture scaffolds a problem, writes a solution into it and rates it, so
// it looks like something finished earlier.
func solvedFixture(t *testing.T, srv *Server, h http.Handler, prog *progress.Store) string {
	t.Helper()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	mine := "package twosum\n\nfunc twoSum(nums []int, target int) []int {\n\treturn []int{0, 1}\n}\n"
	if rec := postJSON(t, h, "/problems/two-sum/solution", map[string]string{"code": mine}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := prog.Rate("two-sum", 4, srv.now()); err != nil {
		t.Fatal(err)
	}
	return mine
}

func TestResolveArchivesTheOldAttemptAndBlanksTheFile(t *testing.T) {
	srv, root, prog := testSite(t)
	h := srv.Handler()
	mine := solvedFixture(t, srv, h, prog)

	rec := postJSON(t, h, "/problems/two-sum/resolve", struct{}{})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST resolve = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[struct {
		Code     string `json:"code"`
		Archive  string `json:"archive"`
		Archived int    `json:"archived"`
	}](t, rec)

	// The file handed back must be a blank scaffold, not the old answer.
	if strings.Contains(got.Code, "return []int{0, 1}") {
		t.Error("the editor was handed the previous solution back")
	}
	if !strings.Contains(got.Code, "panic(\"TODO") {
		t.Errorf("not a blank scaffold: %q", got.Code)
	}
	if got.Archived != 1 {
		t.Errorf("archived = %d, want 1", got.Archived)
	}

	// The snapshot is stored byte for byte in SQLite.
	archived, err := scaffold.ReadAttempt(prog.Scope(), got.Archive)
	if err != nil {
		t.Fatalf("reading %s: %v", got.Archive, err)
	}
	if archived.Code != mine {
		t.Errorf("archive holds %q, want the previous solution verbatim", archived.Code)
	}
	if !strings.HasPrefix(got.Archive, "attempt:") {
		t.Errorf("expected a stable attempt reference, got %q", got.Archive)
	}

	// The test file is untouched: it holds the cases, not the approach.
	if _, err := os.Stat(filepath.Join(root, "solutions/0001-two-sum/solution_test.go")); err != nil {
		t.Errorf("the test file went missing: %v", err)
	}

	e := prog.Get("two-sum")
	if e.Status != progress.Attempted {
		t.Errorf("status = %q, want attempted", e.Status)
	}
	if _, running := e.Elapsed(srv.now()); !running {
		t.Error("the clock was not restarted for the re-solve")
	}
}

// Archiving a solution that is still being written would put aside a
// half-finished attempt and hand back an empty file.
func TestResolveRefusesWorkInProgress(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	before, _ := os.ReadFile(filepath.Join(root, "solutions/0001-two-sum/solution.go"))

	if rec := postJSON(t, h, "/problems/two-sum/resolve", struct{}{}); rec.Code != http.StatusConflict {
		t.Errorf("POST resolve on work in progress = %d, want 409", rec.Code)
	}
	after, _ := os.ReadFile(filepath.Join(root, "solutions/0001-two-sum/solution.go"))
	if string(after) != string(before) {
		t.Error("a refused re-solve still changed the file")
	}
	if n := srv.countArchived(catalog.Problem{Dir: "0001-two-sum"}); n != 0 {
		t.Errorf("a refused re-solve archived %d files", n)
	}
}

func TestResolveNeedsSomethingToArchive(t *testing.T) {
	srv, _, _ := testSite(t)
	if rec := postJSON(t, srv.Handler(), "/problems/two-sum/resolve", struct{}{}); rec.Code != http.StatusConflict {
		t.Errorf("POST resolve with no solution = %d, want 409", rec.Code)
	}
}

// Two re-solves on one day must not overwrite the first archive.
func TestResolveTwiceKeepsBothAttempts(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	solvedFixture(t, srv, h, prog)

	first := decode[struct{ Archive string }](t, postJSON(t, h, "/problems/two-sum/resolve", struct{}{}))

	second := "package twosum\n\nfunc twoSum(nums []int, target int) []int {\n\treturn nil\n}\n"
	if rec := postJSON(t, h, "/problems/two-sum/solution", map[string]string{"code": second}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := prog.Rate("two-sum", 4, srv.now()); err != nil {
		t.Fatal(err)
	}
	got := decode[struct {
		Archive  string `json:"archive"`
		Archived int    `json:"archived"`
	}](t, postJSON(t, h, "/problems/two-sum/resolve", struct{}{}))

	if got.Archive == first.Archive {
		t.Errorf("the second re-solve reused %q and overwrote the first attempt", got.Archive)
	}
	if got.Archived != 2 {
		t.Errorf("archived = %d, want 2", got.Archived)
	}
}

func TestWorkspaceOffersStartOnlyWhenNoAttemptIsActive(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	// No solution yet: show the inactive workspace and its Start action.
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); strings.Contains(body, "data-resolve") {
		t.Error("offered a resolve action with no earlier solution")
	}

	// In progress: re-solving would throw away unfinished work.
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); strings.Contains(body, "data-resolve") {
		t.Error("offered a re-solve on work in progress")
	}

	// Solved: start a fresh attempt and keep the old source behind the reveal.
	if _, err := prog.Rate("two-sum", 4, srv.now()); err != nil {
		t.Fatal(err)
	}
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); !strings.Contains(body, "data-start") {
		t.Error("a solved problem should offer a fresh attempt")
	}
}

// ---------- the tutor panel ----------

// fakeTutor points the server at a stand-in for claude that speaks the same
// stream-json protocol. No test here calls the API.
func fakeTutor(t *testing.T, srv *Server, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := tutor.NewWithCommand(t.TempDir(), tutor.ModeTutor,
		func(ctx context.Context, p catalog.Problem, id string) *exec.Cmd {
			return exec.CommandContext(ctx, "/bin/sh", path, id)
		})
	srv.UseTutor(m)
	t.Cleanup(srv.CloseTutor)
}

const fakeTutorScript = `
echo '{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5","permissionMode":"acceptEdits","tools":["Read"]}'
while IFS= read -r line; do
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Try a hash set."}}}'
  echo '{"type":"result","subtype":"success","total_cost_usd":0.002}'
done
`

// readSSE reads the stream until it has seen want events or the deadline passes.
// streamRecorder is a ResponseWriter two goroutines may hold at once: the
// handler streams into it while the test reads what has arrived so far. The
// recorder httptest hands out is not safe for that, and under -race the poll
// below is a write and a read of the same buffer.
type streamRecorder struct {
	mu     sync.Mutex
	body   bytes.Buffer
	header http.Header
	code   int
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: http.Header{}, code: http.StatusOK}
}

func (r *streamRecorder) Header() http.Header { return r.header }

func (r *streamRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.code = code
}

func (r *streamRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(b)
}

// The SSE handler needs a Flusher or it refuses to stream at all.
func (r *streamRecorder) Flush() {}

func (r *streamRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func readSSE(t *testing.T, h http.Handler, path string, want int) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := newStreamRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// The handler streams until the request context ends, so poll what it has
	// written rather than waiting for it to return.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if n := strings.Count(rec.String(), "data: "); n >= want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	var out []map[string]any
	for _, line := range strings.Split(rec.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("unparseable SSE payload %q: %v", data, err)
		}
		out = append(out, m)
	}
	return out
}

func TestTutorStreamsTheConversation(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	rec := postJSON(t, h, "/problems/two-sum/tutor", map[string]string{"text": "which pattern?"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST tutor = %d: %s", rec.Code, rec.Body.String())
	}

	// ready, the user's own turn, the reply, and the result.
	events := readSSE(t, h, "/problems/two-sum/tutor/events", 4)
	kinds := map[string]bool{}
	var text, user string
	for _, e := range events {
		kind, _ := e["kind"].(string)
		kinds[kind] = true
		switch kind {
		case "text":
			text += e["text"].(string)
		case "user":
			user, _ = e["text"].(string)
		}
	}
	for _, want := range []string{"ready", "user", "text", "done"} {
		if !kinds[want] {
			t.Errorf("no %q event; got %v", want, kinds)
		}
	}
	if text != "Try a hash set." {
		t.Errorf("reply text = %q", text)
	}
	// The user's own turn has to be in the stream, or a reload shows answers
	// with no questions.
	if user != "which pattern?" {
		t.Errorf("user turn = %q", user)
	}
}

// A second browser — or the same one after a reload — must see the history.
func TestTutorStreamReplaysForALaterReader(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	if rec := postJSON(t, h, "/problems/two-sum/tutor", map[string]string{"text": "asked earlier"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	first := readSSE(t, h, "/problems/two-sum/tutor/events", 4)
	if len(first) < 4 {
		t.Fatalf("only %d events on the first read", len(first))
	}
	// A fresh connection replays from the beginning.
	second := readSSE(t, h, "/problems/two-sum/tutor/events", 4)
	if len(second) < len(first) {
		t.Errorf("replay gave %d events, first read saw %d", len(second), len(first))
	}
	var question string
	for _, event := range second {
		if event["kind"] == "user" {
			question, _ = event["text"].(string)
			break
		}
	}
	if question != "asked earlier" {
		t.Errorf("replay lost the question: %+v", second)
	}
}

func TestTutorEventsWithoutASessionDoesNotInviteARetryLoop(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/problems/two-sum/tutor/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	srv.Handler().ServeHTTP(rec, req)

	// 404 tells the script to stop; a 200 with an empty stream would have it
	// reconnect for ever.
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET events with no session = %d, want 404", rec.Code)
	}
}

func TestTutorResetEndsTheSession(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	if rec := postJSON(t, h, "/problems/two-sum/tutor", map[string]string{"text": "hi"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if rec := postJSON(t, h, "/problems/two-sum/tutor/reset", struct{}{}); rec.Code != http.StatusOK {
		t.Fatalf("POST reset = %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/problems/two-sum/tutor/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("the session survived a reset: events = %d", rec.Code)
	}
}

func TestTutorRejectsAnEmptyQuestion(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	for _, body := range []any{struct{}{}, map[string]string{"text": "   "}} {
		if rec := postJSON(t, h, "/problems/two-sum/tutor", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %v = %d, want 400", body, rec.Code)
		}
	}
}

// With no claude on PATH the panel says so and the rest of the site is fine.
func TestWorkspaceWithoutATutorStillWorks(t *testing.T) {
	srv, _, _ := testSite(t)
	srv.UseTutor(tutor.New(t.TempDir(), tutor.ModeFull, 0))
	// Force the unavailable path regardless of whether this machine has claude.
	srv.UseTutor(tutor.NewWithCommand(t.TempDir(), tutor.ModeFull, nil))
	h := srv.Handler()

	if rec := get(t, h, "/problems/two-sum/solve"); rec.Code != http.StatusOK {
		t.Fatalf("the workspace broke: %d", rec.Code)
	}
}

func TestTutorEndpointsAreGuarded(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	for _, path := range []string{"tutor", "tutor/reset", "tutor/interrupt"} {
		t.Run(path+" refuses a form body", func(t *testing.T) {
			if rec := postForm(t, h, "/problems/two-sum/"+path, url.Values{"text": {"hi"}}); rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("= %d, want 415", rec.Code)
			}
		})
		t.Run(path+" refuses another origin", func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/"+path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("= %d, want 403", rec.Code)
			}
		})
	}

	// The event stream carries the conversation, which quotes the user's code.
	t.Run("the stream refuses another origin", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/problems/two-sum/tutor/events", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("= %d, want 403", rec.Code)
		}
	})
	t.Run("the stream refuses a request with no provenance", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/problems/two-sum/tutor/events", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("= %d, want 403", rec.Code)
		}
	})
}

// ---------- the lesson layout ----------

func TestWorkspaceIsOneLessonWithTwoActions(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	if _, err := srv.progress.StartAttempt("two-sum", srv.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.progress.ResetTimer("two-sum", srv.now()); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	body := get(t, h, "/problems/two-sum/solve").Body.String()

	// The page owns the viewport, so the chat input and the buttons stay put.
	if !strings.Contains(body, `class="app-full"`) {
		t.Error("the workspace does not claim the full viewport")
	}
	// The statement is the conversation's opening turn, not a column of its own.
	for _, want := range []string{`class="chat"`, "data-tutor-log", "data-statement-message", "Find the two indices"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q — the statement should open the conversation", want)
		}
	}
	if i, j := strings.Index(body, "data-statement-message"), strings.Index(body, "Find the two indices"); i > j {
		t.Error("the statement is rendered outside the conversation column")
	}

	// Two moves, and saving is not one of them.
	if strings.Contains(body, "data-save") {
		t.Error("the Save button is still there; saving is automatic now")
	}
	for _, want := range []string{"data-run", "data-submit"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// The tutor rates after Submit, so a hand-set rating here would compete
	// with it. It stays on the reading view and in `nc rate`.
	if strings.Contains(body, `action="/problems/two-sum/rate"`) {
		t.Error("the manual rating form is still on the workspace")
	}
	// Hints arrive one rung at a time through the tutor, not as reveals that
	// let you skip to the third.
	if strings.Contains(body, "Hint 1 — pattern") {
		t.Error("the hint ladder is still revealable in one click")
	}
}

func TestStatementIsTheFirstChatMessageBeforeAndDuringAnAttempt(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	check := func(body string) {
		t.Helper()
		if strings.Contains(body, "data-brief-toggle") || strings.Contains(body, "data-brief=") {
			t.Error("statement should not be collapsible")
		}
		if strings.Count(body, "data-statement-message") != 1 {
			t.Error("statement must appear exactly once")
		}
		if !strings.Contains(body, "Find the two indices") {
			t.Error("statement missing")
		}
		log := strings.Index(body, "data-tutor-log")
		statement := strings.Index(body, "data-statement-message")
		controls := strings.Index(body, "data-tutor-form")
		if log < 0 || statement < log || (controls >= 0 && statement > controls) {
			t.Error("statement is outside the chat log")
		}
	}
	check(get(t, h, "/problems/two-sum/solve").Body.String())
	if rec := postJSON(t, h, "/problems/two-sum/start", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	check(get(t, h, "/problems/two-sum/solve").Body.String())
}

// The tutor opens the lesson so the first thing on screen is a question, not an
// empty panel. The prompt that produces it is the application's, so it must not
// appear as something the user said.
func TestStartHasTheTutorSpeakFirst(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	if rec := postJSON(t, h, "/problems/two-sum/start", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	// openLesson runs detached so Start can answer at once.
	deadline := time.Now().Add(8 * time.Second)
	for srv.tutor.Get("two-sum") == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if srv.tutor.Get("two-sum") == nil {
		t.Fatal("Start did not open a tutor session")
	}

	events := readSSE(t, h, "/problems/two-sum/tutor/events", 3)
	var spoke bool
	for _, e := range events {
		switch e["kind"] {
		case "text":
			spoke = true
		case "user":
			if text, _ := e["text"].(string); text != "" {
				t.Errorf("the opening prompt leaked into the transcript as a user turn: %q", text)
			}
		}
	}
	if !spoke {
		t.Fatalf("the tutor said nothing after Start; events: %v", events)
	}
}

func TestRecordFillsTheWorkColumnBeforeStart(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	// Nothing recorded: say so, and say what a rating will do.
	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if !strings.Contains(body, "First sitting") {
		t.Error("an untouched problem does not say it is the first sitting")
	}
	if !strings.Contains(body, "Nothing recorded yet") {
		t.Error("the empty record does not explain the rating scale")
	}
	// Where the problem sits in its pattern is on the page; the pattern guide
	// is not, because naming the pattern is rung one of the ladder.
	if !strings.Contains(body, "2 of 2 in Arrays &amp; Hashing") {
		t.Error("the record does not place the problem within its pattern")
	}
	if strings.Contains(body, "/patterns/arrays-hashing") {
		t.Error("the workspace links the pattern guide, which hands over rung 1")
	}

	// A rated problem shows the sitting, the rung and the generosity flag.
	if _, err := prog.AddMinutes("two-sum", 26, srv.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.RateHinted("two-sum", 4, 3, srv.now()); err != nil {
		t.Fatal(err)
	}

	body = get(t, h, "/problems/two-sum/solve").Body.String()
	for _, want := range []string{
		"record-table",
		"26", // the sitting, not a cumulative total
		"Last sitting needed hint rung 3.",
		"rating looks generous next to that rung",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the record is missing %q", want)
		}
	}

	// Once the attempt begins the editor takes the box back.
	if rec := postJSON(t, h, "/problems/two-sum/start", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	body = get(t, h, "/problems/two-sum/solve").Body.String()
	if strings.Contains(body, `class="record"`) {
		t.Error("the record is still showing during an attempt")
	}
	if !strings.Contains(body, "data-editor") {
		t.Error("the editor did not replace the record")
	}
}

func TestWorkspaceSupportsChatAfterCompletingAnAttempt(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()
	checkIdle := func(body string, canChat bool) {
		t.Helper()
		left, right, ok := strings.Cut(body, `<section class="workspace">`)
		if !ok {
			t.Fatal("missing workspace")
		}
		if strings.Contains(body, "Reading view") {
			t.Error("Reading view link should be removed")
		}
		if !strings.Contains(right, "My approach was brute force first.") || strings.Contains(left, "My approach was brute force first.") {
			t.Error("notes should appear only in the idle right pane")
		}
		if strings.Contains(left, "data-start") {
			t.Error("Start belongs in the right panel")
		}
		if strings.Contains(left, "data-tutor-input") != canChat {
			t.Error("unexpected post-attempt chat availability")
		}
		if canChat && !strings.Contains(left, "Continue discussing your solution") {
			t.Error("missing follow-up invitation")
		}
		for _, unwanted := range []string{"data-run", "data-submit", "data-editor"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("idle workspace contains %s", unwanted)
			}
		}
		if strings.Count(body, "data-start") != 1 || !strings.Contains(body, "data-statement-message") {
			t.Error("idle page should have one Start action and an open statement")
		}
		if strings.Index(body, "data-start") < strings.Index(body, `class="record"`) {
			t.Error("Start should be at the top of the workspace record")
		}
	}
	checkIdle(get(t, h, "/problems/two-sum/solve").Body.String(), false)
	if rec := postJSON(t, h, "/problems/two-sum/start", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	after := get(t, h, "/problems/two-sum/solve").Body.String()
	for _, want := range []string{"data-tutor-input", "tutor-presets", "solve-clock", "data-editor", "data-run", "data-submit"} {
		if !strings.Contains(after, want) {
			t.Errorf("active workspace missing %s", want)
		}
	}
	if strings.Contains(after, "data-start") {
		t.Error("Start should disappear during an attempt")
	}
	if !strings.Contains(after, `data-work-panel="notes" hidden`) || !strings.Contains(after, "My approach was brute force first.") {
		t.Error("notes must remain available in their own tab during an attempt")
	}

	if _, err := prog.Rate("two-sum", 4, srv.now()); err != nil {
		t.Fatal(err)
	}
	checkIdle(get(t, h, "/problems/two-sum/solve").Body.String(), true)
}

// Entries rated before the log kept a per-sitting record carry a confidence
// with no history behind it. That shape is on disk, so the record must not
// claim nothing was ever recorded.
func TestRecordHandlesARatingWithNoPerSittingHistory(t *testing.T) {
	srv, _, prog := testSite(t)
	if _, err := prog.Update("two-sum", func(e *progress.Entry) {
		e.Status = progress.Solved
		e.Attempts = 1
		e.Confidence = 4
		e.ReviewStage = 2
		e.MinutesSpent = 15
	}); err != nil {
		t.Fatal(err)
	}

	body := get(t, srv.Handler(), "/problems/two-sum/solve").Body.String()
	if strings.Contains(body, "Nothing recorded yet") {
		t.Error("a rated problem is reported as having no record")
	}
	if !strings.Contains(body, "Last rated 4 of 5") {
		t.Error("the record does not fall back to the stored rating")
	}
	if strings.Contains(body, "First sitting") {
		t.Error("a solved problem is headlined as a first sitting")
	}
	if strings.Contains(body, "record-table") {
		t.Error("an empty history should not render a table")
	}
}

func TestRunTestsRejectsAnEmptyEditorInsteadOfTestingOldCode(t *testing.T) {
	srv, _, _ := testSite(t)
	called := false
	srv.runTests = func(context.Context, string, string) (testRun, error) {
		called = true
		return testRun{Output: "old code passed", OK: true, Ran: true, Passed: 1}, nil
	}
	for _, code := range []string{"", "  \n"} {
		rec := postJSON(t, srv.Handler(), "/problems/two-sum/test", saveRequest{Code: code})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("empty editor returned %d", rec.Code)
		}
	}
	if called {
		t.Fatal("ran tests against stale code")
	}
}
