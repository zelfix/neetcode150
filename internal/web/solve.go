package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"database/sql"
	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/progress"
	"neetcode150/internal/scaffold"
	"neetcode150/internal/studydb"
	"neetcode150/internal/tutor"
)

// maxBody caps a request body. A solution is a few kilobytes; this only exists
// so a malformed or hostile request cannot make the server read without bound.
const maxBody = 1 << 20 // 1 MiB

// tutorView is what the panel needs before any events arrive.
type tutorView struct {
	Available bool
	Why       string
	Provider  string
	Mode      string
	Active    bool // a session is already running, so replay it
	// HasTurns is whether that session has said anything yet. An attempt can
	// be under way with an empty conversation — the agent is still starting,
	// or it was started before the panel learned to open the lesson — and
	// collapsing the statement then leaves the column with nothing in it.
	HasTurns bool
}

type solvePage struct {
	common
	P                 problemView
	Statement         *content.Doc
	NotesRaw          string
	LatestReview      *submissionReport
	Code              string
	PreviousCode      string
	TestCode          string
	PreviousAvailable bool
	Active            bool
	ChatEnabled       bool
	SolutionPath      string
	TestPath          string
	Archived          int
	Statuses          []progress.Status
	Tutor             tutorView
	Submissions       []submissionView
	Record            recordView
	PendingSubmit     bool
	PendingSubmitSeq  int
	ElapsedSeconds    int64
	ElapsedLabel      string
}

// recordView fills the work column before an attempt starts: why this problem
// is up now, and how the earlier sittings actually went.
//
// Until now this history had no view at all — the aggregates are on the reading
// view and the per-sitting table only ever existed in `nc history`. Showing it
// here is priming without spoiling: it is the schedule and your own ratings,
// never the approach.
type recordView struct {
	Headline      string
	Stage         int
	IntervalDays  int
	Attempts      int
	MinutesSpent  int
	NextReview    *time.Time
	Sittings      []progress.Sitting
	PatternIndex  int // 1-based place of this problem in its pattern
	PatternCount  int
	MedianMinutes int // across solved problems; 0 when there is too little to compare
	Notes         []string
}

func (s *Server) record(p catalog.Problem, view problemView, now time.Time) recordView {
	e := view.Entry
	r := recordView{
		Stage:        e.ReviewStage,
		IntervalDays: e.IntervalDays(),
		Attempts:     e.Attempts,
		MinutesSpent: e.MinutesSpent,
		NextReview:   e.NextReview,
		Sittings:     progress.Sittings(e),
	}

	if g, ok := s.cat.Group(p.PatternSlug); ok {
		r.PatternCount = len(g.Problems)
		for i, other := range g.Problems {
			if other.Slug == p.Slug {
				r.PatternIndex = i + 1
				break
			}
		}
	}

	switch {
	case view.IsDue && view.DaysOverdue > 0:
		unit := "days"
		if view.DaysOverdue == 1 {
			unit = "day"
		}
		r.Headline = fmt.Sprintf("Due for review · %d %s overdue", view.DaysOverdue, unit)
	case view.IsDue:
		r.Headline = "Due for review today"
	// A rating without history is a real shape on disk: entries rated before
	// the log kept a per-sitting record still carry their confidence.
	case e.Confidence > 0 || len(e.History) > 0:
		r.Headline = "Solved before · not due yet"
	case e.Attempts > 0:
		r.Headline = "Started before · never rated"
	default:
		r.Headline = "First sitting"
	}

	switch {
	case len(r.Sittings) == 0 && e.Confidence > 0:
		r.Notes = append(r.Notes, fmt.Sprintf("Last rated %d of 5. There is no per-sitting "+
			"history for this one — it was rated before the log kept any.", e.Confidence))
	case len(r.Sittings) == 0:
		r.Notes = append(r.Notes, "Nothing recorded yet. A 4 or a 5 widens the interval; "+
			"a 1 or a 2 sends it back to the start of the ladder and marks it shaky.")
	default:
		last := r.Sittings[len(r.Sittings)-1]
		if last.Rung > 0 {
			r.Notes = append(r.Notes, fmt.Sprintf("Last sitting needed hint rung %d.", last.Rung))
		} else {
			r.Notes = append(r.Notes, "Last sitting was unaided.")
		}
		if last.Slipped {
			r.Notes = append(r.Notes, "The rating fell last time, so the interval before it was too long.")
		}
		if last.Generous {
			r.Notes = append(r.Notes, "That rating sat next to a high rung. The rung is the honest "+
				"measure where the rating is the felt one.")
		}
	}

	// A median is only worth printing once there is something to be a median of.
	var spent []int
	for _, other := range s.progress.All() {
		if other.Status == progress.Solved && other.MinutesSpent > 0 {
			spent = append(spent, other.MinutesSpent)
		}
	}
	if len(spent) >= 3 {
		slices.Sort(spent)
		r.MedianMinutes = spent[len(spent)/2]
	}
	return r
}

