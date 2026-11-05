package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"neetcode150/internal/studydb"
)

func TestCoachPageReportsWhatIsNotSecure(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	now := srv.now()

	// One pattern held unaided, one taken with the answer handed over.
	if _, err := prog.RateHinted("contains-duplicate", 5, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.RateHinted("two-sum", 4, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.RateHinted("valid-palindrome", 2, 5, now); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/coach/").Body.String()
	for _, want := range []string{
		"solved unaided",
		"Two Pointers",       // the pattern taken with help
		"Valid Palindrome",   // the least secure problem
		"needed hint rung 5", // and why
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the coach page does not mention %q", want)
		}
	}
	// Arrays & Hashing was held unaided, so it is not something to go back to.
	concerns := body
	if i := strings.Index(body, "Least secure"); i > 0 {
		concerns = body[:i]
	}
	if strings.Contains(concerns, "Arrays &amp; Hashing") {
		t.Error("a pattern held unaided was listed as something to go back to")
	}
}

func TestCoachPageOnAnEmptySlate(t *testing.T) {
	srv, _, _ := testSite(t)
	rec := get(t, srv.Handler(), "/coach/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /coach/ = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nothing rated yet") {
		t.Error("an unrated slate should say so rather than show zeroes")
	}
}

// Which fixtures a run broke is the one thing the test output knows and
// nothing used to keep.
func TestRunningTestsRecordsWhichCasesBroke(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	postJSON(t, h, "/problems/two-sum/create", map[string]string{})

	srv.runTests = func(context.Context, string, string) (testRun, error) {
		return testRun{Output: "--- FAIL\n", Ran: true, Passed: 1, Failed: 2, Cases: []studydb.FailedCase{
			{Kind: "edge", Name: "empty input"},
			{Kind: "example", Name: "example 1"},
		}}, nil
	}
	for range 2 {
		if rec := postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": "package twosum\n"}); rec.Code != http.StatusOK {
			t.Fatalf("POST test = %d", rec.Code)
		}
	}

	db := srv.db
	tally, err := db.Owner().Tally("two-sum")
	if err != nil {
		t.Fatal(err)
	}
	if tally.Runs != 2 || tally.Clean != 0 {
		t.Errorf("tally = %+v, want 2 runs and none clean", tally)
	}
	cases, err := db.Owner().CaseFailures("two-sum", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("recorded %d cases, want 2", len(cases))
	}
	for _, c := range cases {
		if c.Failures != 2 {
			t.Errorf("%s/%s counted %d failures, want 2 after two runs", c.Kind, c.Name, c.Failures)
		}
	}

	// A run that never happened says nothing about which fixtures are hard.
	srv.runTests = func(context.Context, string, string) (testRun, error) {
		return testRun{}, errors.New("could not run go test")
	}
	postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": "package twosum\n"})
	if tally, _ := db.Owner().Tally("two-sum"); tally.Runs != 2 {
		t.Errorf("a failed run was recorded: %d runs", tally.Runs)
	}

	body := get(t, h, "/coach/").Body.String()
	if !strings.Contains(body, "empty input") || !strings.Contains(body, "Cases you break most") {
		t.Error("the coach page does not show the broken cases")
	}
}
