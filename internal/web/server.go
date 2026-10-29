// Package web serves the local study site: a reader for the problem Markdown
// plus the progress and review tracking around it.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/progress"
)

//go:embed templates/*.html static/*
var assets embed.FS

// pageNames are the templates that each compose with the layout.
var pageNames = []string{"dashboard", "problems", "problem", "patterns", "pattern", "review"}

// Server serves the site.
type Server struct {
	root     string
	cat      *catalog.Catalog
	content  *content.Store
	progress *progress.Store
	pages    map[string]*template.Template
	now      func() time.Time
}

// New builds a Server over the project at root.
func New(root string, cat *catalog.Catalog, store *content.Store, prog *progress.Store) (*Server, error) {
	s := &Server{
		root:     root,
		cat:      cat,
		content:  store,
		progress: prog,
		pages:    map[string]*template.Template{},
		now:      time.Now,
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

// Handler returns the site's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err) // the embedded tree is fixed at build time
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /highlight.css", s.handleHighlightCSS)

	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /problems/{$}", s.handleProblems)
	mux.HandleFunc("GET /problems/{slug}", s.handleProblem)
	mux.HandleFunc("GET /patterns/{$}", s.handlePatterns)
	mux.HandleFunc("GET /patterns/{slug}", s.handlePattern)
	mux.HandleFunc("GET /review/{$}", s.handleReview)
	mux.HandleFunc("POST /problems/{slug}/status", s.handleSetStatus)
	mux.HandleFunc("POST /problems/{slug}/rate", s.handleRate)

	// Accept the paths without the trailing slash too.
	for _, p := range []string{"/problems", "/patterns", "/review"} {
		target := p + "/"
		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})
	}
	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
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
}

func (s *Server) common(title, nav string) common {
	now := s.now()
	sum := s.progress.Summarize(s.cat.Problems, now)
	return common{Title: title, Nav: nav, Summary: sum, Due: sum.DueNow}
}

// problemView is a problem plus your state for it.
type problemView struct {
	catalog.Problem
	Entry       progress.Entry
	IsDue       bool
	DaysOverdue int
}

func (s *Server) view(p catalog.Problem, now time.Time) problemView {
	e := s.progress.Get(p.Slug)
	v := problemView{Problem: p, Entry: e, IsDue: e.Due(now)}
	if v.IsDue {
		v.DaysOverdue = int(now.Sub(*e.NextReview).Hours() / 24)
	}
	return v
}

func (s *Server) views(problems []catalog.Problem, now time.Time) []problemView {
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

func (s *Server) groups(now time.Time) []groupView {
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
	doc, err := s.content.Doc(filepath.Join("content", "patterns", patternSlug+".md"))
	return err == nil && !doc.Empty()
}

// ---------- pages ----------

type dashboardPage struct {
	common
	Groups     []groupView
	Next       *problemView
	NextReason string
	DueSoon    []problemView
	Recent     []problemView
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	page := dashboardPage{
		common: s.common("Dashboard", "dashboard"),
		Groups: s.groups(now),
	}
	if p, reason, ok := s.progress.Next(s.cat.Problems, now); ok {
		v := s.view(p, now)
		page.Next, page.NextReason = &v, reason
	}
	due := s.views(s.progress.DueReview(s.cat.Problems, now), now)
	page.DueSoon = due[:min(len(due), 5)]
	page.Recent = s.recent(now, 6)
	s.render(w, "dashboard", page)
}

// recent lists the problems you touched most recently.
func (s *Server) recent(now time.Time, limit int) []problemView {
	var touched []problemView
	for _, p := range s.cat.Problems {
		v := s.view(p, now)
		if v.Entry.LastTouched != nil {
			touched = append(touched, v)
		}
	}
	sort.SliceStable(touched, func(i, j int) bool {
		return touched[i].Entry.LastTouched.After(*touched[j].Entry.LastTouched)
	})
	return touched[:min(len(touched), limit)]
}

type problemsPage struct {
	common
	Groups     []groupView
	Difficulty []string
	Statuses   []progress.Status
}

func (s *Server) handleProblems(w http.ResponseWriter, r *http.Request) {
	s.render(w, "problems", problemsPage{
		common:     s.common("All problems", "problems"),
		Groups:     s.groups(s.now()),
		Difficulty: []string{"Easy", "Medium", "Hard"},
		Statuses:   progress.Statuses,
	})
}

type problemPage struct {
	common
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
}

func (s *Server) handleProblem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	now := s.now()
	v := s.view(p, now)

	statement, err := s.content.Doc(filepath.Join(p.ContentDir(), "problem.md"))
	if err != nil {
		s.fail(w, err)
		return
	}
	preamble, hints, err := s.content.Hints(filepath.Join(p.ContentDir(), "hints.md"))
	if err != nil {
		s.fail(w, err)
		return
	}
	notes, err := s.content.Doc(filepath.Join(p.ContentDir(), "notes.md"))
	if err != nil {
		s.fail(w, err)
		return
	}

	solution := filepath.Join(p.SolutionDir(), "solution.go")
	_, statErr := os.Stat(filepath.Join(s.root, solution))

	page := problemPage{
		common:         s.common(fmt.Sprintf("%d. %s", p.ID, p.Title), "problems"),
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
		IntervalDays:   progress.IntervalDays(v.Entry.ReviewStage),
	}
	if p.Order > 0 {
		page.Prev = &s.cat.Problems[p.Order-1]
	}
	if p.Order+1 < len(s.cat.Problems) {
		page.Next = &s.cat.Problems[p.Order+1]
	}
	s.render(w, "problem", page)
}

type patternsPage struct {
	common
	Groups []groupView
}

func (s *Server) handlePatterns(w http.ResponseWriter, r *http.Request) {
	s.render(w, "patterns", patternsPage{
		common: s.common("Patterns", "patterns"),
		Groups: s.groups(s.now()),
	})
}

type patternPage struct {
	common
	Group groupView
	Guide *content.Doc
}

func (s *Server) handlePattern(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	g, ok := s.cat.Group(slug)
	if !ok {
		s.notFound(w, r, "No pattern with that slug.")
		return
	}
	now := s.now()
	guide, err := s.content.Doc(filepath.Join("content", "patterns", slug+".md"))
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
	Due      []problemView
	Upcoming []problemView
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	page := reviewPage{
		common: s.common("Review queue", "review"),
		Due:    s.views(s.progress.DueReview(s.cat.Problems, now), now),
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

func (s *Server) handleSetStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
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

func (s *Server) handleRate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	n, err := strconv.Atoi(r.FormValue("confidence"))
	if err != nil {
		http.Error(w, "confidence must be a number from 1 to 5", http.StatusBadRequest)
		return
	}
	if _, err := s.progress.Rate(p.Slug, n, s.now()); err != nil {
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

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
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
		"pct":         func(sum progress.Summary) int { return sum.Percent() },
		"date": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.Format("2 Jan 2006")
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

// Listen serves the site on addr until the process is stopped.
func Listen(addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