type submissionReport struct {
	Date            time.Time `json:"date"`
	Confidence      int       `json:"confidence"`
	Rung            int       `json:"rung"`
	Feedback        string    `json:"feedback"`
	Rationale       string    `json:"rationale"`
	LearningSummary string    `json:"learningSummary,omitempty"`
	Followup        string    `json:"followup,omitempty"`
	Passed          bool      `json:"passed"`
	ElapsedSecond   int64     `json:"elapsedSeconds"`
	Provider        string    `json:"provider"`
}

type submissionView struct {
	Name   string
	Code   string
	Review *submissionReport
}

// handleSolve renders the workspace: the statement on one side, an editor and
// the test output on the other.
func (s *Server) handleSolve(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}

	statement, err := s.content.Doc(nil, filepath.Join(p.ContentDir(), "problem.md"))
	if err != nil {
		s.fail(w, err)
		return
	}
	notesRaw, err := s.progress.Scope().Document(p.Slug, "notes")
	if err != nil {
		s.fail(w, err)
		return
	}
	code, exists := s.readSolution(p)
	testCode := s.readTests(p)
	entry := s.progress.Get(p.Slug)
	active := entry.Status == progress.Attempted && entry.AttemptStartedAt != nil
	now := s.now()
	elapsed, _ := entry.Elapsed(now)
	previousCode := ""
	if !active && exists {
		previousCode = code
	}
	if !active {
		code = ""
	}
	view := s.view(p, now)
	submissions := s.submissions(p)
	var latestReview *submissionReport
	for _, submission := range submissions {
		if submission.Review != nil {
			latestReview = submission.Review
			break
		}
	}
	coach := s.tutorView(p.Slug)
	s.submissionMu.Lock()
	pending, pendingSubmit := s.pendingSubmissions[p.Slug]
	s.submissionMu.Unlock()

	s.render(w, "solve", solvePage{
		common:            s.fullCommon(fmt.Sprintf("%d. %s", p.ID, p.Title), "problems"),
		P:                 view,
		Statement:         statement,
		NotesRaw:          notesRaw,
		LatestReview:      latestReview,
		Code:              code,
		PreviousCode:      previousCode,
		TestCode:          testCode,
		PreviousAvailable: previousCode != "" || len(submissions) > 0,
		Active:            active,
		ChatEnabled:       active || coach.Active || exists || latestReview != nil,
		SolutionPath:      filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution.go")),
		TestPath:          filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution_test.go")),
		Archived:          s.countArchived(p),
		Statuses:          progress.Statuses,
		Tutor:             coach,
		Record:            s.record(p, view, now),
		Submissions:       submissions,
		PendingSubmit:     pendingSubmit,
		PendingSubmitSeq:  pending.FollowupSeq,
		ElapsedSeconds:    int64(elapsed.Seconds()),
		ElapsedLabel:      formatElapsed(elapsed),
	})
}

func formatElapsed(elapsed time.Duration) string {
	seconds := int(elapsed.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds/60)%60, seconds%60)
}

