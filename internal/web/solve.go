package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/progress"
	"neetcode150/internal/scaffold"
)

// maxBody caps a request body. A solution is a few kilobytes; this only exists
// so a malformed or hostile request cannot make the server read without bound.
const maxBody = 1 << 20 // 1 MiB

type solvePage struct {
	common
	P              problemView
	Statement      *content.Doc
	HintPreamble   template.HTML
	Hints          []content.Hint
	Code           string
	TestCode       string
	SolutionExists bool
	Reviewing      bool
	CanResolve     bool
	SolutionPath   string
	TestPath       string
	Archived       int
	Statuses       []progress.Status
}

// handleSolve renders the workspace: the statement on one side, an editor and
// the test output on the other.
func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}

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

	code, exists := s.readSolution(p)
	testCode, _ := readIfPresent(filepath.Join(s.root, p.SolutionDir(), "solution_test.go"))
	entry := s.progress.Get(p.Slug)

	s.render(w, "solve", solvePage{
		common:         s.wideCommon(fmt.Sprintf("%d. %s", p.ID, p.Title), "problems"),
		P:              s.view(p, s.now()),
		Statement:      statement,
		HintPreamble:   preamble,
		Hints:          hints,
		Code:           code,
		TestCode:       testCode,
		SolutionExists: exists,
		// A due problem opened with its old solution on screen is not the cold
		// re-solve the review flow asks for, so the page offers to fix that.
		Reviewing: exists && s.view(p, s.now()).IsDue,
		// Re-solving something still in progress would archive a half-written
		// attempt and hand back a blank file, which is only ever a mistake.
		CanResolve:   exists && entry.Status != progress.Attempted,
		SolutionPath: filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution.go")),
		TestPath:     filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution_test.go")),
		Archived:     s.countArchived(p),
		Statuses:     progress.Statuses,
	})
}

// ---------- the JSON API behind the editor ----------

type saveRequest struct {
	Code string `json:"code"`
}

type saveResponse struct {
	Code        string `json:"code"`                  // as stored, gofmt'd when it parsed
	FormatError string `json:"formatError,omitempty"` // why it was not formatted
}

type testResponse struct {
	saveResponse
	Output string `json:"output"`
	Passed bool   `json:"passed"`
	Millis int64  `json:"millis"`
	RunErr string `json:"runError,omitempty"`
}

type createResponse struct {
	Code     string   `json:"code"`
	TestCode string   `json:"testCode"`
	Created  []string `json:"created"`
}

// handleCreateSolution scaffolds the solution files, exactly as `nc start` does.
func (s *Server) handleCreateSolution(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}

	signature, _ := readIfPresent(filepath.Join(s.root, p.ContentDir(), "signature.txt"))
	raw, _ := readIfPresent(filepath.Join(s.root, p.ContentDir(), "problem.md"))
	_, statement := content.SplitFrontmatter(raw)

	written, _, err := scaffold.Create(s.root, p, signature, statement)
	if err != nil {
		s.fail(w, err)
		return
	}

	now := s.now()
	if _, err := s.progress.SetStatus(p.Slug, progress.Attempted, now); err != nil {
		s.fail(w, err)
		return
	}
	// Opening a fresh scaffold is the start of a sitting, so the clock runs from
	// here and `nc rate` logs it without anyone having to remember.
	if _, err := s.progress.BeginTimer(p.Slug, now); err != nil {
		s.fail(w, err)
		return
	}

	code, _ := s.readSolution(p)
	testCode, _ := readIfPresent(filepath.Join(s.root, p.SolutionDir(), "solution_test.go"))
	rel := make([]string, 0, len(written))
	for _, f := range written {
		if r, err := filepath.Rel(s.root, f); err == nil {
			rel = append(rel, filepath.ToSlash(r))
		}
	}
	writeJSON(w, http.StatusOK, createResponse{Code: code, TestCode: testCode, Created: rel})
}

