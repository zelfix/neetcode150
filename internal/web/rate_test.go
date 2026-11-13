package web

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/progress"
)

// The buttons are rendered from the table the handler reads, which is the whole
// point of sharing it: a label that drifted from the value beside it would
// record something other than what it says.
func TestReadingPageRendersEveryRateOutcome(t *testing.T) {
	srv, _, _ := testSite(t)
	body := get(t, srv.Handler(), "/problems/two-sum").Body.String()
	for _, o := range rateOutcomes {
		for _, want := range []string{`value="` + o.Key + `"`, o.Label} {
			if !strings.Contains(body, want) {
				t.Errorf("the rate form is missing %q", want)
			}
		}
	}
	for _, gone := range []string{`name="rung"`, `name="confidence"`, `value="amend"`} {
		if strings.Contains(body, gone) {
			t.Errorf("%s is still on the page", gone)
		}
	}
}

// Each button is one of the four moves the schedule can make, which is why
// there are four of them rather than the five-point scale that used to be here.
// Three unaided solves first, so that holding a stage and falling two are
// visible as something other than the floor.
func TestRateOutcomesCoverTheSchedulesFourMoves(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		stage   int
		status  progress.Status
	}{
		{"answer", 1, progress.Shaky},
		{"hint", 2, progress.Shaky},
		{"effort", 4, progress.Solved},
		{"easy", 5, progress.Solved},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			srv, _, prog := testSite(t)
			h := srv.Handler()
			rate := func(outcome string) {
				t.Helper()
				rec := postForm(t, h, "/problems/two-sum/rate",
					url.Values{"outcome": {outcome}, "from": {"/problems/two-sum"}})
				if rec.Code >= 400 {
					t.Fatalf("rate %q returned %d: %s", outcome, rec.Code, rec.Body)
				}
			}
			for range 3 {
				rate("easy")
			}
			if got := prog.Get("two-sum").ReviewStage; got != 4 {
				t.Fatalf("three unaided solves put the problem at stage %d, want 4", got)
			}

			rate(tc.outcome)
			e := prog.Get("two-sum")
			if e.ReviewStage != tc.stage {
				t.Errorf("%q left it at stage %d, want %d", tc.outcome, e.ReviewStage, tc.stage)
			}
			if e.Status != tc.status {
				t.Errorf("%q left the status %q, want %q", tc.outcome, e.Status, tc.status)
			}
			if e.NextReview == nil {
				t.Error("the problem left the review ladder")
			}
		})
	}
}

// The form sets both ends of a rating itself, so the pair that says "I was
// comfortable but I was shown the invariant" cannot be submitted at all. That
// is the whole reason the rung control is gone rather than merely hidden.
func TestNoRateOutcomeLooksGenerous(t *testing.T) {
	for _, o := range rateOutcomes {
		srv, _, prog := testSite(t)
		h := srv.Handler()
		if rec := postForm(t, h, "/problems/two-sum/rate",
			url.Values{"outcome": {o.Key}, "from": {"/problems/two-sum"}}); rec.Code >= 400 {
			t.Fatalf("rate %q returned %d: %s", o.Key, rec.Code, rec.Body)
		}
		sittings := progress.Sittings(prog.Get("two-sum"))
		if len(sittings) != 1 {
			t.Fatalf("%q recorded %d ratings, want 1", o.Key, len(sittings))
		}
		if sittings[0].Confidence != o.confidence || sittings[0].Rung != o.rung {
			t.Errorf("%q recorded rating %d rung %d, want %d and %d",
				o.Key, sittings[0].Confidence, sittings[0].Rung, o.confidence, o.rung)
		}
		if sittings[0].Generous {
			t.Errorf("%q produced a rating the record calls generous", o.Key)
		}
	}
}