func (s *Server) submissions(p catalog.Problem) []submissionView {
	attempts, err := s.progress.Scope().Attempts(p.Slug)
	if err != nil {
		panic(err)
	}
	var out []submissionView
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		v := submissionView{Name: a.Ref(), Code: a.Code}
		if len(a.Review) > 0 {
			var report submissionReport
			if err := json.Unmarshal(a.Review, &report); err != nil {
				panic(err)
			}
			v.Review = &report
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) tutorView(slug string) tutorView {
	available, why := s.tutor.Available()
	v := tutorView{
		Available: available,
		Why:       why,
		Provider:  string(s.tutor.Provider()),
		Mode:      string(s.tutor.Mode()),
	}
	if sess := s.tutor.Get(slug); sess != nil {
		v.Active = true
		v.HasTurns = len(sess.Transcript()) > 0
	}
	return v
}

// ---------- the JSON API behind the editor ----------

type saveRequest struct {
	Code string `json:"code"`
}

type saveResponse struct {
	Code        string `json:"code"`                  // as stored; draft saves preserve the input
	FormatError string `json:"formatError,omitempty"` // why it was not formatted
}

type testResponse struct {
	saveResponse
	Output string `json:"output"`
	Passed bool   `json:"passed"`
	Millis int64  `json:"millis"`
	RunErr string `json:"runError,omitempty"`
}

type submitResponse struct {
	testResponse
	ElapsedSeconds int64  `json:"elapsedSeconds"`
	Pending        bool   `json:"pending,omitempty"`
	Confidence     int    `json:"confidence,omitempty"`
	Rung           int    `json:"rung,omitempty"`
	Feedback       string `json:"feedback,omitempty"`
	RatingError    string `json:"ratingError,omitempty"`
	Archive        string `json:"archive,omitempty"`
}

type pendingSubmission struct {
	Token       string
	Code        string
	Output      string
	Passed      bool
	Millis      int64
	Elapsed     time.Duration
	Entry       progress.Entry
	FollowupSeq int
	Checking    bool
}

type submissionDecision struct {
	Decision        string `json:"decision"`
	Question        string `json:"question,omitempty"`
	Confidence      int    `json:"confidence,omitempty"`
	Rung            int    `json:"rung,omitempty"`
	Feedback        string `json:"feedback,omitempty"`
	Rationale       string `json:"rationale,omitempty"`
	LearningSummary string `json:"learningSummary,omitempty"`
	Followup        string `json:"followup,omitempty"`
}

type tutorEvaluation struct {
	Confidence      int    `json:"confidence"`
	Rung            int    `json:"rung"`
	Feedback        string `json:"feedback"`
	Rationale       string `json:"rationale"`
	LearningSummary string `json:"learningSummary,omitempty"`
	Followup        string `json:"followup,omitempty"`
}

// handleSubmit runs tests and assesses the available learning evidence.
// The tutor asks a follow-up only when something material remains unclear.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
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
	if strings.TrimSpace(req.Code) == "" {
		http.Error(w, "refusing to submit an empty solution", http.StatusBadRequest)
		return
	}
	s.submissionMu.Lock()
	_, alreadyPending := s.pendingSubmissions[p.Slug]
	s.submissionMu.Unlock()
	if alreadyPending {
		http.Error(w, "finish the pending submission conversation first", http.StatusConflict)
		return
	}
	started := s.now()
	entry := s.progress.Get(p.Slug)
	var elapsed time.Duration
	if entry.AttemptStartedAt != nil {
		elapsed = started.Sub(*entry.AttemptStartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
	}
	if elapsed > 6*time.Hour {
		elapsed = 0 // Ignore an overnight idle timer, as `nc rate` does.
	}
	saved, err := s.save(p, req.Code)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := submitResponse{testResponse: testResponse{saveResponse: saved}, ElapsedSeconds: int64(elapsed.Seconds())}
	ctx, cancel := context.WithTimeout(r.Context(), runTimeout)
	defer cancel()
	testStart := time.Now()
	run, runErr := s.runTests(ctx, s.root, p.SolutionDir())
	out.Output, out.Passed = run.Output, run.OK
	out.Millis = time.Since(testStart).Milliseconds()
	s.recordRun(p.Slug, run, runErr)
	passed := run.OK
	if runErr != nil {
		out.RunErr = runErr.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	sess, err := s.tutor.Start(p)
	if err != nil {
		out.RatingError = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	seq := 0
	if sess := s.tutor.Get(p.Slug); sess != nil {
		for _, event := range sess.Transcript() {
			if event.Seq > seq {
				seq = event.Seq
			}
		}
	}
	s.submissionMu.Lock()
	pending := pendingSubmission{Token: rand.Text(), Code: saved.Code, Output: run.Output, Passed: passed,
		Millis: out.Millis, Elapsed: elapsed, Entry: entry, FollowupSeq: seq, Checking: true}
	err = s.persistPendingLocked(p.Slug, pending)
	s.submissionMu.Unlock()
	if err != nil {
		out.RatingError = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Pending = true
	go s.advanceSubmissionAfterAnswer(p, pending, sess)
	writeJSON(w, http.StatusOK, out)
}

// openLesson welcomes the user without repeating checks already demonstrated
// on an earlier solve. The statement is visible, so it needs no model summary.
//
// Starting the agent takes seconds, so this runs detached: Start answers at
// once and the reply arrives over the event stream.
func (s *Server) openLesson(p catalog.Problem) {
	if available, _ := s.tutor.Available(); !available {
		return
	}
	go func() {
		// An empty display string keeps the prompt itself out of the
		// transcript; the browser skips a user turn with no text.
		if _, err := s.tutor.SendAs(p, s.openingPrompt(p), ""); err != nil {
			return // a tutor that will not start is reported by the panel itself
		}
	}()
}

func (s *Server) openingPrompt(p catalog.Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The user has just started a sitting on %q and the page is showing them "+
		"the statement verbatim, examples and constraints included.\n\n", p.Title)
	if len(s.progress.Get(p.Slug).History) > 0 {
		b.WriteString("Welcome them back and invite them to begin their cold solve. Skip the restatement and introductory checks they already demonstrated. Ask a question only if the earlier evidence identifies a gap worth revisiting.\n\n")
	} else {
		b.WriteString("Open the lesson. Greet them in one short line, then ask them to restate the " +
			"problem in their own words: the input and output types, what happens at the boundaries, " +
			"and whether any property of the input is guaranteed.\n\n")
	}
	b.WriteString("Do not summarise or paraphrase the statement — they can read it, and restating " +
		"it is the work. Do not name the pattern, suggest a data structure, or give any hint yet. " +
		"If you ask a question, ask only one and wait.")
	s.appendLearningHistory(&b, p)
	b.WriteString("\n" + tutor.LearningPolicy)
	return b.String()
}

func (s *Server) advanceSubmissionAfterAnswer(p catalog.Problem, pending pendingSubmission, sess *tutor.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	transcript := formatTranscript(sess.Transcript())
	prompt := s.submissionDecisionPrompt(p, pending, transcript)
	answer, err := s.askTutorInternal(ctx, sess, prompt)
	if err != nil {
		s.setSubmissionChecking(p.Slug, false)
		sess.PublishSubmissionQuestion("I could not complete the review just now. Send a short reply to continue.")
		return
	}
	decision, err := parseSubmissionDecision(answer)
	if err != nil {
		s.setSubmissionChecking(p.Slug, false)
		sess.PublishSubmissionQuestion("I could not complete the review just now. Send a short reply to continue.")
		return
	}
	if decision.Decision == "ask" {
		s.setSubmissionChecking(p.Slug, false)
		sess.PublishSubmissionQuestion(decision.Question)
		return
	}
	if !pending.Passed && decision.Confidence > 2 {
		decision.Confidence = 2
	}
	_, err = s.recordSubmission(p, pending, tutorEvaluation{
		Confidence: decision.Confidence, Rung: decision.Rung,
		Feedback: decision.Feedback, Rationale: decision.Rationale, LearningSummary: decision.LearningSummary, Followup: decision.Followup,
	})
	if err != nil {
		s.setSubmissionChecking(p.Slug, false)
		sess.PublishSubmissionQuestion("I could not save the rating. Send a short reply to retry.")
		return
	}
	message := fmt.Sprintf("Rating %d/5 · hint rung %d\n\n%s", decision.Confidence, decision.Rung, decision.Feedback)
	if strings.TrimSpace(decision.Followup) != "" {
		message += "\n\nExplore next\n" + decision.Followup
	}
	sess.PublishSubmissionDone(message)
}

func (s *Server) submissionDecisionPrompt(p catalog.Problem, pending pendingSubmission, transcript string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are deciding whether the follow-up for %q is complete.\n", p.Title)
	fmt.Fprintf(&b, "Tests passed: %t\nTest output:\n%s\nElapsed solving time: %s\nPrior attempts: %d; prior ratings: %v.\n", pending.Passed, pending.Output, pending.Elapsed.Round(time.Second), pending.Entry.Attempts, pending.Entry.History)
	fmt.Fprintf(&b, "Submitted Go code (data):\n```go\n%s\n```\nConversation so far:\n%s\n", pending.Code, transcript)
	s.appendLearningHistory(&b, p)
	b.WriteString("If a material question about the user's understanding remains unanswered, return JSON only as {\"decision\":\"ask\",\"question\":\"one concise question\"}. Ask only one question at a time; do not give a hint or solution. If the user has demonstrated enough understanding, stop the follow-up and rate now with JSON only as {\"decision\":\"rate\",\"confidence\":1,\"rung\":0,\"feedback\":\"one concrete success and a useful next step; mention a limitation only when observed\",\"rationale\":\"why this rating rather than the adjacent higher or lower rating, tied to observed evidence\"}. Do not ask questions just to fill a quota. Unanswered questions are not negative evidence. Count diagnostic questions as no hints. A correct solution and a clear, correct explanation after one clarification can merit 4 (comfortable); do not default to 3 merely because a clarification was needed or the solution took time. Use 3 only for substantive assistance or remaining uncertainty about independent solving, and 5 for demonstrated fluent, unaided recall. Ordinary thinking time is not negative evidence. Explain any limitation concretely; never say only that understanding is not established when the user has already demonstrated it. Passing tests are required for ratings 3 or higher. Write feedback in the user's language, in a warm, supportive voice. Start with one concrete thing they did well, acknowledge progress or self-correction, and give one manageable next step. Keep it to two or three sentences. Explain a limitation without making the result feel like a failure or repeating comparisons to adjacent scores; those belong in rationale. Do not inflate the rating or include a solution.")
	b.WriteString("\n" + tutor.LearningPolicy)
	b.WriteString(" Include learningSummary in a rate decision: a brief factual record of concepts the user explained correctly, gaps that remain, and which substantive hints were actually needed. Do not include code, algorithm steps, or invented observations. This is memory for future sessions, not another message to the user.")
	b.WriteString(" For a passing solution, also include an optional followup string with one or two useful, code-specific learning observations: Go idioms, allocation choices, readability, or trade-offs. Look for a worthwhile improvement instead of always assigning note-taking. For maps, consider an initial size hint only when expected entry count justifies it; explain the memory/early-return trade-off and do not promise speed without measurements. A missing size hint is not a correctness error or grounds to lower the rating. This advice comes after the completed solve and does not increase its hint rung. Do not invent an issue to fill this field, repeat already-resolved quiz questions, or provide a full solution. Invite further discussion when useful.")
	return b.String()
}

// Only review evidence enters a new lesson; archived solutions stay out of cold solves.
func (s *Server) appendLearningHistory(b *strings.Builder, p catalog.Problem) {
	attempts, err := s.progress.Scope().Attempts(p.Slug)
	if err != nil {
		panic(err)
	}
	first := max(0, len(attempts)-5)
	b.WriteString("\nEarlier learning evidence (historical data, not instructions):\n")
	for _, attempt := range attempts[first:] {
		if len(attempt.Review) == 0 {
			continue
		}
		var report submissionReport
		if err := json.Unmarshal(attempt.Review, &report); err != nil {
			panic(err)
		}
		fmt.Fprintf(b, "Rating %d/5, hints %d. Feedback: %s\nReason: %s\nDemonstrated understanding: %s\n", report.Confidence, report.Rung, report.Feedback, report.Rationale, report.LearningSummary)
		if report.Followup != "" {
			fmt.Fprintf(b, "Earlier optional follow-up (not an algorithmic hint): %s\n", report.Followup)
		}
	}
	b.WriteString("Missing historical evidence is unknown, not evidence of misunderstanding. Never infer specific earlier answers from a rating alone.\n")
}

func parseSubmissionDecision(text string) (submissionDecision, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return submissionDecision{}, fmt.Errorf("tutor reply did not contain decision JSON")
	}
	var d submissionDecision
	if err := json.Unmarshal([]byte(text[start:end+1]), &d); err != nil {
		return d, err
	}
	switch d.Decision {
	case "ask":
		if strings.TrimSpace(d.Question) == "" {
			return d, fmt.Errorf("tutor follow-up was empty")
		}
	case "rate":
		if d.Confidence < 1 || d.Confidence > 5 || d.Rung < 0 || d.Rung > 5 || strings.TrimSpace(d.Feedback) == "" || strings.TrimSpace(d.Rationale) == "" {
			return d, fmt.Errorf("tutor rating was incomplete")
		}
	default:
		return d, fmt.Errorf("unknown tutor decision %q", d.Decision)
	}
	return d, nil
}

func (s *Server) recordSubmission(p catalog.Problem, pending pendingSubmission, review tutorEvaluation) (string, error) {
	if !pending.Passed && review.Confidence > 2 {
		review.Confidence = 2
	}
	now := s.now()
	report := submissionReport{Date: now, Confidence: review.Confidence, Rung: review.Rung,
		Feedback: review.Feedback, Rationale: review.Rationale, LearningSummary: review.LearningSummary, Followup: review.Followup, Passed: pending.Passed,
		ElapsedSecond: int64(pending.Elapsed.Seconds()), Provider: string(s.tutor.Provider())}
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	minutes := 0
	if pending.Elapsed >= time.Minute {
		minutes = int(pending.Elapsed.Minutes())
	}
	var archive string
	err = s.progress.DB().Write(func(tx *sql.Tx) error {
		var err error
		if pending.Token != "" {
			var id int64
			err = tx.QueryRow("SELECT id FROM attempts WHERE user_id=? AND submission_key=?", s.progress.UserID(), pending.Token).Scan(&id)
			if err == nil {
				archive = fmt.Sprintf("attempt:%d", id)
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		archive, err = studydb.InsertAttempt(tx, s.progress.UserID(), p.Slug, pending.Code, data, now, pending.Token)
		if err != nil {
			return err
		}
		if err = s.progress.CompleteIn(tx, p.Slug, review.Confidence, review.Rung, minutes, pending.Passed, now); err != nil {
			return err
		}
		return s.progress.Scope().DeletePendingIn(tx, p.Slug)
	})
	if err != nil {
		return "", err
	}
	s.submissionMu.Lock()
	delete(s.pendingSubmissions, p.Slug)
	s.submissionMu.Unlock()
	return archive, nil
}

func formatTranscript(events []tutor.Event) string {
	var b strings.Builder
	for _, e := range events {
		if e.Kind == tutor.KindUser || e.Kind == tutor.KindText || e.Kind == tutor.KindSubmissionQuestion || e.Kind == tutor.KindSubmissionDone {
			role := "Tutor"
			if e.Kind == tutor.KindUser {
				role = "User"
			}
			fmt.Fprintf(&b, "%s: %s\n", role, e.Text)
		}
	}
	return b.String()
}

func (s *Server) askTutorAs(ctx context.Context, p catalog.Problem, prompt, display string) (string, error) {
	var baseline int
	if old := s.tutor.Get(p.Slug); old != nil {
		for _, e := range old.Transcript() {
			if e.Seq > baseline {
				baseline = e.Seq
			}
		}
	}
	sess, err := s.tutor.SendAs(p, prompt, display)
	if err != nil {
		return "", err
	}
	replay, ch, stop := sess.Subscribe()
	defer stop()
	var response strings.Builder
	consume := func(e tutor.Event) bool {
		if e.Seq <= baseline {
			return false
		}
		if e.Kind == tutor.KindText || e.Kind == tutor.KindInternalText {
			response.WriteString(e.Text)
		}
		return e.Kind == tutor.KindDone || e.Kind == tutor.KindError || e.Kind == tutor.KindExit
	}
	for _, e := range replay {
		if consume(e) {
			if response.Len() > 0 {
				return response.String(), nil
			}
			return "", fmt.Errorf("tutor returned no evaluation")
		}
	}
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return "", fmt.Errorf("tutor event stream closed")
			}
			if consume(e) {
				if response.Len() > 0 {
					return response.String(), nil
				}
				return "", fmt.Errorf("tutor returned no evaluation")
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (s *Server) askTutorInternal(ctx context.Context, sess *tutor.Session, prompt string) (string, error) {
	baseline := 0
	for _, event := range sess.Transcript() {
		if event.Seq > baseline {
			baseline = event.Seq
		}
	}
	if err := sess.SendInternal(prompt); err != nil {
		return "", err
	}
	replay, ch, stop := sess.Subscribe()
	defer stop()
	var response strings.Builder
	consume := func(event tutor.Event) bool {
		if event.Seq <= baseline {
			return false
		}
		if event.Kind == tutor.KindInternalText {
			response.WriteString(event.Text)
		}
		return event.Kind == tutor.KindDone || event.Kind == tutor.KindError || event.Kind == tutor.KindExit
	}
	for _, event := range replay {
		if consume(event) {
			if event.Kind == tutor.KindError || event.Kind == tutor.KindExit {
				return "", fmt.Errorf("tutor evaluation failed: %s", event.Text)
			}
			return response.String(), nil
		}
	}
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return "", fmt.Errorf("tutor event stream closed")
			}
			if consume(event) {
				if event.Kind == tutor.KindError || event.Kind == tutor.KindExit {
					return "", fmt.Errorf("tutor evaluation failed: %s", event.Text)
				}
				return response.String(), nil
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

type createResponse struct {
	Code     string   `json:"code"`
	TestCode string   `json:"testCode"`
	Created  []string `json:"created"`
	Archive  string   `json:"archive,omitempty"`
}

// handleStartAttempt begins a cold sitting, archiving any inactive solution
// before creating a blank scaffold and starting the timer.
func (s *Server) handleStartAttempt(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	entry := s.progress.Get(p.Slug)
	if entry.Status == progress.Attempted && entry.AttemptStartedAt != nil {
		http.Error(w, "this attempt is already in progress", http.StatusConflict)
		return
	}

	s.submissionMu.Lock()
	_, pendingSubmit := s.pendingSubmissions[p.Slug]
	s.submissionMu.Unlock()
	if pendingSubmit {
		http.Error(w, "finish the tutor follow-up before starting another attempt", http.StatusConflict)
		return
	}

	signature, _ := readIfPresent(filepath.Join(s.root, p.ContentDir(), "signature.txt"))
	raw, _ := readIfPresent(filepath.Join(s.root, p.ContentDir(), "problem.md"))
	_, statement := content.SplitFrontmatter(raw)
	solutionPath := filepath.Join(s.root, p.SolutionDir(), "solution.go")
	var archive string
	var err error
	if _, statErr := os.Stat(solutionPath); statErr == nil {
		archive, _, err = scaffold.Reset(s.progress.Scope(), s.root, p, signature, statement, s.now())
	} else if os.IsNotExist(statErr) {
		_, _, err = scaffold.Create(s.progress.Scope(), s.root, p, signature, statement)
	} else {
		err = statErr
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	now := s.now()
	if _, err := s.progress.StartAttempt(p.Slug, now); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.progress.ResetTimer(p.Slug, now); err != nil {
		s.fail(w, err)
		return
	}
	s.tutor.Reset(p.Slug)
	s.openLesson(p)
	code, _ := s.readSolution(p)
	testCode := s.readTests(p)
	created := []string{filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution.go"))}
	if _, err := os.Stat(filepath.Join(s.root, p.SolutionDir(), "solution_test.go")); err == nil {
		created = append(created, filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution_test.go")))
	}
	archiveRel := archive
	writeJSON(w, http.StatusOK, createResponse{Code: code, TestCode: testCode, Created: created, Archive: filepath.ToSlash(archiveRel)})
}

// handleCancelAttempt saves the editor and stops its clock without discarding
// the work; the next Start exposes the saved source as a previous attempt.
func (s *Server) handleCancelAttempt(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	entry := s.progress.Get(p.Slug)
	if entry.Status != progress.Attempted || entry.AttemptStartedAt == nil {
		http.Error(w, "there is no active attempt to cancel", http.StatusConflict)
		return
	}
	s.submissionMu.Lock()
	_, pendingSubmit := s.pendingSubmissions[p.Slug]
	s.submissionMu.Unlock()
	if pendingSubmit {
		http.Error(w, "the tutor follow-up is still in progress", http.StatusConflict)
		return
	}
	req, err := decodeBody[saveRequest](r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := s.save(p, req.Code)
	if err != nil {
		s.fail(w, err)
		return
	}
	now := s.now()
	elapsed, running := entry.Elapsed(now)
	if running && elapsed >= time.Minute {
		if _, err := s.progress.AddMinutes(p.Slug, int(elapsed.Minutes()), now); err != nil {
			s.fail(w, err)
			return
		}
	}
	if _, err := s.progress.StopTimer(p.Slug, now); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
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

	written, _, err := scaffold.Create(s.progress.Scope(), s.root, p, signature, statement)
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
	testCode := s.readTests(p)
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
	resp, err := s.saveDraft(p, req.Code)
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

	if strings.TrimSpace(req.Code) == "" {
		http.Error(w, "write some code before running tests", http.StatusBadRequest)
		return
	}
	saved, err := s.save(p, req.Code)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := testResponse{saveResponse: saved}

	ctx, cancel := context.WithTimeout(r.Context(), runTimeout)
	defer cancel()

	// The package path comes from the catalog, never from the request: a slug
	// that did not resolve above cannot reach this line, so there is no way to
	// aim `go test` at an arbitrary directory.
	start := time.Now()
	run, runErr := s.runTests(ctx, s.root, p.SolutionDir())
	out.Output, out.Passed = run.Output, run.OK
	out.Millis = time.Since(start).Milliseconds()
	s.recordRun(p.Slug, run, runErr)
	if runErr != nil {
		out.RunErr = runErr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// recordRun files what the run broke, so that "it failed again" can become
// "you keep breaking the empty-input case".
//
// A run that never happened is not recorded: a compile error or a cut-off loop
// says nothing about which fixtures are hard, and counting it would make the
// pass rate a measure of typing rather than of understanding. Recording is
// best-effort — losing a statistic must never fail the run the user asked for.
func (s *Server) recordRun(slug string, run testRun, runErr error) {
	if runErr != nil || !run.Ran {
		return
	}
	if err := s.progress.Scope().RecordTestRun(slug, s.now(), run.Passed, run.Failed, run.Cases); err != nil {
		log.Printf("record test run for %s: %v", slug, err)
	}
}

// ---------- shared bits ----------

// save writes the solution file, formatting it when it parses.
func (s *Server) save(p catalog.Problem, code string) (saveResponse, error) {
	formatted, formatErr := formatGo(code)
	resp, err := s.saveDraft(p, formatted)
	if formatErr != nil {
		resp.FormatError = formatErr.Error()
	}
	return resp, err
}

func (s *Server) saveDraft(p catalog.Problem, code string) (saveResponse, error) {
	path := filepath.Join(s.root, p.SolutionDir(), "solution.go")
	if _, err := os.Stat(path); err != nil {
		return saveResponse{}, fmt.Errorf("no solution file yet for %s; create it first", p.Slug)
	}
	if err := catalog.WriteFileAtomic(path, []byte(code)); err != nil {
		return saveResponse{}, err
	}
	return saveResponse{Code: code}, nil
}

func (s *Server) readSolution(p catalog.Problem) (code string, exists bool) {
	code, err := readIfPresent(filepath.Join(s.root, p.SolutionDir(), "solution.go"))
	return code, err == nil && code != ""
}

// countArchived is how many earlier attempts `nc resolve` has put aside.
func (s *Server) countArchived(p catalog.Problem) int {
	var n int
	if err := s.progress.DB().SQL.QueryRow("SELECT count(*) FROM attempts WHERE user_id=? AND slug=?", s.progress.UserID(), p.Slug).Scan(&n); err != nil {
		panic(err)
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
	archive, _, err := scaffold.Reset(s.progress.Scope(), s.root, p, signature, statement, now)
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
	rel := archive
	writeJSON(w, http.StatusOK, resolveResponse{
		Code:     code,
		Archive:  filepath.ToSlash(rel),
		Archived: s.countArchived(p),
	})
}

// readTests includes personal tests and the additional practice suite in one tab.
func (s *Server) readTests(p catalog.Problem) string {
	paths, _ := filepath.Glob(filepath.Join(s.root, p.SolutionDir(), "*_test.go"))
	var out strings.Builder
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "// %s\n%s", filepath.Base(path), text)
	}
	return out.String()
}
