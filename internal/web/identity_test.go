package web

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
	"neetcode150/internal/workspace"
)

// guest adds a second person and returns their link and their own store.
func guest(t *testing.T, srv *Server, name string) (token string, store *progress.Store) {
	t.Helper()
	token = name + "-link"
	u, err := srv.db.CreateUser(name, studydb.HashToken(token), 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token, progress.NewScoped(srv.db.For(u.ID))
}

func getAs(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	}
	h.ServeHTTP(rec, req)
	return rec
}

// Nothing personal is reachable without a link, and the refusal says nothing
// about who exists.
func TestEveryPersonalPageRefusesAnUnidentifiedRequest(t *testing.T) {
	srv, _, prog := testSite(t)
	if _, err := prog.SetStatus("two-sum", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	for _, path := range []string{"/", "/problems/", "/problems/two-sum", "/problems/two-sum/solve",
		"/patterns/", "/patterns/arrays-hashing", "/review/", "/coach/"} {
		rec := getAs(t, h, path, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without a cookie = %d, want 403", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "Two Sum") {
			t.Errorf("%s leaked problem data to an unidentified request", path)
		}
		if strings.Contains(rec.Body.String(), "owner") {
			t.Errorf("%s named a user to an unidentified request", path)
		}
	}
}

// A stylesheet is not personal and must not need a cookie, or the refusal page
// renders unstyled.
func TestStaticAssetsDoNotNeedALink(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	for _, path := range []string{"/static/app.css", "/highlight.css"} {
		if rec := getAs(t, h, path, ""); rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
	}
}

func TestJoiningSetsACookieAndKeepsTheTokenOutOfThePage(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	rec := getAs(t, h, "/join/"+testToken, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("join = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("join redirected to %q, want /", loc)
	}
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatal("join set no cookie")
	}
	if found.Value != testToken {
		t.Errorf("cookie carries %q", found.Value)
	}
	if !found.HttpOnly {
		t.Error("cookie is readable from page script")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", found.SameSite)
	}
	// The token must not survive in anything the browser renders or stores
	// beyond the cookie itself.
	if strings.Contains(rec.Body.String(), testToken) {
		t.Error("the join response body repeats the token")
	}
}

func TestAnUnknownOrRevokedLinkIsRefused(t *testing.T) {
	srv, _, prog := testSite(t)
	h := srv.Handler()
	if rec := getAs(t, h, "/join/not-a-real-token", ""); rec.Code != http.StatusForbidden {
		t.Errorf("unknown token = %d, want 403", rec.Code)
	}
	if rec := getAs(t, h, "/", testToken); rec.Code != http.StatusOK {
		t.Fatalf("owner = %d, want 200", rec.Code)
	}
	if err := prog.DB().SetUserToken(studydb.OwnerID, ""); err != nil {
		t.Fatal(err)
	}
	if rec := getAs(t, h, "/", testToken); rec.Code != http.StatusForbidden {
		t.Error("a revoked link still worked")
	}
}

// The point of the whole change: two people on one instance keep separate
// schedules.
func TestTwoPeopleSeeTheirOwnProgress(t *testing.T) {
	srv, _, prog := testSite(t)
	token, theirs := guest(t, srv, "dima")
	h := srv.Handler()

	if _, err := prog.SetStatus("two-sum", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := theirs.SetStatus("valid-palindrome", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}

	if got := prog.Get("valid-palindrome").Status; got != progress.New {
		t.Errorf("the guest's solve reached the owner: %s", got)
	}
	if got := theirs.Get("two-sum").Status; got != progress.New {
		t.Errorf("the owner's solve reached the guest: %s", got)
	}

	// The roadmap row carries both the problem id and the status, so this reads
	// each person's own view of the same two problems.
	owner := listedStatus(t, getAs(t, h, "/problems/", testToken).Body.String())
	theirView := listedStatus(t, getAs(t, h, "/problems/", token).Body.String())
	if owner["1"] != "solved" || owner["125"] == "solved" {
		t.Errorf("owner sees two-sum=%q valid-palindrome=%q", owner["1"], owner["125"])
	}
	if theirView["125"] != "solved" || theirView["1"] == "solved" {
		t.Errorf("guest sees two-sum=%q valid-palindrome=%q", theirView["1"], theirView["125"])
	}
}

var rowRE = regexp.MustCompile(`data-id="(\d+)"[^>]*data-status="([a-z]+)"`)

// listedStatus pulls each problem's rendered status off the roadmap rows.
func listedStatus(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range rowRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatal("no problem rows in the page")
	}
	return out
}

// Notes are rendered through the shared content store, which used to hold one
// database handle for the whole process.
func TestNotesAreNotSharedBetweenPeople(t *testing.T) {
	srv, _, prog := testSite(t)
	token, theirs := guest(t, srv, "dima")
	if err := prog.Scope().SaveDocument("two-sum", "notes", "OWNER_PRIVATE_NOTE"); err != nil {
		t.Fatal(err)
	}
	if err := theirs.Scope().SaveDocument("two-sum", "notes", "GUEST_NOTE"); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	guestPage := getAs(t, h, "/problems/two-sum", token).Body.String()
	if strings.Contains(guestPage, "OWNER_PRIVATE_NOTE") {
		t.Error("the guest was served the owner's notes")
	}
	if !strings.Contains(guestPage, "GUEST_NOTE") {
		t.Error("the guest was not served their own notes")
	}
}

// The sharpest leak this change closes: the DELETE behind a submission used to
// match on slug alone.
func TestOnePersonSubmittingLeavesAnothersPendingWorkAlone(t *testing.T) {
	srv, _, prog := testSite(t)
	_, theirs := guest(t, srv, "dima")

	if err := prog.Scope().SavePending("two-sum", []byte(`{"Code":"owner draft"}`)); err != nil {
		t.Fatal(err)
	}
	if err := theirs.Scope().SavePending("two-sum", []byte(`{"Code":"guest draft"}`)); err != nil {
		t.Fatal(err)
	}
	// Clearing the guest's must not touch the owner's.
	if err := theirs.Scope().Write(func(tx *sql.Tx) error {
		return theirs.Scope().DeletePendingIn(tx, "two-sum")
	}); err != nil {
		t.Fatal(err)
	}
	owner, err := prog.Scope().Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(owner) != 1 {
		t.Fatalf("the owner's pending work was destroyed: %v", owner)
	}
}

// The tutor keeps one process and one transcript per problem, so until that is
// per-user it stays the owner's.
func TestTheTutorIsNotHandedToGuests(t *testing.T) {
	srv, _, _ := testSite(t)
	token, _ := guest(t, srv, "dima")
	h := srv.Handler()
	if rec := getAs(t, h, "/problems/two-sum/tutor/events", token); rec.Code != http.StatusForbidden {
		t.Errorf("a guest reached the tutor stream: %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/problems/two-sum/tutor", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a guest posted to the tutor: %d", rec.Code)
	}
}

// Leaving is how a shared browser is handed back.
func TestLeavingDropsTheCookie(t *testing.T) {
	srv, _, _ := testSite(t)
	rec := getAs(t, srv.Handler(), "/leave", testToken)
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.MaxAge >= 0 {
			t.Errorf("leave did not expire the cookie: %+v", c)
		}
	}
}

// Two people on the same problem must not write over each other's Go. The
// owner keeps solutions/; everybody else gets _work/<id>/solutions/.
func TestTwoPeopleWritingTheSameProblemKeepSeparateFiles(t *testing.T) {
	srv, root, _ := testSite(t)
	token, _ := guest(t, srv, "dima")
	h := srv.Handler()

	for _, who := range []struct{ name, token, code string }{
		{"owner", testToken, "// OWNER CODE\n"},
		{"guest", token, "// GUEST CODE\n"},
	} {
		if rec := postJSONAs(t, h, "/problems/two-sum/start", map[string]string{}, who.token); rec.Code != http.StatusOK {
			t.Fatalf("%s start = %d: %s", who.name, rec.Code, rec.Body)
		}
		body := map[string]string{"code": "package twosum\n" + who.code}
		if rec := postJSONAs(t, h, "/problems/two-sum/solution", body, who.token); rec.Code != http.StatusOK {
			t.Fatalf("%s save = %d: %s", who.name, rec.Code, rec.Body)
		}
	}

	p, _ := srv.cat.BySlug("two-sum")
	ownerFile := workspace.Owner(root).Path(p, "solution.go")
	guestUser, err := srv.db.UserByToken(token)
	if err != nil {
		t.Fatal(err)
	}
	guestFile := workspace.For(root, guestUser.ID).Path(p, "solution.go")

	if ownerFile == guestFile {
		t.Fatal("both people were given the same file")
	}
	ownerCode, err := os.ReadFile(ownerFile)
	if err != nil {
		t.Fatalf("owner file: %v", err)
	}
	guestCode, err := os.ReadFile(guestFile)
	if err != nil {
		t.Fatalf("guest file: %v", err)
	}
	if !strings.Contains(string(ownerCode), "OWNER CODE") {
		t.Errorf("owner's file holds %q", ownerCode)
	}
	if !strings.Contains(string(guestCode), "GUEST CODE") {
		t.Errorf("guest's file holds %q", guestCode)
	}
	// And the guest's directory is the underscore one, which is what keeps it
	// out of ./... and therefore out of CI.
	if !strings.Contains(guestFile, string(filepath.Separator)+"_work"+string(filepath.Separator)) {
		t.Errorf("guest file %q is not under _work/", guestFile)
	}
}

// postJSONAs is postJSON for a chosen person.
func postJSONAs(t *testing.T, h http.Handler, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	h.ServeHTTP(rec, req)
	return rec
}
