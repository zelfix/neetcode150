package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
)

// The page ranks effort. The numbers the review schedule reads must not appear
// on it at all: ranking by them would put a price on admitting a hint was
// needed, which is the one thing the schedule cannot survive.
func TestThePeoplePageRanksEffortAndPublishesNoRatings(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	now := srv.now()

	// The owner finishes one problem, needing the ladder to do it.
	if _, err := prog.RateHinted("two-sum", 5, 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.AddMinutes("two-sum", 40, now); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/people/").Body.String()
	if !strings.Contains(body, "owner") {
		t.Fatalf("the owner is not listed:\n%s", body)
	}
	if !strings.Contains(body, "40") {
		t.Error("the minutes are not shown, so effort is not actually ranked")
	}

	// Nothing that belongs to the schedule.
	for _, leak := range []string{"rung", "Rung", "unaided", "Unaided", "5/5"} {
		// The explanatory note mentions rungs by name on purpose; what must not
		// appear is a value. Check the table only.
		table := body
		if at := strings.Index(body, "panel-note"); at > 0 {
			table = body[:at]
		}
		if strings.Contains(table, leak) {
			t.Errorf("the table leaks %q, which the schedule needs to stay honest", leak)
		}
	}
}

func TestThePeoplePageCountsEachPersonSeparately(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	now := srv.now()
	guestToken, guestProg := guest(t, srv, "dima")

	// The owner solves two, the guest one.
	for _, slug := range []string{"two-sum", "valid-palindrome"} {
		if _, err := prog.SetStatus(slug, progress.Solved, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := guestProg.SetStatus("contains-duplicate", progress.Solved, now); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/people/").Body.String()
	ownerAt, guestAt := strings.Index(body, "owner"), strings.Index(body, "dima")
	if ownerAt < 0 || guestAt < 0 {
		t.Fatalf("both people should be listed:\n%s", body)
	}
	if ownerAt > guestAt {
		t.Error("two solved problems should rank above one")
	}
	// Each person sees the same table, and their own row is marked.
	own := getAs(t, h, "/people/", guestToken).Body.String()
	if !strings.Contains(own, "is-you") {
		t.Error("the guest's own row is not marked on their view")
	}
}

func TestRenamingIsALabelChangeAndNothingMore(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	if _, err := prog.SetStatus("two-sum", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}

	rec := postForm(t, h, "/me/name", url.Values{"name": {"swift_heron_314"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", rec.Code, rec.Body)
	}
	owner, err := srv.db.User(studydb.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Name != "swift_heron_314" {
		t.Errorf("name = %q, want the new one", owner.Name)
	}
	// The work is keyed by id, so it is still there under the new label.
	if got := prog.Get("two-sum").Status; got != progress.Solved {
		t.Errorf("status after a rename = %s, want solved", got)
	}

	// A name somebody else holds is refused, case included.
	guest(t, srv, "Dima")
	if rec := postForm(t, h, "/me/name", url.Values{"name": {"dima"}}); !strings.Contains(rec.Body.String(), "already goes by that") {
		t.Errorf("a name differing only in case was accepted: %d", rec.Code)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 33)} {
		if rec := postForm(t, h, "/me/name", url.Values{"name": {bad}}); rec.Code >= 400 {
			t.Errorf("name %q returned %d; it should re-render with a message", bad, rec.Code)
		}
	}
}

// A suggested name is what a newcomer is offered, so that joining does not
// require deciding how much of a real name to hand a study group.
func TestAnInviteOffersAPseudonym(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	mintInvite(t, srv, "tok", 1, 5, time.Hour)

	body := get(t, h, "/invite/tok").Body.String()
	// adjective_noun_123, pre-filled in the field.
	if !strings.Contains(body, `value="`) || strings.Count(body, "_") < 2 {
		t.Errorf("no pseudonym was suggested:\n%s", body)
	}
	for range 20 {
		if a, b := srv.freeSuggestion(), srv.freeSuggestion(); a == b {
			t.Fatalf("two suggestions in a row were identical (%q)", a)
		}
	}
}
