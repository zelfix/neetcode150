package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"neetcode150/internal/studydb"
)

// These figures were a Coach page of their own until it was merged into the
// dashboard, so they are asserted against "/".
func TestDashboardReportsWhatIsNotSecure(t *testing.T) {
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

	body := get(t, h, "/").Body.String()
	for _, want := range []string{
		"solved unaided",
		"Two Pointers",       // the pattern taken with help
		"Valid Palindrome",   // the least secure problem
		"needed hint 5 of 5", // and why
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dashboard does not mention %q", want)
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

func TestDashboardOnAnEmptySlate(t *testing.T) {
	srv, _, _ := testSite(t)
	rec := get(t, srv.Handler(), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nothing rated yet") {
		t.Error("an unrated slate should say so rather than show zeroes")
	}
}

// The page is gone, not moved: nothing links it and the site is not published.
func TestTheCoachPageIsNowTheDashboard(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	for _, path := range []string{"/coach/", "/coach"} {
		if code := get(t, h, path).Code; code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
	// Two of the sections only render once something is rated.
	if _, err := prog.RateHinted("two-sum", 2, 5, srv.now()); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, "/").Body.String()
	// The four sections it brought with it, plus the per-pattern table that
	// replaced the dashboard's own lanes.
	for _, want := range []string{"solved unaided", "Where to go back", "Every pattern"} {
		if !strings.Contains(body, want) {
			t.Errorf("the dashboard is missing %q", want)
		}
	}
	if strings.Contains(body, ">Coach<") {
		t.Error("the nav still offers a separate Coach page")
	}
}

// Which fixtures a run broke is the one thing the test output knows and
// nothing used to keep.
func TestRunningTestsRecordsWhichCasesBroke(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	postJSON(t, h, "/problems/two-sum/create", map[string]string{})

	srv.runTests = runnerFunc(func(context.Context, runRequest) (testRun, error) {
		return testRun{Output: "--- FAIL\n", Ran: true, Passed: 1, Failed: 2, Cases: []studydb.FailedCase{
			{Kind: "edge", Name: "empty input"},
			{Kind: "example", Name: "example 1"},
		}}, nil
	})
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
	srv.runTests = runnerFunc(func(context.Context, runRequest) (testRun, error) {
		return testRun{}, errors.New("could not run go test")
	})
	postJSON(t, h, "/problems/two-sum/test", map[string]string{"code": "package twosum\n"})
	if tally, _ := db.Owner().Tally("two-sum"); tally.Runs != 2 {
		t.Errorf("a failed run was recorded: %d runs", tally.Runs)
	}

	body := get(t, h, "/").Body.String()
	if !strings.Contains(body, "empty input") || !strings.Contains(body, "Cases you break most") {
		t.Error("the dashboard does not show the broken cases")
	}
}
