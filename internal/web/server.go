// Package web serves the local study site: a reader for the problem Markdown
// plus the progress and review tracking around it.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/lang"
	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
	"neetcode150/internal/tutor"
)

//go:embed templates/*.html static/*
var assets embed.FS

// pageNames are the templates that each compose with the layout.
var pageNames = []string{"dashboard", "problems", "problem", "pattern", "review", "interview", "solve"}

// Server serves the site.
// submissionKey identifies one person's unfinished submission. Keyed by slug
// alone, two people on the same problem would overwrite each other's.
type submissionKey struct {
	userID int64
	slug   string
}

// Server serves the site. It holds nothing personal: a request's own view comes
// from view(), so there is no field a handler can read the owner's work from.
type Server struct {
	root    string
	cat     *catalog.Catalog
	content *content.Store
	db      *studydb.DB
	pages   map[string]*template.Template
	now     func() time.Time
	// pick draws an index below n. Injected the same way now is, so a drill
	// test gets a determined draw rather than seeding a global.
	pick func(n int) int

	// runTests is injected the same way now is, so handler tests do not shell
	// out to a real `go test` and inherit its speed and flakiness.
	runTests testRunner
	// unavailable says, per language, why tests cannot run on this server;
	// absent means they can. UseSandbox decides it once.
	unavailable map[lang.ID]string

	// pyFormatTool is the Python formatter found on this machine, or "" when
	// there is none. Go needs no such lookup: gofmt is go/format, compiled in.
	//
	// Python's is deliberately optional rather than required. The project takes
	// no dependency to format Python, so the key reports what is missing and
	// starts working the moment ruff is installed — nothing here breaks when it
	// is not. Resolved once at startup; pyFormat is injected the same way
	// runTests is, so tests need no binary.
	pyFormatTool string
	pyFormat     func(ctx context.Context, src string) (string, error)

	// tutor owns the claude child processes. It is never nil: a Manager with no
	// binary to run reports why through Available, and the rest of the site
	// does not care.
	tutor *tutor.Manager

	submissionMu       sync.Mutex
	pendingSubmissions map[submissionKey]pendingSubmission

	// interviewMu serialises every read-modify-write of an interview's payload.
	// The payload is one JSON document saved whole, and four things write it
	// without a request ordering them: Next, Finish, the time-up report, and
	// the interviewer's own decision arriving seconds after the submission. Two
	// of those interleaved lose one's write — the interview steps back onto a
	// problem it had closed. One lock for everybody is enough: a mutation is a
	// few statements, and the model turns run outside it.
	interviewMu sync.Mutex

	// background is the parent of work that outlives the request that started
	// it, such as a submission review, so that stopping the server stops it
	// too instead of leaving it to talk to a closed database.
	background context.Context
	stop       context.CancelFunc
}

