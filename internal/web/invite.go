package web

import (
	"crypto/rand"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strings"

	"neetcode150/internal/studydb"
)

// maxNameLen bounds a self-chosen name. It is a label in a table, not prose.
const maxNameLen = 32

// invitePage asks for the one thing the server cannot invent: what to call
// this person.
//
// The token travels in a hidden field rather than in the form's action, so the
// POST's URL carries no secret. Referrer-Policy covers the GET that put this
// page on screen, whose URL does.
var invitePage = template.Must(template.New("invite").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>neetcode150</title>
<link rel="stylesheet" href="/static/app.css">
</head><body class="join-page">
<main>
  <h1>Pick a name</h1>
  <p>This is how your work is kept apart from everybody else's. It is a label,
  not an account: no password, and no email.</p>
  {{if .Err}}<p class="muted">{{.Err}}</p>{{end}}
  <form method="post" action="/invite">
    <input type="hidden" name="token" value="{{.Token}}">
    <input name="name" maxlength="32" required autofocus
      placeholder="dima" aria-label="Your name" value="{{.Name}}">
    <button type="submit">Join</button>
  </form>
  <p class="muted">A name is suggested so you need not invent one; change it to
  anything free. You get your own link afterwards — keep it, it is what signs
  you in from then on, and it cannot be looked up.</p>
</main>
</body></html>
`))

type inviteView struct {
	Token string
	Name  string
	Err   string
}

// noReferrer keeps a URL that contains a secret out of the next request's
// Referer header. The invite's own page is reached by a URL holding the token,
// so this is not decoration.
func noReferrer(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// handleInvite shows the name form for a live invite.
//
// Unknown, expired and spent all answer with the same refusal the site gives
// anybody without a link, so this endpoint says nothing about which invites
// exist.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if _, err := s.db.InviteByToken(token, s.now()); err != nil {
		noReferrer(w)
		s.askForALink(w, r)
		return
	}
	noReferrer(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Pre-filled, so joining is one click and nobody has to decide on the spot
	// how much of their real name to give a study group. It is editable.
	_ = invitePage.Execute(w, inviteView{Token: token, Name: s.freeSuggestion()})
}

// handleInviteRedeem spends one use and signs the newcomer in.
//
// Behind guard, which checks the request came from this site's own page and —
// unlike the rest of the writing endpoints — does not require a cookie: the
// whole point is that this person does not have one yet.
func (s *Server) handleInviteRedeem(w http.ResponseWriter, r *http.Request) {
	if !limitForm(w, r) {
		return
	}
	noReferrer(w)
	token := r.FormValue("token")
	name := strings.TrimSpace(r.FormValue("name"))

	// Checked before anything is written, so a bad name does not spend a use.
	if _, err := s.db.InviteByToken(token, s.now()); err != nil {
		s.askForALink(w, r)
		return
	}
	again := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		shown := name
		if shown == "" {
			shown = s.freeSuggestion()
		}
		_ = invitePage.Execute(w, inviteView{Token: token, Name: shown, Err: msg})
	}
	switch {
	case name == "":
		again("A name is needed — it is what keeps your work yours.")
		return
	case len([]rune(name)) > maxNameLen:
		again("That name is too long; 32 characters at most.")
		return
	case strings.ContainsAny(name, "\n\r\t"):
		again("A name is one line.")
		return
	}

	// The newcomer's own token: minted here, where the randomness is, and
	// stored only as a hash, exactly as the CLI does it for a named person.
	personal := rand.Text()
	user, err := s.db.RedeemInvite(token, name, studydb.HashToken(personal), s.now())
	switch {
	case errors.Is(err, studydb.ErrNoInvite):
		// Someone took the last use between the check above and this write.
		s.askForALink(w, r)
		return
	case errors.Is(err, studydb.ErrNameTaken):
		again("That name is taken. Try another.")
		return
	case err != nil:
		// Anything else is ours, not theirs, and saying "that name is taken"
		// about a database fault would send them in circles.
		log.Printf("invite redemption failed: %v", err)
		http.Error(w, "something went wrong creating that account", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    personal,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  s.now().Add(cookieLife),
	})
	log.Printf("invite redeemed: %s is user %d", user.Name, user.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
