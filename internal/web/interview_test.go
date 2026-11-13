package web

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/studydb"
	"neetcode150/internal/tutor"
	"neetcode150/internal/workspace"
)

// interviewScript answers every turn with one decision, so a test can drive the
// whole sitting without caring which prompt arrived.
func interviewScript(reply string) string {
	return `
while IFS= read -r line; do
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"` + reply + `"}]}}'
  echo '{"type":"result","subtype":"success"}'
done
`
}

func startInterview(t *testing.T, h http.Handler, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if form.Get("problems") == "" {
		form.Set("problems", "2")
	}
	if form.Get("limit") == "" {
		form.Set("limit", "20")
	}
	if form.Get("lang") == "" {
		form.Set("lang", "go")
	}
	return postForm(t, h, "/interview/start", form)
}

// liveState reads the interview row back the way the handlers do.
func liveState(t *testing.T, srv *Server) (*studydb.Interview, interviewState) {
	t.Helper()
	rec, err := srv.db.Owner().LiveInterview()
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("no live interview")
	}
	var st interviewState
	if err := json.Unmarshal(rec.Payload, &st); err != nil {
		t.Fatal(err)
	}
	return rec, st
}

func TestAnInterviewDrawsItsProblemsAndWithholdsThePattern(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, interviewScript("Right, how will you approach it?"))
	h := srv.Handler()

	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, st := liveState(t, srv)
	if len(st.Problems) != 2 {
		t.Fatalf("drew %d problems, want 2", len(st.Problems))
	}
	if st.Problems[0].Slug == st.Problems[1].Slug {
		t.Error("the same problem was drawn twice")
	}
	if st.Problems[0].Started == nil {
		t.Error("the first problem's clock never started")
	}
	if st.Problems[1].Started != nil {
		t.Error("a problem that has not been reached is already running")
	}

	// The pattern is the one thing a random draw has to withhold, on the
	// interview page and in the workspace it sends you to. Taken from whichever
	// problem was actually drawn, so the assertion does not depend on the draw.
	current, ok := srv.cat.BySlug(st.Problems[0].Slug)
	if !ok {
		t.Fatalf("unknown problem %q", st.Problems[0].Slug)
	}
	named := func(body, where string) {
		t.Helper()
		for _, form := range []string{current.Pattern, template.HTMLEscapeString(current.Pattern)} {
			if form != "" && strings.Contains(body, form) {
				t.Errorf("%s names the pattern %q during an interview", where, current.Pattern)
			}
		}
	}
	named(get(t, h, "/interview/").Body.String(), "the interview page")
	work := get(t, h, "/problems/"+st.Problems[0].Slug+"/solve").Body.String()
	named(work, "the workspace")
	// Including the file it hands over: the generated header names the problem.
	code, err := os.ReadFile(workspace.Interview(srv.root, studydb.OwnerID, rec.ID).Path(current, "solution.go"))
	if err != nil {
		t.Fatal(err)
	}
	named(string(code), "the scaffold")
	if !strings.Contains(work, "problem 1 of 2") {
		t.Error("the workspace does not say where you are in the interview")
	}
	// The editor is live at once: the interview scaffolded the file and its own
	// clock is already running, so there is nothing to press Start for.
	if strings.Contains(work, "data-start") {
		t.Error("the workspace still offers Start during an interview")
	}
}

