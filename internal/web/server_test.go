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
	write("content/problems/0001-two-sum/signature.txt", "func twoSum(nums []int, target int) []int {\n    \n}\n")

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
	if !strings.Contains(body, "data-create") {
		t.Error("no create button on a problem with no solution file")
	}
	if strings.Contains(body, "data-editor") {
		t.Error("an editor was rendered for a solution that does not exist")
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

func TestSaveFormatsAndPersists(t *testing.T) {
	srv, root, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", struct{}{}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}

	// Valid Go, badly laid out: gofmt is what makes a textarea usable.
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
	if !strings.Contains(got.Code, "\treturn nil") {
		t.Errorf("not gofmt'd: %q", got.Code)
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
func TestSaveKeepsUnparseableSourceAndSaysWhy(t *testing.T) {
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
	if got.FormatError == "" {
		t.Error("source that does not parse reported no error")
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
	srv.runTests = func(_ context.Context, root, pkgDir string) (string, bool, error) {
		sawRoot, sawPkg = root, pkgDir
		b, _ := os.ReadFile(filepath.Join(root, pkgDir, "solution.go"))
		sawOnDisk = string(b)
		return "ok  \tneetcode150/solutions/0001-two-sum\t0.21s\n", true, nil
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

	srv.runTests = func(context.Context, string, string) (string, bool, error) {
		return "--- FAIL: TestTwoSum\n", false, nil
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
	srv.runTests = func(context.Context, string, string) (string, bool, error) {
		return "", false, errors.New("could not run go test: exec: \"go\": not found")
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
