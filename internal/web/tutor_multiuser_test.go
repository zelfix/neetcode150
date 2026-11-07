package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
)

// cumulativeCostScript answers three turns, reporting the cost of the session
// so far each time — which is what the Claude CLI actually does.
const cumulativeCostScript = `
echo '{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5","permissionMode":"plan","tools":["Read"]}'
turn=0
while IFS= read -r line; do
  turn=$((turn+1))
  echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"A question."}}}'
  case $turn in
    1) echo '{"type":"result","subtype":"success","total_cost_usd":0.30}' ;;
    2) echo '{"type":"result","subtype":"success","total_cost_usd":0.36}' ;;
    *) echo '{"type":"result","subtype":"success","total_cost_usd":0.40}' ;;
  esac
done
`

// The trap this guards. total_cost_usd is the cost of the session so far, not
// of the turn, so adding up the reported figures across three turns gives
// $1.06 where the session actually cost $0.40 — and a $5 ceiling would then
// close at about a fifth of the work it is meant to allow.
func TestTutorSpendCountsTheTurnAndNotTheRunningTotal(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, cumulativeCostScript)
	h := srv.Handler()

	for _, text := range []string{"first", "second", "third"} {
		rec := postJSON(t, h, "/problems/two-sum/tutor", tutorSendRequest{Text: text})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", text, rec.Code, rec.Body.String())
		}
	}

	// Spend is written as each turn finishes, which outlives the request, so
	// wait for the last one to land.
	var spent float64
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		u, err := prog.DB().User(studydb.OwnerID)
		if err != nil {
			t.Fatal(err)
		}
		spent = u.SpendUSD
		if spent >= 0.40 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if spent > 0.41 {
		t.Fatalf("spend = %.4f; the running totals were summed instead of differenced (that would give 1.06)", spent)
	}
	if spent < 0.39 {
		t.Fatalf("spend = %.4f, want about 0.40", spent)
	}
}

// A ceiling that only notices once the money is gone is not a ceiling.
func TestTheTutorStopsAtTheMonthlyCeiling(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()

	// Give the owner a small allowance and spend it.
	if _, err := prog.DB().SQL.Exec("UPDATE users SET tutor_budget_usd=? WHERE id=?", 0.50, studydb.OwnerID); err != nil {
		t.Fatal(err)
	}
	month := srv.now().Format("2006-01")
	if _, err := prog.DB().AddTutorSpend(studydb.OwnerID, 0.50, month); err != nil {
		t.Fatal(err)
	}

	rec := postJSON(t, h, "/problems/two-sum/tutor", tutorSendRequest{Text: "one more"})
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("over budget = %d, want 402", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "allowance") {
		t.Errorf("refusal does not explain itself: %s", rec.Body.String())
	}

	// A new month starts the counter again rather than carrying the debt.
	if _, err := prog.DB().AddTutorSpend(studydb.OwnerID, 0, "1999-01"); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(t, h, "/problems/two-sum/tutor", tutorSendRequest{Text: "next month"}); rec.Code != http.StatusOK {
		t.Fatalf("new month = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// No ceiling means no ceiling, which is what the owner's own row carries.
func TestAnUncappedPersonIsNeverRefused(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	if _, err := prog.DB().AddTutorSpend(studydb.OwnerID, 99, srv.now().Format("2006-01")); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(t, srv.Handler(), "/problems/two-sum/tutor", tutorSendRequest{Text: "hi"}); rec.Code != http.StatusOK {
		t.Fatalf("uncapped owner refused: %d %s", rec.Code, rec.Body.String())
	}
}

// Two people on one problem must not share a process or a transcript.
func TestTwoPeopleGetSeparateTutorConversations(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	guestUser, err := srv.db.CreateUser("dima", studydb.HashToken("dima-link"), 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	owner := srv.tutor.Get(prog.Scope(), "two-sum")
	theirs := srv.tutor.Get(srv.db.For(guestUser.ID), "two-sum")
	if owner != nil && theirs != nil && owner == theirs {
		t.Fatal("both people were handed the same session")
	}

	if _, err := srv.tutor.Send(prog.Scope(), problemOf(t, srv, "two-sum"), "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.tutor.Send(srv.db.For(guestUser.ID), problemOf(t, srv, "two-sum"), "theirs"); err != nil {
		t.Fatal(err)
	}
	a := srv.tutor.Get(prog.Scope(), "two-sum")
	b := srv.tutor.Get(srv.db.For(guestUser.ID), "two-sum")
	if a == nil || b == nil {
		t.Fatal("a session went missing")
	}
	if a == b {
		t.Fatal("one session served two people")
	}
	// And their stored transcripts are separate rows.
	ownerRec, err := prog.Scope().Session("two-sum")
	if err != nil || ownerRec == nil {
		t.Fatalf("owner session not stored: %v", err)
	}
	guestRec, err := srv.db.For(guestUser.ID).Session("two-sum")
	if err != nil || guestRec == nil {
		t.Fatalf("guest session not stored: %v", err)
	}
	if ownerRec.ID == guestRec.ID {
		t.Fatal("both people wrote to one stored session")
	}
}

// The panel is offered to guests only on a provider that cannot read the
// server's files. The CLI providers run an agent rooted at the repository, so
// a guest's tutor there could read everybody's solutions and the database — a
// wider hole than the test sandbox closes.
func TestGuestsGetTheTutorOnlyWhereItCannotReadTheServer(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	token, _ := guest(t, srv, "dima")

	if !srv.tutor.FileAccess() {
		t.Fatal("the CLI provider should report file access")
	}
	// The same-origin guard sits outside the tutor gate, so the request has to
	// pass it first or this tests the wrong layer.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/problems/two-sum/tutor/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a guest reached a file-reading tutor: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "owner") {
		t.Errorf("the refusal does not say why: %s", rec.Body.String())
	}
}

func problemOf(t *testing.T, srv *Server, slug string) catalog.Problem {
	t.Helper()
	got, ok := srv.cat.BySlug(slug)
	if !ok {
		t.Fatalf("no problem %q", slug)
	}
	return got
}