// New builds a Server over the project at root.
func New(root string, cat *catalog.Catalog, store *content.Store, prog *progress.Store) (*Server, error) {
	s := &Server{
		root:               root,
		cat:                cat,
		content:            store,
		db:                 prog.DB(),
		pages:              map[string]*template.Template{},
		now:                time.Now,
		pick:               rand.IntN,
		pyFormatTool:       pyFormatterName(),
		runTests:           localRunner{},
		tutor:              tutor.New(root, tutor.ModeTutor, 0),
		pendingSubmissions: make(map[submissionKey]pendingSubmission),
	}
	s.background, s.stop = context.WithCancel(context.Background())
	s.tutor.OnSpend(s.recordTutorSpend)
	s.tutor.OnLanguage(s.languageOf)
	users, err := s.db.Users()
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		pending, err := s.db.For(u.ID).Pending()
		if err != nil {
			return nil, err
		}
		for slug, raw := range pending {
			var p pendingSubmission
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			// A worker cannot survive a process restart. Preserve the submitted
			// source and let the next user reply retry the assessment.
			wasChecking := p.Checking
			p.Checking = false
			s.pendingSubmissions[submissionKey{u.ID, slug}] = p
			if wasChecking && u.ID == studydb.OwnerID {
				if session := s.tutor.Get(s.db.For(u.ID), slug); session != nil {
					session.PublishSubmissionQuestion("The review was interrupted by a server restart. Your solution is saved; send ‘continue’ to resume the review.")
				}
			}
		}
	}
	for _, name := range pageNames {
		t, err := template.New("layout.html").Funcs(s.funcs()).ParseFS(assets,
			"templates/layout.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return s, nil
}

// UseTutor replaces the tutor manager, so the command line can choose the
// permission mode and budget without New growing more parameters.
func (s *Server) UseTutor(m *tutor.Manager) {
	if s.tutor != nil {
		s.tutor.Close()
	}
	m.OnSpend(s.recordTutorSpend)
	m.OnLanguage(s.languageOf)
	s.tutor = m
}

// languageOf is the resolution rule for the tutor, which has a scope rather
// than a session.
func (s *Server) languageOf(scope *studydb.Scope, slug string) lang.Lang {
	return lang.Resolve(progress.NewScoped(scope).Get(slug).Lang)
}

// CloseTutor ends every tutor session. The server must call this on the way
// down or the claude child processes outlive it.
func (s *Server) CloseTutor() {
	s.stop()
	if s.tutor != nil {
		s.tutor.Close()
	}
}

// Handler returns the site's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err) // the embedded tree is fixed at build time
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /highlight.css", s.handleHighlightCSS)

	// A probe for the reverse proxy or a monitor. It answers without a cookie
	// and says nothing about who exists or what is stored.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})

	// Trading a link for a cookie is the only route that runs without an
	// identity, and leaving is the only one that deliberately drops it. Leaving
	// is a POST behind guard: a Lax cookie still rides along on a top-level GET,
	// so a GET that signed you out could be triggered by a link on any site.
	mux.HandleFunc("GET /join/{token}", s.handleJoin)
	// The two routes a person without a cookie may reach. Redeeming writes, so
	// it is behind guard — same-origin, but deliberately not signed-in.
	mux.HandleFunc("GET /invite/{token}", s.handleInvite)
	mux.HandleFunc("POST /invite", guard(s.handleInviteRedeem))
	mux.HandleFunc("GET /leave", s.handleLeavePage)
	mux.HandleFunc("POST /leave", guard(s.handleLeave))

	// Everything below is somebody's own work, so it all goes through with(),
	// which refuses a request that names nobody.
	mux.HandleFunc("GET /{$}", s.with((*session).handleDashboard))
	mux.HandleFunc("GET /problems/{$}", s.with((*session).handleProblems))
	mux.HandleFunc("GET /problems/{slug}", s.with((*session).handleProblem))
	mux.HandleFunc("GET /problems/{slug}/solve", s.with((*session).handleSolve))
	mux.HandleFunc("GET /patterns/{slug}", s.with((*session).handlePattern))
	mux.HandleFunc("GET /review/{$}", s.with((*session).handleReview))
	// The interview needs an interviewer, so every one of these sits behind the
	// same gate as the panel: on a provider that can read the server, they are
	// the owner's alone.
	mux.HandleFunc("GET /interview/{$}", s.tutorGate((*session).handleInterview))
	// A literal segment beats the wildcard below it, so /interview/state is the
	// poll and /interview/4 is a report.
	mux.HandleFunc("GET /interview/state", s.tutorGate((*session).handleInterviewState))
	mux.HandleFunc("GET /interview/{id}", s.tutorGate((*session).handleInterviewReport))
	mux.HandleFunc("POST /interview/start", guard(s.tutorGate((*session).handleInterviewStart)))
	mux.HandleFunc("POST /interview/next", guard(s.tutorGate((*session).handleInterviewNext)))
	mux.HandleFunc("POST /interview/finish", guard(s.tutorGate((*session).handleInterviewFinish)))
	mux.HandleFunc("POST /interview/timeup", guardJSON(s.tutorGate((*session).handleInterviewTimeUp)))
	// A person's own default language: it changes one row, their own, and
	// mints nothing, so it is not an admin surface.
	mux.HandleFunc("POST /problems/{slug}/status", guard(s.with((*session).handleSetStatus)))
	mux.HandleFunc("POST /problems/{slug}/rate", guard(s.with((*session).handleRate)))
	mux.HandleFunc("POST /problems/{slug}/time", guard(s.with((*session).handleAddTime)))
	// These write files and run code, so they carry the stricter guard.
	mux.HandleFunc("POST /problems/{slug}/create", guardJSON(s.with((*session).handleCreateSolution)))
	mux.HandleFunc("POST /problems/{slug}/start", guardJSON(s.with((*session).handleStartAttempt)))
	mux.HandleFunc("POST /problems/{slug}/cancel", guardJSON(s.with((*session).handleCancelAttempt)))
	mux.HandleFunc("POST /problems/{slug}/solution", guardJSON(s.with((*session).handleSaveSolution)))
	mux.HandleFunc("POST /problems/{slug}/notes", guardJSON(s.with((*session).handleSaveNotes)))
	mux.HandleFunc("POST /problems/{slug}/format", guardJSON(s.with((*session).handleFormatSolution)))
	mux.HandleFunc("POST /problems/{slug}/test", guardJSON(s.with((*session).handleRunTests)))
	mux.HandleFunc("POST /problems/{slug}/submit", guardJSON(s.with((*session).handleSubmit)))
	mux.HandleFunc("POST /problems/{slug}/resolve", guardJSON(s.with((*session).handleResolve)))
	// Sessions are per person now. What still restricts the panel is whether
	// the provider can read the server's files — see tutorGate.
	mux.HandleFunc("GET /problems/{slug}/tutor/events", guardStream(s.tutorGate((*session).handleTutorEvents)))
	mux.HandleFunc("POST /problems/{slug}/tutor", guardJSON(s.tutorGate((*session).handleTutorSend)))
	mux.HandleFunc("POST /problems/{slug}/tutor/reset", guardJSON(s.tutorGate((*session).handleTutorReset)))
	mux.HandleFunc("POST /problems/{slug}/tutor/interrupt", guardJSON(s.tutorGate((*session).handleTutorInterrupt)))

	// Accept the paths without the trailing slash too.
	for _, p := range []string{"/problems", "/review", "/interview"} {
		target := p + "/"
		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})
	}
	// /patterns/ and /coach/ are absent rather than redirected: both were
	// merged into other pages, and the site is not published, so there is no
	// link to keep working. `/patterns/<slug>` above is the guide, unaffected.
	return instrument(mux)
}

