package web

import (
	"net/url"
	"testing"
	"time"

	"neetcode150/internal/progress"
)

func TestRateFormRecordsTheRung(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	if rec := postForm(t, h, "/problems/two-sum/rate",
		url.Values{"confidence": {"4"}, "rung": {"3"}, "from": {"/problems/two-sum"}}); rec.Code != 303 && rec.Code != 302 {
		t.Fatalf("rate returned %d: %s", rec.Code, rec.Body)
	}
	sittings := progress.Sittings(prog.Get("two-sum"))
	if len(sittings) != 1 {
		t.Fatalf("recorded %d ratings, want 1", len(sittings))
	}
	if got := sittings[0].Rung; got != 3 {
		t.Errorf("rung = %d, want 3", got)
	}
	// Rung 3 against a rating of 4 is the case the record calls generous; it is
	// the whole reason the form carries a rung at all.
	if !sittings[0].Generous {
		t.Error("rating 4 at rung 3 was not flagged as generous")
	}
}

// A bare confidence button has to behave exactly as it did before the ladder
// reached the form.
func TestRateFormWithoutARungIsUnaided(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	if rec := postForm(t, h, "/problems/two-sum/rate",
		url.Values{"confidence": {"5"}, "from": {"/problems/two-sum"}}); rec.Code >= 400 {
		t.Fatalf("rate returned %d: %s", rec.Code, rec.Body)
	}
	sittings := progress.Sittings(prog.Get("two-sum"))
	if len(sittings) != 1 || sittings[0].Rung != 0 {
		t.Fatalf("sittings = %+v; want one rating at rung 0", sittings)
	}
}

func TestRateFormAmendsAndUndoes(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	rate := func(form url.Values) {
		t.Helper()
		form.Set("from", "/problems/two-sum")
		if rec := postForm(t, h, "/problems/two-sum/rate", form); rec.Code >= 400 {
			t.Fatalf("rate returned %d: %s", rec.Code, rec.Body)
		}
	}

	rate(url.Values{"confidence": {"2"}, "rung": {"4"}})
	rate(url.Values{"action": {"amend"}, "confidence": {"5"}, "rung": {"1"}})

	sittings := progress.Sittings(prog.Get("two-sum"))
	if len(sittings) != 1 {
		t.Fatalf("amend added a rating instead of correcting one: %+v", sittings)
	}
	if sittings[0].Confidence != 5 || sittings[0].Rung != 1 {
		t.Errorf("after amend: rating %d rung %d; want 5 and 1", sittings[0].Confidence, sittings[0].Rung)
	}

	rate(url.Values{"action": {"undo"}})
	if got := progress.Sittings(prog.Get("two-sum")); len(got) != 0 {
		t.Errorf("undo left %d ratings, want none", len(got))
	}
	// Undoing a rating nobody gave is a no-op, not an error.
	rate(url.Values{"action": {"undo"}})
}

func TestRateFormRejectsAnImpossibleRung(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()

	rec := postForm(t, h, "/problems/two-sum/rate",
		url.Values{"confidence": {"4"}, "rung": {"9"}, "from": {"/problems/two-sum"}})
	if rec.Code != 400 {
		t.Errorf("rate returned %d for rung 9, want 400", rec.Code)
	}
	if got := progress.Sittings(prog.Get("two-sum")); len(got) != 0 {
		t.Errorf("a rejected rating was recorded anyway: %+v", got)
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
		url.Values{"confidence": {"4"}, "from": {"/problems/two-sum"}}); rec.Code >= 400 {
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
