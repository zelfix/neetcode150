package web

import (
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
	"neetcode150/internal/workspace"
)

// cookieName holds the join token. HttpOnly because no page script needs it,
// and SameSite=Lax so a link from a chat window still signs you in while a
// cross-site form post does not act as you.
const cookieName = "nc_session"

// cookieLife is deliberately long. This replaces a password, and a study
// session that logs you out between sittings would just get re-joined.
const cookieLife = 180 * 24 * time.Hour

// session is one request's view of the site: the shared server, plus who is
// asking and their own slice of the study data.
//
// The field is called progress so that everything reading personal data reads
// it from here. Server deliberately has no such field: that is what stops a
// handler from quietly serving the owner's work to a guest.
type session struct {
	*Server
	user     studydb.User
	progress *progress.Store
}

// signedIn resolves the signed-in user. It writes the response itself when there is
// nobody to serve, and reports whether the caller should continue.
//
// There is no exception for loopback or for a missing cookie. An exception
// would have to trust either the listening address or a forwarded-for header,
// and behind a reverse proxy every request looks local — so the failure mode is
// a guest who clears their cookie becoming the owner. One rule has no such
// mode: a cookie, or nothing.
func (s *Server) signedIn(w http.ResponseWriter, r *http.Request) (*session, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		s.askForALink(w, r)
		return nil, false
	}
	user, err := s.db.UserByToken(cookie.Value)
	if err != nil {
		// A revoked or unknown token is indistinguishable from none, and saying
		// which would turn the page into an oracle for guessing tokens.
		clearCookie(w, r)
		s.askForALink(w, r)
		return nil, false
	}
	return &session{Server: s, user: user, progress: progress.NewScoped(s.db.For(user.ID))}, true
}

// with adapts a session handler for the router. Method expressions keep the
// registration readable: s.with((*session).handleDashboard).
func (s *Server) with(h func(*session, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.signedIn(w, r)
		if !ok {
			return
		}
		h(v, w, r)
	}
}

// ownerOnly fences off what is not per-user yet. The tutor keeps one live
// process and one transcript per problem rather than per person, so handing it
// to a guest would put two people in one conversation.
func (s *Server) ownerOnly(h func(*session, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return s.with(func(v *session, w http.ResponseWriter, r *http.Request) {
		if v.user.ID != studydb.OwnerID {
			http.Error(w, "the tutor is not available on shared instances yet", http.StatusForbidden)
			return
		}
		h(v, w, r)
	})
}

func secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// handleJoin trades a token for a cookie and sends the browser to the site.
//
// The token leaves the URL immediately: a redirect means it does not stay in
// the address bar, in history, or in a screenshot of the page.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	user, err := s.db.UserByToken(token)
	if err != nil {
		s.askForALink(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  s.now().Add(cookieLife),
	})
	_ = user
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// handleLeave drops the cookie, which is how a shared browser is handed back.
func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, r)
	s.askForALink(w, r)
}

var joinPage = template.Must(template.New("join").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>neetcode150</title>
<link rel="stylesheet" href="/static/app.css">
</head><body class="join-page">
<main>
  <h1>You need your own link</h1>
  <p>This study kit keeps each person's progress separately, and the link is
  what identifies you. Opening it once in this browser is the whole sign-in.</p>
  <p class="muted">If you have lost it, ask for a new one — links cannot be
  looked up, only reissued.</p>
</main>
</body></html>
`))

// askForALink is the only thing an unidentified request ever sees. It carries
// no list of users and no hint about which tokens exist.
func (s *Server) askForALink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Not 401: there is no auth scheme to challenge with, and a browser would
	// put up a basic-auth dialog that nothing here can satisfy.
	w.WriteHeader(http.StatusForbidden)
	_ = joinPage.Execute(w, nil)
}

// key identifies this person's unfinished submission for one problem.
func (s *session) key(p catalog.Problem) submissionKey {
	return submissionKey{userID: s.user.ID, slug: p.Slug}
}

// slugKey is the same, where only the slug is to hand.
func (s *session) slugKey(slug string) submissionKey {
	return submissionKey{userID: s.user.ID, slug: slug}
}

// cacheDir is this person's own Go build cache. Per user rather than shared:
// build entries are content-addressed, but a writable cache shared between
// people is still a channel for substituting a compiled object into somebody
// else's build.
func (s *session) cacheDir() string {
	return filepath.Join(s.root, ".nc-cache", "users", strconv.FormatInt(s.user.ID, 10))
}

// runRequest describes a test run for this person and problem.
func (s *session) runRequest(p catalog.Problem) runRequest {
	return runRequest{
		Root:     s.root,
		PkgDir:   s.work().PkgDir(p),
		UserID:   s.user.ID,
		CacheDir: s.cacheDir(),
	}
}

// work is this person's workspace. The owner keeps solutions/; everybody else
// gets their own directory, so two people on one problem do not write over
// each other's Go.
func (s *session) work() workspace.Layout {
	return workspace.For(s.root, s.user.ID)
}