func (s *Server) handleHighlightCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	fmt.Fprint(w, s.content.Renderer().HighlightCSS())
}

// ---------- view models ----------

// common is the data every page needs for the shell.
type common struct {
	Title   string
	Nav     string
	Summary progress.Summary
	Due     int
	// Wide widens the main column. The workspace runs two live panes side by
	// side, which does not fit the measure a page of prose wants.
	Wide bool
	// Full hands the viewport to the page. The workspace pins a chat input and
	// a button row to the bottom of their panes, which only reads as one screen
	// if the page itself does not scroll.
	Full bool
}

func (s *session) common(title, nav string) common {
	now := s.now()
	sum := s.progress.Summarize(s.cat.Problems, now)
	return common{Title: title, Nav: nav, Summary: sum, Due: sum.DueNow}
}

func (s *session) wideCommon(title, nav string) common {
	c := s.common(title, nav)
	c.Wide = true
	return c
}

func (s *session) fullCommon(title, nav string) common {
	c := s.wideCommon(title, nav)
	c.Full = true
	return c
}

// problemView is a problem plus your state for it.
type problemView struct {
	catalog.Problem
	Entry       progress.Entry
	IsDue       bool
	DaysOverdue int
}

func (s *session) view(p catalog.Problem, now time.Time) problemView {
	e := s.progress.Get(p.Slug)
	v := problemView{Problem: p, Entry: e, IsDue: e.Due(now)}
	if v.IsDue {
		v.DaysOverdue = int(now.Sub(*e.NextReview).Hours() / 24)
	}
	return v
}

func (s *session) views(problems []catalog.Problem, now time.Time) []problemView {
	out := make([]problemView, 0, len(problems))
	for _, p := range problems {
		out = append(out, s.view(p, now))
	}
	return out
}

// groupView is one pattern with its problems and its own progress.
type groupView struct {
	Pattern  string
	Slug     string
	Problems []problemView
	Summary  progress.Summary
	HasGuide bool
}

func (s *session) groups(now time.Time) []groupView {
	out := make([]groupView, 0, len(catalog.PatternOrder))
	for _, g := range s.cat.Groups() {
		out = append(out, groupView{
			Pattern:  g.Pattern,
			Slug:     g.Slug,
			Problems: s.views(g.Problems, now),
			Summary:  s.progress.Summarize(g.Problems, now),
			HasGuide: s.hasGuide(g.Slug),
		})
	}
	return out
}