// handleSaveSolution stores the editor's contents.
func (s *Server) handleSaveSolution(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	req, err := decodeBody[saveRequest](r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// An absent or blank field would otherwise truncate the file. Saving an
	// empty solution is never what anyone means, and the cost of being wrong
	// about that is somebody's work.
	if strings.TrimSpace(req.Code) == "" {
		http.Error(w, "refusing to save an empty solution", http.StatusBadRequest)
		return
	}
	resp, err := s.save(p, req.Code)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRunTests saves the editor's contents and then runs the package tests.
//
// Saving first is not a convenience: without it, a keystroke followed by "run"
// would test the previous version of the file and report a result about code
// that is no longer on screen.
func (s *Server) handleRunTests(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	req, err := decodeBody[saveRequest](r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out := testResponse{}
	if strings.TrimSpace(req.Code) != "" {
		saved, err := s.save(p, req.Code)
		if err != nil {
			s.fail(w, err)
			return
		}
		out.saveResponse = saved
	}

	ctx, cancel := context.WithTimeout(r.Context(), runTimeout)
	defer cancel()

	// The package path comes from the catalog, never from the request: a slug
	// that did not resolve above cannot reach this line, so there is no way to
	// aim `go test` at an arbitrary directory.
	start := time.Now()
	output, passed, runErr := s.runTests(ctx, s.root, p.SolutionDir())
	out.Output, out.Passed = output, passed
	out.Millis = time.Since(start).Milliseconds()
	if runErr != nil {
		out.RunErr = runErr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- shared bits ----------

// save writes the solution file, formatting it when it parses.
func (s *Server) save(p catalog.Problem, code string) (saveResponse, error) {
	path := filepath.Join(s.root, p.SolutionDir(), "solution.go")
	if _, err := os.Stat(path); err != nil {
		return saveResponse{}, fmt.Errorf("no solution file yet for %s; create it first", p.Slug)
	}

	formatted, formatErr := formatGo(code)
	// Written atomically so an interrupted save cannot leave a half file where
	// a solution used to be.
	if err := catalog.WriteFileAtomic(path, []byte(formatted)); err != nil {
		return saveResponse{}, err
	}
	resp := saveResponse{Code: formatted}
	if formatErr != nil {
		resp.FormatError = formatErr.Error()
	}
	return resp, nil
}

func (s *Server) readSolution(p catalog.Problem) (code string, exists bool) {
	code, err := readIfPresent(filepath.Join(s.root, p.SolutionDir(), "solution.go"))
	return code, err == nil && code != ""
}

// countArchived is how many earlier attempts `nc resolve` has put aside.
func (s *Server) countArchived(p catalog.Problem) int {
	des, err := os.ReadDir(filepath.Join(s.root, p.SolutionDir(), "attempts"))
	if err != nil {
		return 0
	}
	n := 0
	for _, de := range des {
		if !de.IsDir() {
			n++
		}
	}
	return n
}

func readIfPresent(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeBody[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return v, nil // an empty body is a request with no fields set
		}
		return v, fmt.Errorf("malformed JSON body: %w", err)
	}
	return v, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("error: writing JSON response: %v", err)
	}
}

type resolveResponse struct {
	Code     string `json:"code"`
	Archive  string `json:"archive"`
	Archived int    `json:"archived"`
}

// handleResolve starts a cold re-solve: the current solution is archived and the
// file is reset to a blank scaffold, exactly as `nc resolve` does.
//
// Nothing is destroyed — the old attempt is on disk under attempts/ and
// `nc diff` reads it back — but the editor's contents do change out from under
// the user, so the page asks before calling this.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	// Archiving a solution that is still being written would put aside a
	// half-finished attempt and hand back an empty file.
	if s.progress.Get(p.Slug).Status == progress.Attempted {
		http.Error(w, "this problem is already in progress; there is nothing to re-solve",
			http.StatusConflict)
		return
	}

	signature, _ := readIfPresent(filepath.Join(s.root, p.ContentDir(), "signature.txt"))
	raw, _ := readIfPresent(filepath.Join(s.root, p.ContentDir(), "problem.md"))
	_, statement := content.SplitFrontmatter(raw)

	now := s.now()
	archive, _, err := scaffold.Reset(s.root, p, signature, statement, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if _, err := s.progress.SetStatus(p.Slug, progress.Attempted, now); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.progress.ResetTimer(p.Slug, now); err != nil {
		s.fail(w, err)
		return
	}

	code, _ := s.readSolution(p)
	rel, _ := filepath.Rel(s.root, archive)
	writeJSON(w, http.StatusOK, resolveResponse{
		Code:     code,
		Archive:  filepath.ToSlash(rel),
		Archived: s.countArchived(p),
	})
}
