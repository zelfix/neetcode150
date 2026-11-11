package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/studydb"
)

// mintInvite records one directly, the way `nc invite new` would.
func mintInvite(t *testing.T, srv *Server, token string, uses int, budget float64, life time.Duration) studydb.Invite {
	t.Helper()
	now := srv.now()
	v, err := srv.db.CreateInvite(studydb.HashToken(token), uses, budget, "test", now, now.Add(life))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// postInvite submits the join form the way the page does: same-origin, form
// encoded, and with no cookie, because the whole point is not having one.
func postInvite(t *testing.T, h http.Handler, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/invite", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	h.ServeHTTP(rec, req)
	return rec
}

func TestAnInviteAsksForANameAndKeepsTheTokenOutOfTheForm(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	mintInvite(t, srv, "live-tok", 2, 5, time.Hour)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/invite/live-tok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /invite = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="name"`) {
		t.Error("the page does not ask for a name")
	}
	// The POST must not carry the secret in its URL.
	if !strings.Contains(body, `action="/invite"`) {
		t.Error("the form posts back to a URL containing the token")
	}
	// A page whose own URL holds a secret must not leak it onward.
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

// Unknown, expired and spent must be indistinguishable from each other and
// from having no link at all, or the endpoint tells an attacker which invites
// are live.
func TestADeadInviteLooksExactlyLikeNoLink(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	// CreateInvite refuses a deadline in the past, which is the point of it, so
	// an expired one is made by letting the clock pass it.
	base := srv.now()
	mintInvite(t, srv, "expired-tok", 1, 5, time.Hour)
	spent := mintInvite(t, srv, "spent-tok", 1, 5, 4*time.Hour)
	if err := srv.db.RevokeInvite(spent.ID); err != nil {
		t.Fatal(err)
	}
	srv.now = func() time.Time { return base.Add(2 * time.Hour) }

	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	nothing, baseline := get("/")
	for _, path := range []string{"/invite/never-existed", "/invite/expired-tok", "/invite/spent-tok"} {
		code, body := get(path)
		if code != nothing || body != baseline {
			t.Errorf("%s answered %d (want %d) with a different page", path, code, nothing)
		}
	}
}

func TestRedeemingAnInviteCreatesOneOwnIdentity(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	v := mintInvite(t, srv, "tok", 2, 7.5, time.Hour)

	rec := postInvite(t, h, url.Values{"token": {"tok"}, "name": {"dima"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("redeem = %d: %s", rec.Code, rec.Body)
	}

	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie was set, so the newcomer is not signed in")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Errorf("cookie = %+v; want the same attributes handleJoin sets", session)
	}
	// Their token is their own, not the invite's.
	if session.Value == "tok" {
		t.Error("the newcomer was signed in with the invite token itself")
	}

	users, err := srv.db.Users()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("%d users, want the owner and dima", len(users))
	}
	var dima studydb.User
	for _, u := range users {
		if u.Name == "dima" {
			dima = u
		}
	}
	if dima.BudgetUSD != 7.5 {
		t.Errorf("budget = %v, want the invite's 7.5 rather than a default", dima.BudgetUSD)
	}

	// The use was spent, and the cookie actually works.
	left, err := srv.db.InviteByToken("tok", srv.now())
	if err != nil || left.UsesLeft != v.UsesLeft-1 {
		t.Fatalf("after one redemption: %+v, %v", left, err)
	}
	page := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(session)
	h.ServeHTTP(page, req)
	if page.Code != http.StatusOK {
		t.Errorf("the newcomer's dashboard = %d, want 200", page.Code)
	}
}

func TestRedeemingRefusesABadNameWithoutSpendingAUse(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	mintInvite(t, srv, "tok", 1, 5, time.Hour)

	for _, name := range []string{"", "   ", strings.Repeat("x", 33), "two\nlines"} {
		rec := postInvite(t, h, url.Values{"token": {"tok"}, "name": {name}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q = %d, want 400", name, rec.Code)
		}
	}
	// A rejected name must not have cost the invite anything.
	if v, err := srv.db.InviteByToken("tok", srv.now()); err != nil || v.UsesLeft != 1 {
		t.Errorf("a refused name spent a use: %+v, %v", v, err)
	}

	// A name somebody already has is the newcomer's to fix, not a 500.
	if rec := postInvite(t, h, url.Values{"token": {"tok"}, "name": {"owner"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("duplicate name = %d, want 400", rec.Code)
	}
}

func TestRedeemingNeedsTheSameOriginHeadersAndALiveInvite(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	mintInvite(t, srv, "tok", 1, 5, time.Hour)

	// No Sec-Fetch-Site: this is the cross-site post the guard exists for.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/invite",
		strings.NewReader(url.Values{"token": {"tok"}, "name": {"x"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a post from elsewhere = %d, want 403", rec.Code)
	}

	// Spend the one use, then try again.
	if rec := postInvite(t, h, url.Values{"token": {"tok"}, "name": {"first"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("first redeem = %d: %s", rec.Code, rec.Body)
	}
	again := postInvite(t, h, url.Values{"token": {"tok"}, "name": {"second"}})
	if again.Code != http.StatusForbidden {
		t.Errorf("redeeming a spent invite = %d, want the same 403 as no link", again.Code)
	}
	if users, _ := srv.db.Users(); len(users) != 2 {
		t.Errorf("%d users exist; a spent invite let somebody else in", len(users))
	}
}