func (s *Server) hasGuide(patternSlug string) bool {
	doc, err := s.content.Doc(nil, filepath.Join("content", "patterns", patternSlug+".md"))
	return err == nil && !doc.Empty()
}

// ---------- pages ----------

type problemsPage struct {
	common
	Groups     []groupView
	Difficulty []string
	Statuses   []progress.Status
}

func (s *session) handleProblems(w http.ResponseWriter, r *http.Request) {
	s.render(w, "problems", problemsPage{
		common:     s.common("All problems", "problems"),
		Groups:     s.groups(s.now()),
		Difficulty: []string{"Easy", "Medium", "Hard"},
		Statuses:   progress.Statuses,
	})
}

// workAction is the label on the button that leads into the workspace.
//
// The verb describes the workspace state: Start begins a cold attempt,
// Continue returns to an active timer, and Open workspace opens an inactive
// page where an earlier solution is kept behind a reveal.
func workAction(v problemView, hasSolution bool) string {
	switch {
	case !hasSolution:
		return "Start"
	case v.Entry.Status == progress.Attempted && v.Entry.AttemptStartedAt != nil:
		return "Continue"
	case v.Entry.Status == progress.Attempted:
		return "Start"
	default:
		return "Open workspace"
	}
}

type problemPage struct {
	common
	Action         string
	P              problemView
	Statement      *content.Doc
	HintPreamble   template.HTML
	Hints          []content.Hint
	Notes          *content.Doc
	SolutionPath   string
	SolutionExists bool
	HasGuide       bool
	Prev           *catalog.Problem
	Next           *catalog.Problem
	Statuses       []progress.Status
	Topics         []string
	IntervalDays   int
	Record         recordView
}

func (s *session) handleProblem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	now := s.now()
	v := s.view(p, now)

	statement, err := s.content.Doc(nil, filepath.Join(p.ContentDir(), "problem.md"))
	if err != nil {
		s.fail(w, err)
		return
	}
	preamble, hints, err := s.content.Hints(s.progress.Scope(), filepath.Join(p.ContentDir(), "hints.md"))
	if err != nil {
		s.fail(w, err)
		return
	}
	notes, err := s.content.Doc(s.progress.Scope(), filepath.Join(p.ContentDir(), "notes.md"))
	if err != nil {
		s.fail(w, err)
		return
	}

	solution := s.work(p).Rel(p, s.language(p).SolutionFile)
	_, statErr := os.Stat(filepath.Join(s.root, solution))

	page := problemPage{
		common:         s.common(fmt.Sprintf("%d. %s", p.ID, p.Title), "problems"),
		Action:         workAction(v, statErr == nil),
		P:              v,
		Statement:      statement,
		HintPreamble:   preamble,
		Hints:          hints,
		Notes:          notes,
		SolutionPath:   solution,
		SolutionExists: statErr == nil,
		HasGuide:       s.hasGuide(p.PatternSlug),
		Statuses:       progress.Statuses,
		Topics:         p.Topics,
		IntervalDays:   v.Entry.IntervalDays(),
		Record:         s.record(p, v, now),
	}
	if p.Order > 0 {
		page.Prev = &s.cat.Problems[p.Order-1]
	}
	if p.Order+1 < len(s.cat.Problems) {
		page.Next = &s.cat.Problems[p.Order+1]
	}
	s.render(w, "problem", page)
}

type patternPage struct {
	common
	Group groupView
	Guide *content.Doc
}