// An interview must not touch the study files: the problems are scaffolded
// blank, which over a solution they already have would cost them the work.
func TestInterviewFilesLeaveTheStudyTreeAlone(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, interviewScript("Go on."))
	h := srv.Handler()

	// A solution already on disk for every problem, so a clobber would show.
	for _, slug := range []string{"two-sum", "valid-anagram", "contains-duplicate"} {
		p, ok := srv.cat.BySlug(slug)
		if !ok {
			continue
		}
		dir := workspace.Owner(srv.root).Dir(p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "solution.go"), []byte("// MY REAL SOLUTION\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, st := liveState(t, srv)

	for _, slug := range []string{"two-sum", "valid-anagram", "contains-duplicate"} {
		p, ok := srv.cat.BySlug(slug)
		if !ok {
			continue
		}
		body, err := os.ReadFile(filepath.Join(workspace.Owner(srv.root).Dir(p), "solution.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "MY REAL SOLUTION") {
			t.Errorf("the interview overwrote the study solution for %s", slug)
		}
	}
	first, ok := srv.cat.BySlug(st.Problems[0].Slug)
	if !ok {
		t.Fatalf("unknown problem %q", st.Problems[0].Slug)
	}
	lay := workspace.Interview(srv.root, studydb.OwnerID, rec.ID)
	if _, err := os.Stat(lay.Path(first, "solution.go")); err != nil {
		t.Errorf("the interview scaffold is not in its own directory: %v", err)
	}
	if !strings.Contains(lay.PkgDir(first), "interview-") {
		t.Errorf("interview PkgDir = %q", lay.PkgDir(first))
	}
}

// The whole point of the decision: an interview writes no rating and moves no
// review date. What it does record is the time.
func TestInterviewRecordsTimeButNeitherRatingNorSchedule(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, interviewScript("{\\\"decision\\\":\\\"satisfied\\\",\\\"message\\\":\\\"That works.\\\",\\\"note\\\":\\\"Reached the hash map unaided.\\\"}"))
	h := srv.Handler()

	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, st := liveState(t, srv)
	slug := st.Problems[0].Slug
	before := prog.Get(slug)

	// Twenty-five minutes on the clock, then move on.
	started := st.Problems[0].Started.Add(-25 * time.Minute)
	st.Problems[0].Started = &started
	payload, err := json.Marshal(&st)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.db.Owner().SaveInterview(rec.ID, payload); err != nil {
		t.Fatal(err)
	}
	if code := postForm(t, h, "/interview/next", url.Values{}).Code; code != http.StatusSeeOther {
		t.Fatalf("next = %d", code)
	}

	after := prog.Get(slug)
	if after.MinutesSpent-before.MinutesSpent != 25 {
		t.Errorf("logged %d minutes, want 25", after.MinutesSpent-before.MinutesSpent)
	}
	if len(after.History) != len(before.History) {
		t.Error("the interview wrote a rating")
	}
	if after.Confidence != before.Confidence {
		t.Error("the interview changed the recorded confidence")
	}
	switch {
	case before.NextReview == nil && after.NextReview != nil:
		t.Error("the interview put the problem on the review ladder")
	case before.NextReview != nil && after.NextReview != nil && !before.NextReview.Equal(*after.NextReview):
		t.Error("the interview moved the review date")
	}
	if attempts, err := srv.db.Owner().Attempts(slug); err != nil {
		t.Fatal(err)
	} else if len(attempts) != 0 {
		t.Errorf("the interview archived %d attempts", len(attempts))
	}
}

// The time-up turn is bought once. A tab polling every second must not keep
// buying it, which is why the flag is on the row rather than in the page.
func TestTheTimeIsUpOnlyOnce(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, interviewScript("Time is up on this one."))
	h := srv.Handler()
	if rec := startInterview(t, h, url.Values{"limit": {"5"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, st := liveState(t, srv)

	// Before the limit, nothing is said.
	if said := timeUp(t, h); said {
		t.Error("the interviewer was told the time was up before it was")
	}
	started := st.Problems[0].Started.Add(-6 * time.Minute)
	st.Problems[0].Started = &started
	payload, err := json.Marshal(&st)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.db.Owner().SaveInterview(rec.ID, payload); err != nil {
		t.Fatal(err)
	}

	if said := timeUp(t, h); !said {
		t.Error("the interviewer was never told the time was up")
	}
	for i := range 3 {
		if said := timeUp(t, h); said {
			t.Errorf("report %d bought a second time-up turn", i+2)
		}
	}
	if _, st := liveState(t, srv); !st.Problems[0].TimeUp {
		t.Error("the time-up flag was not recorded")
	}
}

// The row stays live while the feedback is written, but no problem is current,
// so a tab whose clock ran out in that gap has nothing to report against. It
// used to index past the end of the problems.
func TestTimeUpWhileTheInterviewIsBeingWrittenUpIsNothing(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, "while IFS= read -r line; do :; done\n")
	h := srv.Handler()
	if rec := startInterview(t, h, url.Values{"limit": {"5"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	if code := postForm(t, h, "/interview/finish", url.Values{}).Code; code != http.StatusSeeOther {
		t.Fatalf("finish = %d", code)
	}
	if _, st := liveState(t, srv); !st.Finishing {
		t.Fatal("the row was not marked as finishing")
	}
	if said := timeUp(t, h); said {
		t.Error("a time-up turn was bought for an interview with no problem open")
	}
}

func timeUp(t *testing.T, h http.Handler) bool {
	t.Helper()
	rec := postJSON(t, h, "/interview/timeup", struct{}{})
	if rec.Code != http.StatusOK {
		t.Fatalf("timeup = %d: %s", rec.Code, rec.Body)
	}
	var out map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out["said"]
}

// Finishing answers at once and writes the feedback behind it: the write is a
// model turn, and doing it inside the request left the browser waiting on a
// page with nothing on it.
func TestFinishingAnInterviewWritesTheFeedback(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, interviewScript("You reached the right idea on the second problem; the first stalled on the boundary case."))
	h := srv.Handler()
	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, _ := liveState(t, srv)

	finish := postForm(t, h, "/interview/finish", url.Values{})
	if finish.Code != http.StatusSeeOther {
		t.Fatalf("finish = %d: %s", finish.Code, finish.Body)
	}
	if got := finish.Header().Get("Location"); !strings.HasSuffix(got, "/interview/"+strconv.FormatInt(rec.ID, 10)) {
		t.Errorf("finish redirected to %q, want the report", got)
	}

	// Waiting for the report, which arrives after the response did.
	var stored *studydb.Interview
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := srv.db.Owner().InterviewByID(rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil && got.Finished != nil {
			stored = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stored == nil {
		t.Fatal("the interview never finished")
	}
	if live, err := srv.db.Owner().LiveInterview(); err != nil {
		t.Fatal(err)
	} else if live != nil {
		t.Error("the interview is still live after the report landed")
	}
	var st interviewState
	if err := json.Unmarshal(stored.Payload, &st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Report, "stalled") {
		t.Errorf("the feedback is not what the interviewer wrote: %q", st.Report)
	}
	if body := get(t, h, "/interview/"+strconv.FormatInt(rec.ID, 10)).Body.String(); !strings.Contains(body, "Feedback") {
		t.Error("the report page does not show the feedback")
	}
}

// While the feedback is being written the row stays live, so nothing starts a
// second interview in the gap — and the page says what is happening rather than
// showing an empty section.
func TestAnInterviewBeingWrittenUpSaysSo(t *testing.T) {
	srv, _, _ := testSite(t)
	// A tutor that never answers, so the interview stays in the finishing state
	// for the length of this test.
	fakeTutor(t, srv, "while IFS= read -r line; do :; done\n")
	h := srv.Handler()
	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, _ := liveState(t, srv)
	if code := postForm(t, h, "/interview/finish", url.Values{}).Code; code != http.StatusSeeOther {
		t.Fatalf("finish = %d", code)
	}

	_, st := liveState(t, srv)
	if !st.Finishing {
		t.Error("the row was not marked as finishing")
	}
	page := get(t, h, "/interview/"+strconv.FormatInt(rec.ID, 10)).Body.String()
	if !strings.Contains(page, "data-interview-pending") {
		t.Error("the report page does not poll for the feedback")
	}
	if !strings.Contains(page, "The interviewer is writing it") {
		t.Error("the report page does not say the feedback is on its way")
	}
	// Starting another one in the gap is still refused.
	if body := startInterview(t, h, nil).Body.String(); !strings.Contains(body, "already in progress") {
		t.Error("a second interview started while the first was being written up")
	}
	// And no problem is current any more, so the workspace is out of interview
	// mode: the editor must not still be live on a problem that is over.
	work := get(t, h, "/problems/"+st.Problems[0].Slug+"/solve").Body.String()
	if strings.Contains(work, "data-interview=") {
		t.Error("the workspace is still in interview mode while the interview is being written up")
	}
}

// A second interview while one is live is refused by the database, not by a
// check a second tab could race past.
func TestOnlyOneInterviewRunsAtATime(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, interviewScript("Go on."))
	h := srv.Handler()
	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	body := startInterview(t, h, nil).Body.String()
	if !strings.Contains(body, "already in progress") {
		t.Errorf("a second interview was not refused: %q", body)
	}

	_, st := liveState(t, srv)
	payload, err := json.Marshal(&st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.Owner().CreateInterview(time.Now(), payload); err == nil {
		t.Error("the database allowed a second live interview")
	}
}

// Drill is gone, and so is its URL.
func TestDrillIsGone(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	for _, path := range []string{"/drill/", "/drill"} {
		if code := get(t, h, path).Code; code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
	if nav := get(t, h, "/").Body.String(); strings.Contains(nav, ">Drill<") {
		t.Error("the nav still offers Drill")
	}
}

// The language is chosen once, on the way in, and it governs the whole
// interview — not the study language of whichever problems were drawn.
func TestTheInterviewLanguageIsChosenOnTheWayIn(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, interviewScript("Go on."))
	h := srv.Handler()

	// Every drawn problem needs a Python stub, or the draw refuses the language.
	for _, dir := range []string{"0001-two-sum", "0125-valid-palindrome"} {
		if err := os.MkdirAll(filepath.Join(srv.root, "content/problems", dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srv.root, "content/problems", dir, "signature.py"),
			[]byte("class Solution:\n    def solve(self) -> int:\n        \n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A problem studied in Go must not decide the language of an interview
	// somebody began in Python.
	for _, slug := range []string{"two-sum", "valid-palindrome"} {
		if _, err := prog.SetLanguage(slug, "go"); err != nil {
			t.Fatal(err)
		}
	}
	page := get(t, h, "/interview/").Body.String()
	for _, want := range []string{`value="go"`, `value="python"`, "Begin in Go", "Begin in Python"} {
		if !strings.Contains(page, want) {
			t.Errorf("the start form is missing %q", want)
		}
	}

	if rec := startInterview(t, h, url.Values{"lang": {"python"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	rec, st := liveState(t, srv)
	if st.Lang != "python" {
		t.Errorf("interview language = %q, want python", st.Lang)
	}
	current, ok := srv.cat.BySlug(st.Problems[0].Slug)
	if !ok {
		t.Fatalf("unknown problem %q", st.Problems[0].Slug)
	}
	lay := workspace.Interview(srv.root, studydb.OwnerID, rec.ID)
	if _, err := os.Stat(lay.Path(current, "solution.py")); err != nil {
		t.Errorf("the interview did not scaffold Python: %v", err)
	}
	// And the workspace works in it, rather than in the problem's own language.
	work := get(t, h, "/problems/"+current.Slug+"/solve").Body.String()
	if !strings.Contains(work, "solution.py") {
		t.Error("the workspace is not editing the Python file")
	}
	// The study choice is untouched: the interview overrides, it does not write.
	if got := prog.Get(current.Slug).Lang; got != "go" {
		t.Errorf("the interview changed the problem's study language to %q", got)
	}
}

// The opening has to be on screen. It is sent with an empty display rather than
// as an internal turn: the panel skips internal-text, which is why the
// interviewer appeared to say nothing at all when this was first tried.
func TestTheInterviewerOpensVisibly(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, interviewScript("Hello — I will be interviewing you. Three problems, 20 minutes each."))
	h := srv.Handler()
	if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	_, st := liveState(t, srv)
	p, ok := srv.cat.BySlug(st.Problems[0].Slug)
	if !ok {
		t.Fatalf("unknown problem %q", st.Problems[0].Slug)
	}

	var kinds []string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		sess := srv.tutor.GetAs(srv.db.Owner(), p.Slug, tutor.RoleInterviewer)
		if sess != nil {
			kinds = nil
			for _, e := range sess.Transcript() {
				kinds = append(kinds, e.Kind)
			}
			if slices.Contains(kinds, tutor.KindText) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Contains(kinds, tutor.KindText) {
		t.Errorf("the opening never reached the panel as visible text: %v", kinds)
	}
	if slices.Contains(kinds, tutor.KindInternalText) {
		t.Errorf("the opening was published as an internal turn: %v", kinds)
	}
	// The prompt itself stays off screen: an empty display means the panel
	// shows the reply without the instructions that produced it.
	for _, e := range srv.tutor.GetAs(srv.db.Owner(), p.Slug, tutor.RoleInterviewer).Transcript() {
		if e.Kind == tutor.KindUser && strings.Contains(e.Text, "Greet them") {
			t.Error("the opening instructions are visible in the transcript")
		}
	}
}

// Submitting inside an interview: the interviewer answers, the note is kept for
// the report, and none of the study record moves. A follow-up leaves the problem
// open; being satisfied closes it without anybody pressing a button.
func TestSubmittingInsideAnInterview(t *testing.T) {
	for _, tc := range []struct {
		name, decision string
		advances       bool
	}{
		{"a follow-up keeps it open", "followup", false},
		{"satisfied moves on by itself", "satisfied", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, prog := testSite(t)
			fakeTutor(t, srv, interviewScript(`{\"decision\":\"`+tc.decision+
				`\",\"message\":\"What changes if the values do not fit in memory?\",\"note\":\"Hash map reached unaided, no boundary check.\"}`))
			h := srv.Handler()
			srv.runTests = runnerFunc(func(context.Context, runRequest) (testRun, error) {
				return testRun{OK: true, Output: "ok\t0.1s"}, nil
			})

			if rec := startInterview(t, h, nil); rec.Code != http.StatusSeeOther {
				t.Fatalf("start = %d: %s", rec.Code, rec.Body)
			}
			_, st := liveState(t, srv)
			slug := st.Problems[0].Slug
			before := prog.Get(slug)

			rec := postJSON(t, h, "/problems/"+slug+"/submit", map[string]string{"code": "package solution\n"})
			if rec.Code != http.StatusOK {
				t.Fatalf("submit = %d: %s", rec.Code, rec.Body)
			}
			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out["ratingError"] != nil {
				t.Fatalf("submit reported %v", out["ratingError"])
			}

			// The decision turn is detached, like the submission flow's.
			var got interviewState
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				_, got = liveState(t, srv)
				if len(got.Problems[0].Notes) > 0 && got.Current == boolTo(tc.advances) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(got.Problems[0].Notes) == 0 {
				t.Fatal("the interviewer recorded no note for the submission")
			}
			if !strings.Contains(got.Problems[0].Notes[0], "boundary") {
				t.Errorf("note = %q, want what the interviewer wrote", got.Problems[0].Notes[0])
			}
			if got.Problems[0].Submits != 1 || !got.Problems[0].Passed {
				t.Errorf("the submission was not recorded: %+v", got.Problems[0])
			}
			if want := boolTo(tc.advances); got.Current != want {
				t.Errorf("current problem = %d after %q, want %d", got.Current, tc.decision, want)
			}
			if tc.advances && got.Problems[0].Closed == nil {
				t.Error("moving on left the finished problem open")
			}

			after := prog.Get(slug)
			if len(after.History) != len(before.History) || after.Status != before.Status {
				t.Errorf("submitting in an interview changed the study record: %+v", after)
			}
			if attempts, err := srv.db.Owner().Attempts(slug); err != nil {
				t.Fatal(err)
			} else if len(attempts) != 0 {
				t.Errorf("submitting in an interview archived %d attempts", len(attempts))
			}
		})
	}
}

// boolTo is the index the interview should be on: still the first problem, or
// the second once the interviewer has moved on.
func boolTo(advanced bool) int {
	if advanced {
		return 1
	}
	return 0
}