func TestRateFormRejectsAnUnknownOutcome(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	// The empty case is a stale page posting the old `confidence` field.
	for _, form := range []url.Values{
		{"outcome": {"brilliant"}},
		{"outcome": {""}},
		{"confidence": {"4"}, "rung": {"3"}},
	} {
		form.Set("from", "/problems/two-sum")
		if rec := postForm(t, h, "/problems/two-sum/rate", form); rec.Code != 400 {
			t.Errorf("rate %v returned %d, want 400", form, rec.Code)
		}
	}
	if got := progress.Sittings(prog.Get("two-sum")); len(got) != 0 {
		t.Errorf("a rejected rating was recorded anyway: %+v", got)
	}
}

func TestRateFormUndoesAndRefusesToAmend(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	post := func(form url.Values) int {
		t.Helper()
		form.Set("from", "/problems/two-sum")
		return postForm(t, h, "/problems/two-sum/rate", form).Code
	}

	if code := post(url.Values{"outcome": {"hint"}}); code >= 400 {
		t.Fatalf("rate returned %d", code)
	}
	// Amending takes a rating and a rung, which is what this form stopped
	// asking for; the way to change one is to drop it and record it again.
	if code := post(url.Values{"action": {"amend"}, "confidence": {"5"}, "rung": {"1"}}); code != 400 {
		t.Errorf("amend returned %d, want 400", code)
	}
	if got := progress.Sittings(prog.Get("two-sum")); len(got) != 1 || got[0].Confidence != 2 {
		t.Fatalf("the refused amend changed the record: %+v", got)
	}

	if code := post(url.Values{"action": {"undo"}}); code >= 400 {
		t.Fatalf("undo returned %d", code)
	}
	if got := progress.Sittings(prog.Get("two-sum")); len(got) != 0 {
		t.Errorf("undo left %d ratings, want none", len(got))
	}
	// Undoing a rating nobody gave is a no-op, not an error.
	if code := post(url.Values{"action": {"undo"}}); code >= 400 {
		t.Errorf("a second undo returned %d", code)
	}
}

// Rating ends the sitting, so the clock has to be banked first. The form used
// to drop it, which made a problem rated from the reading page look instant.
func TestRatingFromTheReadingPageKeepsTheClock(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	if _, err := prog.BeginTimer("two-sum", srv.now().Add(-40*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rec := postForm(t, h, "/problems/two-sum/rate",
		url.Values{"outcome": {"effort"}, "from": {"/problems/two-sum"}}); rec.Code >= 400 {
		t.Fatalf("rate returned %d: %s", rec.Code, rec.Body)
	}
	if got := prog.Get("two-sum").MinutesSpent; got != 40 {
		t.Errorf("MinutesSpent = %d, want 40", got)
	}
}

func TestLogTimeTheClockMissed(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	log := func(minutes string) int {
		t.Helper()
		rec := postForm(t, h, "/problems/two-sum/time",
			url.Values{"minutes": {minutes}, "from": {"/problems/two-sum"}})
		if rec.Code >= 400 {
			return rec.Code
		}
		return 0
	}

	if code := log("25"); code != 0 {
		t.Fatalf("logging 25 minutes returned %d", code)
	}
	if got := prog.Get("two-sum").MinutesSpent; got != 25 {
		t.Fatalf("MinutesSpent = %d, want 25", got)
	}
	// It accumulates rather than replaces.
	if code := log("15"); code != 0 {
		t.Fatalf("logging 15 more minutes returned %d", code)
	}
	if got := prog.Get("two-sum").MinutesSpent; got != 40 {
		t.Errorf("MinutesSpent = %d after two entries, want 40", got)
	}

	for _, bad := range []string{"0", "-5", "5000", "abc", ""} {
		if code := log(bad); code != 400 {
			t.Errorf("logging %q returned %d, want 400", bad, code)
		}
	}
	if got := prog.Get("two-sum").MinutesSpent; got != 40 {
		t.Errorf("a rejected entry changed the total to %d", got)
	}
}