func (s *session) handlePattern(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	g, ok := s.cat.Group(slug)
	if !ok {
		s.notFound(w, r, "No pattern with that slug.")
		return
	}
	now := s.now()
	guide, err := s.content.Doc(nil, filepath.Join("content", "patterns", slug+".md"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "pattern", patternPage{
		common: s.common(g.Pattern, "patterns"),
		Group: groupView{
			Pattern:  g.Pattern,
			Slug:     g.Slug,
			Problems: s.views(g.Problems, now),
			Summary:  s.progress.Summarize(g.Problems, now),
			HasGuide: !guide.Empty(),
		},
		Guide: guide,
	})
}

type reviewPage struct {
	common
	DueProblems []problemView
	Upcoming    []problemView
}

func (s *session) handleReview(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	page := reviewPage{
		common:      s.common("Review queue", "review"),
		DueProblems: s.views(s.progress.DueReview(s.cat.Problems, now), now),
	}
	for _, p := range s.cat.Problems {
		v := s.view(p, now)
		if v.Entry.NextReview != nil && !v.IsDue {
			page.Upcoming = append(page.Upcoming, v)
		}
	}
	sort.SliceStable(page.Upcoming, func(i, j int) bool {
		return page.Upcoming[i].Entry.NextReview.Before(*page.Upcoming[j].Entry.NextReview)
	})
	page.Upcoming = page.Upcoming[:min(len(page.Upcoming), 12)]
	s.render(w, "review", page)
}

// ---------- mutations ----------

func (s *session) handleSetStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	if !limitForm(w, r) {
		return
	}
	status := progress.Status(r.FormValue("status"))
	if !status.Valid() {
		http.Error(w, fmt.Sprintf("unknown status %q", r.FormValue("status")), http.StatusBadRequest)
		return
	}
	if _, err := s.progress.SetStatus(p.Slug, status, s.now()); err != nil {
		s.fail(w, err)
		return
	}
	s.redirectBack(w, r, p)
}

// rateOutcome is one button on the rate form: how much help the sitting needed,
// in words, and the rating it records.
//
// The form used to ask for a 1-5 rating and a hint rung separately. The tutor
// still reports both, because it has the transcript to judge them from, but
// asking a person for two numbers — one of them named "rung" — produced pairs
// that did not mean much. Each button now sets both.
//
// There are four because applyRating does four things: reset, lapse, hold,
// advance. The first two record a rung the scheduler ignores (it gates only
// ratings of 3 and up); it is kept because the dashboard and `nc weak` read
// the rungs to report how much help a problem needs.
type rateOutcome struct {
	Key   string
	Label string
	Title string

	confidence, rung int
}

var rateOutcomes = []rateOutcome{
	{"answer", "needed the answer", "Shown the solution — back to the start of the ladder", 1, 5},
	{"hint", "needed a hint", "Got there on a nudge — falls two stages", 2, 3},
	{"effort", "got there with effort", "Unaided, with work — holds its place", 3, 0},
	{"easy", "got there easily", "Unaided and quick — the interval widens", 5, 0},
}

func rateOutcomeFor(key string) (rateOutcome, bool) {
	for _, o := range rateOutcomes {
		if o.Key == key {
			return o, true
		}
	}
	return rateOutcome{}, false
}

func (s *session) handleRate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	if !limitForm(w, r) {
		return
	}
	now := s.now()

	// Undo takes no rating: it drops the last one and replays the schedule.
	// Amending one is deliberately not offered here — correcting a rating by
	// hand means naming a confidence and a rung, which is the thing this form
	// no longer asks for. Undo and submit again, or use `nc rate --amend`.
	if r.FormValue("action") == "undo" {
		if _, _, _, err := s.progress.Undo(p.Slug, now); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.redirectBack(w, r, p)
		return
	}

	outcome, ok := rateOutcomeFor(r.FormValue("outcome"))
	if !ok {
		http.Error(w, "outcome must be one of answer, hint, effort, easy", http.StatusBadRequest)
		return
	}

	// A rating ends the sitting, so the clock has to be banked before it is
	// cleared. `nc rate` has always done this; the form used to drop it, which
	// made a problem rated from the reading page look like it took no time.
	elapsed, running := s.progress.Get(p.Slug).Elapsed(now)
	if minutes, _ := progress.ClockMinutes(elapsed); running && minutes > 0 {
		if _, err := s.progress.AddMinutes(p.Slug, minutes, now); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if _, err := s.progress.RateHinted(p.Slug, outcome.confidence, outcome.rung, now); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.redirectBack(w, r, p)
}

// maxLoggedMinutes bounds one hand-entered sitting. AddMinutes itself rejects
// only negatives, and a mistyped 6000 lands in the median that Weakest sorts on
// and the workspace reports — the same figure the clock cap protects.
const maxLoggedMinutes = 24 * 60

// handleAddTime records a sitting the clock missed, for a problem worked
// through somewhere other than the workspace.
func (s *session) handleAddTime(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	if !limitForm(w, r) {
		return
	}
	n, err := strconv.Atoi(r.FormValue("minutes"))
	if err != nil || n < 1 || n > maxLoggedMinutes {
		http.Error(w, fmt.Sprintf("minutes must be a number from 1 to %d", maxLoggedMinutes), http.StatusBadRequest)
		return
	}
	if _, err := s.progress.AddMinutes(p.Slug, n, s.now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.redirectBack(w, r, p)
}

// redirectBack returns to the page the form was submitted from, so rating a
// problem from the review queue leaves you in the review queue.
func (s *Server) redirectBack(w http.ResponseWriter, r *http.Request, p catalog.Problem) {
	target := "/problems/" + p.Slug
	if from := r.FormValue("from"); strings.HasPrefix(from, "/") && !strings.HasPrefix(from, "//") {
		target = from
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// ---------- plumbing ----------

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t, ok := s.pages[name]
	if !ok {
		s.fail(w, fmt.Errorf("no template %q", name))
		return
	}
	// Render to a buffer so a template error does not emit a half page.
	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, buf.String())
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, msg string) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "404 — %s\n", msg)
}

// fail logs the cause and tells the browser only that there was one. On a
// shared instance the detail is a file path or a SQL error, which is the
// operator's to read, not a guest's.
func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// ---------- template helpers ----------

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"lower":   strings.ToLower,
		"slugify": catalog.Slugify,
		"statusClass": func(st progress.Status) string {
			if st == "" {
				return "new"
			}
			return string(st)
		},
		"statusLabel": func(st progress.Status) string { return st.Label() },
		// The buttons are rendered from the same table the handler reads, so a
		// label can never describe a different rating from the one it submits.
		"rateOutcomes": func() []rateOutcome { return rateOutcomes },
		"pct":          func(sum progress.Summary) int { return sum.Percent() },
		// Both shapes turn up: schedule fields are optional and so are
		// pointers, while a recorded rating always has a date.
		"date": func(v any) string {
			switch t := v.(type) {
			case time.Time:
				return t.Format("2 Jan 2006")
			case *time.Time:
				if t == nil {
					return "—"
				}
				return t.Format("2 Jan 2006")
			}
			return "—"
		},
		"ago": func(t *time.Time) string {
			if t == nil {
				return "never"
			}
			return humanizeDuration(s.now().Sub(*t), "ago")
		},
		"until": func(t *time.Time) string {
			if t == nil {
				return "not scheduled"
			}
			d := t.Sub(s.now())
			if d <= 0 {
				return "due now"
			}
			return "in " + humanizeDuration(d, "")
		},
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return fmt.Sprintf("%d %s", n, one)
			}
			return fmt.Sprintf("%d %s", n, many)
		},
		"add1":      func(i int) int { return i + 1 },
		"diffClass": diffClassOf,
		"diffSign":  diffSignOf,
		// dict builds a map so a partial can be given more than one value.
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				key, ok := kv[i].(string)
				if !ok {
					continue
				}
				m[key] = kv[i+1]
			}
			return m
		},
	}
}

// humanizeDuration renders a duration at day granularity, which is the unit the
// review schedule works in.
func humanizeDuration(d time.Duration, suffix string) string {
	if d < 0 {
		d = -d
	}
	var out string
	switch days := int(d.Hours() / 24); {
	case d < time.Hour:
		out = "under an hour"
	case days == 0:
		out = fmt.Sprintf("%dh", int(d.Hours()))
	case days == 1:
		out = "1 day"
	case days < 30:
		out = fmt.Sprintf("%d days", days)
	case days < 365:
		out = fmt.Sprintf("%d months", max(1, days/30))
	default:
		out = fmt.Sprintf("%d years", max(1, days/365))
	}
	if suffix != "" {
		return out + " " + suffix
	}
	return out
}

// Listen serves the site on addr until ctx is cancelled.
//
// The context is what makes an orderly stop possible: the site now owns child
// processes, and killing the server without ending them would leave claude
// running with nothing attached to it.
func Listen(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		// Bodies are small (maxBody) but a client may send one slowly, and an
		// idle keep-alive connection otherwise costs a goroutine for ever. There
		// is no WriteTimeout: the tutor's event stream is meant to stay open,
		// and a server-wide write deadline would cut it at the limit. The
		// stream handler also clears the read deadline for the same reason.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		// A short grace period: an open event stream should be let go of, not
		// waited on, since it only ends when the browser does.
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(stop)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
