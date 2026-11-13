package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/progress"
	"neetcode150/internal/scaffold"
	"neetcode150/internal/studydb"
	"neetcode150/internal/tutor"
	"neetcode150/internal/workspace"
)

// Interview bounds. Two or three problems is what fits in a sitting; the limit
// per problem is what a phone screen gives you for one.
const (
	minInterviewProblems = 2
	maxInterviewProblems = 3
	defaultProblemLimit  = 20
	minProblemLimit      = 5
	maxProblemLimit      = 60
)

// interviewState is the payload of an interviews row. The columns carry only
// what the database searches or constrains on; the state machine is here.
type interviewState struct {
	Problems []interviewProblem `json:"problems"`
	// Lang is chosen once for the whole interview, on the way in. It overrides
	// each problem's own study language while the interview runs.
	Lang string `json:"lang,omitempty"`
	// Current indexes Problems. It only moves forward.
	Current int    `json:"current"`
	Pattern string `json:"pattern,omitempty"` // the filter the draw used
	Report  string `json:"report,omitempty"`
	// Finishing is set once the last problem closes and the feedback is being
	// written. The row stays live until the report lands, so nothing can start
	// a second interview in the gap — but no problem is current any more, which
	// is what takes the workspace out of interview mode.
	Finishing bool `json:"finishing,omitempty"`
}

type interviewProblem struct {
	Slug  string `json:"slug"`
	Lang  string `json:"lang"`
	Limit int    `json:"limitMinutes"`

	Started *time.Time `json:"startedAt,omitempty"`
	Closed  *time.Time `json:"closedAt,omitempty"`
	Minutes int        `json:"minutes,omitempty"`
	Submits int        `json:"submits,omitempty"`
	Passed  bool       `json:"passed,omitempty"`
	// Notes is what the interviewer recorded for itself at each submission. It
	// is the only thing the final report is written from, so a report does not
	// depend on a transcript still being in memory.
	Notes []string `json:"notes,omitempty"`
	// TimeUp records that the one time-is-up turn has been spent, so a browser
	// reporting expiry on every poll cannot buy a second one.
	TimeUp bool `json:"timeUpSaid,omitempty"`
}

// liveInterview is the sitting in progress and its decoded payload.
func (s *session) liveInterview() (*studydb.Interview, *interviewState, error) {
	rec, err := s.progress.Scope().LiveInterview()
	if err != nil || rec == nil {
		return nil, nil, err
	}
	var st interviewState
	if err := json.Unmarshal(rec.Payload, &st); err != nil {
		return nil, nil, err
	}
	return rec, &st, nil
}

// interviewHere reports the live interview when the problem on screen is the
// one it is currently asking about. Everything the workspace does differently
// in an interview hangs off this.
func (s *session) interviewHere(slug string) (*studydb.Interview, *interviewState, bool) {
	rec, st, err := s.liveInterview()
	if err != nil || rec == nil {
		return nil, nil, false
	}
	if st.Finishing || st.Current >= len(st.Problems) || st.Problems[st.Current].Slug != slug {
		return nil, nil, false
	}
	return rec, st, true
}

func (s *session) saveInterview(rec *studydb.Interview, st *interviewState) error {
	payload, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.progress.Scope().SaveInterview(rec.ID, payload)
}

// updateLive runs one mutation of the live interview under interviewMu, loading
// the row inside the lock so fn works from what is actually stored rather than
// from a copy another writer may have replaced. It reports whether there was a
// live interview to work on; fn saves what it changes, as the handlers always
// have.
func (s *session) updateLive(fn func(rec *studydb.Interview, st *interviewState) error) (bool, error) {
	s.interviewMu.Lock()
	defer s.interviewMu.Unlock()
	rec, st, err := s.liveInterview()
	if err != nil || rec == nil {
		return false, err
	}
	return true, fn(rec, st)
}

type interviewView struct {
	ID       int64
	Started  time.Time
	Finished *time.Time
	Live     bool
	// Finishing is true while the feedback is still being written, which is the
	// one state the report page has to say something about rather than show an
	// empty section.
	Finishing bool
	Problems  []interviewProblemView
	Report    string
	Minutes   int
}

type interviewProblemView struct {
	Index   int
	Total   int
	Title   string
	Slug    string
	Limit   int
	Minutes int
	Submits int
	Passed  bool
	Done    bool
	Current bool
	// Pattern is deliberately absent. The whole point of drawing at random is
	// that naming the approach is part of the exercise, so neither this page nor
	// the workspace may print it while the interview is live.
	Elapsed int // minutes on the clock, for the live problem
}

type interviewPage struct {
	common

	Live     *interviewView
	Past     []interviewView
	Groups   []catalog.PatternGroup
	Counts   []int
	Limits   []int
	Langs    []langChoice
	Pattern  string
	Problems int
	Limit    int
	Err      string
	Why      string // why there is no interviewer
}

func (s *session) interviewView(rec *studydb.Interview, st *interviewState, now time.Time) interviewView {
	v := interviewView{ID: rec.ID, Started: rec.Started, Finished: rec.Finished, Live: rec.Live(),
		Finishing: rec.Live() && st.Finishing, Report: st.Report}
	for i, ip := range st.Problems {
		title := ip.Slug
		if p, ok := s.cat.BySlug(ip.Slug); ok {
			title = fmt.Sprintf("%d. %s", p.ID, p.Title)
		}
		pv := interviewProblemView{
			Index: i + 1, Total: len(st.Problems), Title: title, Slug: ip.Slug,
			Limit: ip.Limit, Minutes: ip.Minutes, Submits: ip.Submits, Passed: ip.Passed,
			Done: ip.Closed != nil, Current: rec.Live() && i == st.Current,
		}
		if pv.Current && ip.Started != nil {
			pv.Elapsed = int(now.Sub(*ip.Started).Minutes())
		}
		v.Minutes += ip.Minutes
		v.Problems = append(v.Problems, pv)
	}
	return v
}

func (s *session) handleInterview(w http.ResponseWriter, r *http.Request) {
	s.renderInterview(w, "")
}

func (s *session) renderInterview(w http.ResponseWriter, msg string) {
	now := s.now()
	page := interviewPage{
		common:   s.common("Interview", "interview"),
		Groups:   s.cat.Groups(),
		Langs:    s.langChoices(),
		Counts:   []int{minInterviewProblems, maxInterviewProblems},
		Limits:   []int{10, 15, 20, 30, 45},
		Problems: minInterviewProblems,
		Limit:    defaultProblemLimit,
		Err:      msg,
	}
	if ok, why := s.tutor.Available(); !ok {
		page.Why = why
	}
	rec, st, err := s.liveInterview()
	if err != nil {
		s.fail(w, err)
		return
	}
	if rec != nil {
		v := s.interviewView(rec, st, now)
		page.Live = &v
	}
	past, err := s.progress.Scope().Interviews(10)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, p := range past {
		if rec != nil && p.ID == rec.ID {
			continue
		}
		var pst interviewState
		if json.Unmarshal(p.Payload, &pst) != nil {
			continue
		}
		page.Past = append(page.Past, s.interviewView(&p, &pst, now))
	}
	s.render(w, "interview", page)
}

// handleInterviewReport shows one finished interview.
func (s *session) handleInterviewReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r, "No interview with that id.")
		return
	}
	rec, err := s.progress.Scope().InterviewByID(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if rec == nil {
		s.notFound(w, r, "No interview with that id.")
		return
	}
	var st interviewState
	if err := json.Unmarshal(rec.Payload, &st); err != nil {
		s.fail(w, err)
		return
	}
	v := s.interviewView(rec, &st, s.now())
	s.render(w, "interview", interviewPage{
		common: s.common(fmt.Sprintf("Interview %d", rec.ID), "interview"),
		Live:   &v,
	})
}

// handleInterviewStart draws the problems and opens the first one.
func (s *session) handleInterviewStart(w http.ResponseWriter, r *http.Request) {
	if !limitForm(w, r) {
		return
	}
	if ok, why := s.tutor.Available(); !ok {
		s.renderInterview(w, "There is no interviewer on this instance: "+why)
		return
	}
	// Held through opening the first problem, so nothing else moves the row
	// between its creation and its first save.
	s.interviewMu.Lock()
	defer s.interviewMu.Unlock()
	if rec, _, err := s.liveInterview(); err != nil {
		s.fail(w, err)
		return
	} else if rec != nil {
		s.renderInterview(w, "An interview is already in progress. Finish it first.")
		return
	}

	count := clampAtoi(r.FormValue("problems"), minInterviewProblems, minInterviewProblems, maxInterviewProblems)
	limit := clampAtoi(r.FormValue("limit"), defaultProblemLimit, minProblemLimit, maxProblemLimit)
	pattern := r.FormValue("pattern")
	// The language comes from the button that was pressed, so starting an
	// interview is one click rather than a selector to arrange first. An
	// unnamed language is Go, which is what it means everywhere else here.
	l, err := lang.Parse(r.FormValue("lang"))
	if err != nil {
		s.renderInterview(w, err.Error())
		return
	}
	if why := s.RunUnavailable(l.ID); why != "" {
		s.renderInterview(w, "Tests cannot run in "+l.Label+" here: "+why)
		return
	}

	drawn, err := s.drawInterview(pattern, count, l)
	if err != nil {
		s.renderInterview(w, err.Error())
		return
	}
	st := &interviewState{Pattern: pattern, Lang: string(l.ID)}
	for _, p := range drawn {
		st.Problems = append(st.Problems, interviewProblem{
			Slug: p.Slug, Lang: string(l.ID), Limit: limit,
		})
	}
	payload, err := json.Marshal(st)
	if err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.progress.Scope().CreateInterview(s.now(), payload)
	if err != nil {
		s.fail(w, err)
		return
	}
	rec, err := s.progress.Scope().InterviewByID(id)
	if err != nil || rec == nil {
		s.fail(w, fmt.Errorf("the interview was created but cannot be read back: %v", err))
		return
	}
	if err := s.openInterviewProblem(rec, st); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/problems/"+st.Problems[st.Current].Slug+"/solve", http.StatusSeeOther)
}

// drawInterview picks the problems, without replacement and without telling
// anybody their pattern. The pool and the filter are the ones `progress` and
// `catalog` already define, so there is one answer to what may be drawn.
//
// Only problems that can actually be scaffolded in the chosen language are
// drawn. Checking here rather than when the problem opens is the difference
// between a message on this page and a live interview whose second problem
// cannot be started.
func (s *session) drawInterview(pattern string, count int, l lang.Lang) ([]catalog.Problem, error) {
	pool := catalog.MatchPattern(s.cat.Problems, pattern)
	if len(pool) == 0 {
		return nil, fmt.Errorf("no pattern matches that")
	}
	// Only Python insists on a stub: the Go scaffold writes "no signature on
	// file — write the entry point yourself" and carries on, where the Python
	// one refuses. So this filters nothing on a Go interview.
	workable := func(in []catalog.Problem) []catalog.Problem {
		if l.ID != lang.Python {
			return in
		}
		var out []catalog.Problem
		for _, p := range in {
			if signature, _ := s.inputs(p, l); strings.TrimSpace(signature) != "" {
				out = append(out, p)
			}
		}
		return out
	}
	candidates := workable(s.progress.DrillPool(pool, false))
	if len(candidates) < count {
		// Falling back to solved ones rather than refusing: an interview on a
		// problem you have solved before is a retrieval exercise, which is the
		// same reason Drill offered the choice.
		candidates = append(candidates, workable(s.progress.DrillPool(pool, true))...)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("nothing to draw from in that pool for %s — if that is unexpected, run `nc fetch`", l.Label)
	}
	var out []catalog.Problem
	for len(out) < count && len(candidates) > 0 {
		i := s.pick(len(candidates))
		out = append(out, candidates[i])
		candidates = append(candidates[:i], candidates[i+1:]...)
	}
	if len(out) < minInterviewProblems {
		return nil, fmt.Errorf("that pool holds only %d problems workable in %s; an interview needs %d",
			len(out), l.Label, minInterviewProblems)
	}
	return out, nil
}

// openInterviewProblem scaffolds the current problem and briefs the
// interviewer. The clock starts here, which is what the limit is measured from.
func (s *session) openInterviewProblem(rec *studydb.Interview, st *interviewState) error {
	ip := &st.Problems[st.Current]
	p, ok := s.cat.BySlug(ip.Slug)
	if !ok {
		return fmt.Errorf("the interview holds an unknown problem %q", ip.Slug)
	}
	l := lang.Resolve(st.Lang)
	signature, statement := s.inputs(p, l)
	lay := workspace.Interview(s.root, s.user.ID, rec.ID)
	// WithoutPattern: the generated header would otherwise name the pattern in
	// the first line of the file they are about to write in.
	if _, _, err := scaffold.Create(s.progress.Scope(), lay, scaffold.WithoutPattern(p), l, signature, statement); err != nil {
		return err
	}
	now := s.now()
	ip.Started = &now
	if err := s.saveInterview(rec, st); err != nil {
		return err
	}
	// Detached, like openLesson: starting an agent takes seconds and the reply
	// arrives over the event stream.
	//
	// SendAs with an empty display, not SendInternal: an internal turn is
	// published as internal-text, which the panel skips. The opening is the one
	// thing here that has to be read, and sending it privately is why the
	// interviewer appeared to say nothing at all.
	prompt := s.interviewOpeningPrompt(p, st, ip.Limit)
	go func() {
		sess, err := s.tutor.StartAs(s.progress.Scope(), p, tutor.RoleInterviewer)
		if err != nil || sess == nil {
			return // the panel reports an interviewer that will not start
		}
		_ = sess.SendAs(prompt, "")
	}()
	return nil
}

// interviewOpeningPrompt opens one problem. The first one opens the interview
// too, which is three beats rather than one: who is talking, how this will go,
// and only then the problem.
func (s *session) interviewOpeningPrompt(p catalog.Problem, st *interviewState, limit int) string {
	index, total := st.Current+1, len(st.Problems)
	var b strings.Builder
	if index == 1 {
		fmt.Fprintf(&b, "The interview starts now. There are %d problems and they have %d minutes "+
			"on each. They are writing %s.\n\n", total, limit, lang.Resolve(st.Lang).Label)
		b.WriteString("Say three things, in this order and in one short message:\n" +
			"1. Greet them in one line and say you will be interviewing them.\n" +
			"2. How this will go: the number of problems and the time each, that you will ask " +
			"for their approach before they write code, that there is a Run-tests button and a " +
			"Submit button on the page, that after a working solution you will add a " +
			"complication, and that you will not be giving hints. Thinking aloud helps you.\n" +
			"3. Hand them the first problem by name and ask how they intend to solve it.\n\n")
		fmt.Fprintf(&b, "The statement for %q is already on the screen in front of them, examples "+
			"and constraints included, so do not restate or summarise it.\n", p.Title)
	} else {
		fmt.Fprintf(&b, "That is problem %d of %d done. Move on to problem %d: %q. Its statement is "+
			"on the screen in front of them, examples and constraints included.\n\n", index-1, total, index, p.Title)
		b.WriteString("One short message: one line to move on, then ask how they intend to solve " +
			"this one. Do not restate the problem and do not repeat the format — they know it.\n")
		fmt.Fprintf(&b, "They have %d minutes on this one.\n", limit)
	}
	b.WriteString("\nDo not name the pattern, suggest a data structure, or hint at the approach.\n")
	b.WriteString("Do not mention the clock until the application tells you the time is up.")
	if followUp := s.followUpFor(p); followUp != "" {
		fmt.Fprintf(&b, "\n\nThe statement's own follow-up question, which they cannot see and you "+
			"may use as your complication once their solution works: %s", followUp)
	}
	return b.String()
}

// handleInterviewNext closes the current problem and opens the next, or
// finishes the interview when there is none.
func (s *session) handleInterviewNext(w http.ResponseWriter, r *http.Request) {
	if !limitForm(w, r) {
		return
	}
	var whereTo string
	found, err := s.updateLive(func(rec *studydb.Interview, st *interviewState) error {
		if err := s.advanceInterview(rec, st); err != nil {
			return err
		}
		whereTo = s.interviewWhereTo(rec, st)
		return nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		s.renderInterview(w, "There is no interview in progress.")
		return
	}
	http.Redirect(w, r, whereTo, http.StatusSeeOther)
}

// advanceInterview closes the current problem and opens the next, or starts
// finishing when there is none left.
//
// One path for both ways in: the person pressing Next, and the interviewer
// saying it is satisfied with a submission. The second has no request to answer,
// which is why this returns an error rather than writing a response.
func (s *session) advanceInterview(rec *studydb.Interview, st *interviewState) error {
	if st.Finishing {
		return nil
	}
	s.closeInterviewProblem(rec, st)
	if st.Current+1 >= len(st.Problems) {
		return s.startFinishingInterview(rec, st)
	}
	st.Current++
	return s.openInterviewProblem(rec, st)
}

// interviewWhereTo is the page that now shows what is happening.
func (s *session) interviewWhereTo(rec *studydb.Interview, st *interviewState) string {
	if st.Finishing || st.Current >= len(st.Problems) {
		return fmt.Sprintf("/interview/%d", rec.ID)
	}
	return "/problems/" + st.Problems[st.Current].Slug + "/solve"
}

// closeInterviewProblem banks the clock. An interview writes no rating, so this
// is the only thing it records against the problem itself.
func (s *session) closeInterviewProblem(rec *studydb.Interview, st *interviewState) {
	if st.Current >= len(st.Problems) {
		return
	}
	ip := &st.Problems[st.Current]
	if ip.Closed != nil {
		return
	}
	now := s.now()
	ip.Closed = &now
	if ip.Started != nil {
		if minutes, _ := progress.ClockMinutes(now.Sub(*ip.Started)); minutes > 0 {
			ip.Minutes = minutes
			if _, err := s.progress.AddMinutes(ip.Slug, minutes, now); err != nil {
				return
			}
		}
	}
	s.tutor.ResetAs(s.progress.Scope(), ip.Slug, tutor.RoleInterviewer)
	_ = s.saveInterview(rec, st)
}

// startFinishingInterview stamps the interview as finishing and writes the
// feedback in the background.
//
// The write is a model turn: it takes as long as it takes, and doing it inside
// the request left the browser on a dead page with nothing to read. The row
// stays live while it runs, so the report page can say what is happening and
// nothing can start a second interview meanwhile.
func (s *session) startFinishingInterview(rec *studydb.Interview, st *interviewState) error {
	st.Finishing = true
	st.Current = len(st.Problems)
	if err := s.saveInterview(rec, st); err != nil {
		return err
	}
	final := *st
	go func() {
		final.Report = s.interviewReport(&final)
		payload, err := json.Marshal(&final)
		if err != nil {
			log.Printf("interview %d: marshal report: %v", rec.ID, err)
			return
		}
		if err := s.progress.Scope().FinishInterview(rec.ID, s.now(), payload); err != nil {
			log.Printf("interview %d: save report: %v", rec.ID, err)
			return
		}
		for _, ip := range final.Problems {
			s.tutor.ResetAs(s.progress.Scope(), ip.Slug, tutor.RoleInterviewer)
		}
	}()
	return nil
}

// handleInterviewFinish ends it early, from the page.
func (s *session) handleInterviewFinish(w http.ResponseWriter, r *http.Request) {
	if !limitForm(w, r) {
		return
	}
	var id int64
	found, err := s.updateLive(func(rec *studydb.Interview, st *interviewState) error {
		id = rec.ID
		if st.Finishing {
			return nil
		}
		s.closeInterviewProblem(rec, st)
		return s.startFinishingInterview(rec, st)
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		s.renderInterview(w, "There is no interview in progress.")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/interview/%d", id), http.StatusSeeOther)
}

// handleInterviewState is what the page polls to notice the interview moving
// underneath it: the interviewer can close a problem itself, and the feedback
// arrives after the page that asked for it was already rendered.
func (s *session) handleInterviewState(w http.ResponseWriter, r *http.Request) {
	rec, st, err := s.liveInterview()
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"live": rec != nil}
	if rec == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["id"] = rec.ID
	out["total"] = len(st.Problems)
	out["finishing"] = st.Finishing
	if !st.Finishing && st.Current < len(st.Problems) {
		out["slug"] = st.Problems[st.Current].Slug
		out["index"] = st.Current + 1
	}
	writeJSON(w, http.StatusOK, out)
}

// interviewReport is the one assessment of the sitting. It is written from the
// notes the interviewer kept per problem rather than from a transcript, so it
// does not depend on any session still being alive.
func (s *session) interviewReport(st *interviewState) string {
	var notes strings.Builder
	for i, ip := range st.Problems {
		title := ip.Slug
		if p, ok := s.cat.BySlug(ip.Slug); ok {
			title = p.Title
		}
		fmt.Fprintf(&notes, "\nProblem %d: %s\n", i+1, title)
		fmt.Fprintf(&notes, "  time: %d of %d minutes", ip.Minutes, ip.Limit)
		if ip.Minutes > ip.Limit {
			notes.WriteString(" (over)")
		}
		fmt.Fprintf(&notes, "\n  submissions: %d, tests %s\n", ip.Submits, passedWord(ip.Passed))
		for _, n := range ip.Notes {
			notes.WriteString("  note: " + n + "\n")
		}
		if len(ip.Notes) == 0 {
			notes.WriteString("  note: nothing recorded — they submitted nothing on this one\n")
		}
	}
	prompt := "The interview is over. Write the feedback now, from your own notes below.\n" +
		"Follow the feedback section of tutor/INTERVIEW.md: what held up, what did not, " +
		"one or two things to practise, and no score.\n" +
		"Plain Markdown, a few short paragraphs. This is the whole output — do not ask a question.\n" +
		"=== YOUR NOTES ===\n" + notes.String()

	// Any of the problems' sessions can write it; the last one is the one with
	// the most recent conversation in it.
	ctx, cancel := context.WithTimeout(s.background, 5*time.Minute)
	defer cancel()
	for i := len(st.Problems) - 1; i >= 0; i-- {
		p, ok := s.cat.BySlug(st.Problems[i].Slug)
		if !ok {
			continue
		}
		sess, err := s.tutor.StartAs(s.progress.Scope(), p, tutor.RoleInterviewer)
		if err != nil || sess == nil {
			continue
		}
		answer, err := s.askTutorInternal(ctx, sess, prompt)
		if err == nil && strings.TrimSpace(answer) != "" {
			return strings.TrimSpace(answer)
		}
	}
	// The sitting still happened, so the numbers stand in for the words.
	return "The interviewer could not be reached for the write-up. What the sitting recorded:\n" + notes.String()
}

func passedWord(passed bool) string {
	if passed {
		return "passed"
	}
	return "did not pass"
}

// handleInterviewTimeUp is the browser reporting that a problem's limit has
// passed. The page keeps the clock; this spends the one turn in which the
// interviewer says so, and the flag is what stops a polling tab buying more.
func (s *session) handleInterviewTimeUp(w http.ResponseWriter, r *http.Request) {
	said := false
	_, err := s.updateLive(func(rec *studydb.Interview, st *interviewState) error {
		// A tab whose clock ran out after the interviewer closed the last
		// problem: the row is still live while the report is written, but no
		// problem is current, and there is nobody left to tell.
		if st.Finishing || st.Current >= len(st.Problems) {
			return nil
		}
		ip := &st.Problems[st.Current]
		if ip.TimeUp || ip.Started == nil || s.now().Sub(*ip.Started) < time.Duration(ip.Limit)*time.Minute {
			return nil
		}
		ip.TimeUp = true
		if err := s.saveInterview(rec, st); err != nil {
			return err
		}
		p, ok := s.cat.BySlug(ip.Slug)
		if !ok {
			return nil
		}
		said = true
		// Public, like the opening: this one is meant to be read.
		go func(limit int) {
			sess := s.tutor.GetAs(s.progress.Scope(), p.Slug, tutor.RoleInterviewer)
			if sess == nil {
				return
			}
			_ = sess.SendAs(fmt.Sprintf(
				"The %d minutes for this problem have passed. Say so once, in one or two lines, and "+
					"offer them the choice of finishing this problem or moving on. Do not hurry them "+
					"and do not mention the clock again.", limit), "")
		}(ip.Limit)
		return nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"said": said})
}

// interviewDecision is what the interviewer answers a submission with.
//
// The same shape as the submission flow's decision, and for the same reason:
// the reply has to be machine-read to know whether the problem stays open, and
// a model asked for prose plus a verdict gives you prose.
type interviewDecision struct {
	Decision string `json:"decision"` // "followup" or "satisfied"
	Message  string `json:"message"`  // shown in the chat
	Note     string `json:"note"`     // kept for the final report
}

// submitToInterview records a submission and asks the interviewer what to do
// with it. The decision turn runs detached, like the submission flow's: it
// takes seconds and the answer arrives over the event stream.
//
// The caller saw this problem as the interview's current one before the tests
// ran; that is checked again under the lock, because the interviewer may have
// closed it in between.
func (s *session) submitToInterview(p catalog.Problem, code, output string, passed bool) error {
	found, err := s.updateLive(func(rec *studydb.Interview, st *interviewState) error {
		if st.Finishing || st.Current >= len(st.Problems) || st.Problems[st.Current].Slug != p.Slug {
			return fmt.Errorf("the interview has moved on from %s", p.Slug)
		}
		ip := &st.Problems[st.Current]
		ip.Submits++
		ip.Passed = passed
		if err := s.saveInterview(rec, st); err != nil {
			return err
		}
		sess, err := s.tutor.StartAs(s.progress.Scope(), p, tutor.RoleInterviewer)
		if err != nil {
			return err
		}
		if sess == nil {
			return fmt.Errorf("no interviewer session for %s", p.Slug)
		}
		prompt := s.interviewDecisionPrompt(p, lang.Resolve(st.Lang).Fence, code, output, passed, ip.Submits)
		go s.decideInterviewSubmission(rec.ID, st.Current, p, sess, prompt, passed)
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("the interview ended before the submission reached it")
	}
	return nil
}

func (s *session) interviewDecisionPrompt(p catalog.Problem, fence, code, output string, passed bool, submits int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "They have submitted a solution to %q. This is their submission number %d on this problem.\n", p.Title, submits)
	if passed {
		b.WriteString("The tests passed.\n")
	} else {
		b.WriteString("The tests did not pass.\n")
	}
	b.WriteString("\n=== THEIR CODE ===\n```" + fence + "\n" + capInterview(code, 24_000) + "\n```\n")
	b.WriteString("\n=== TEST OUTPUT ===\n```\n" + capInterview(output, 8_000) + "\n```\n")
	b.WriteString(`
Answer with one JSON object and nothing else:

{"decision": "followup" | "satisfied", "message": "...", "note": "..."}

- "followup": you are asking one complicating question. Put it in "message".
- "satisfied": you have what you need on this problem. Put your closing line in
  "message" — one or two sentences, no score.
- "message" is shown to them as your reply, in Markdown, their language.
- "note" is for you: one or two sentences for the final write-up, naming what
  this submission showed. They never see it.

Do not correct their code, name the pattern, or give a hint in "message".`)
	return b.String()
}

// decideInterviewSubmission runs the decision turn and files its outcome.
//
// The note goes into the interview row rather than being left in the
// transcript, because the final report is written from the notes: the agent
// process may be gone by then, and a reply that was only ever in memory would
// take the evidence with it.
func (s *session) decideInterviewSubmission(id int64, index int, p catalog.Problem, sess *tutor.Session, prompt string, passed bool) {
	ctx, cancel := context.WithTimeout(s.background, 5*time.Minute)
	defer cancel()
	answer, err := s.askTutorInternal(ctx, sess, prompt)
	if err != nil {
		sess.PublishSubmissionQuestion("I could not read that just now. Say where you got to and we will carry on.")
		return
	}
	var d interviewDecision
	if raw := jsonObject(answer); raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	if strings.TrimSpace(d.Message) == "" {
		// A reply that did not parse is still a reply: show it rather than
		// swallowing the turn that was paid for.
		d.Message = strings.TrimSpace(answer)
	}
	if strings.TrimSpace(d.Message) == "" {
		d.Message = "Noted."
	}
	sess.PublishSubmissionQuestion(d.Message)

	_, err = s.updateLive(func(rec *studydb.Interview, st *interviewState) error {
		// The interview moved on or ended while the turn was in flight. Once it
		// is finishing, the report is being written from a copy already taken,
		// so a note added now would be overwritten by it anyway.
		if rec.ID != id || st.Finishing || index >= len(st.Problems) {
			return nil
		}
		note := strings.TrimSpace(d.Note)
		if note == "" {
			note = fmt.Sprintf("submission %d: tests %s, no note recorded",
				st.Problems[index].Submits, passedWord(passed))
		}
		st.Problems[index].Notes = append(st.Problems[index].Notes, note)
		if err := s.saveInterview(rec, st); err != nil {
			return err
		}
		// Satisfied means the interviewer is done with this problem, so it moves
		// on by itself rather than waiting to be clicked. The page is polling
		// /interview/state and follows.
		if d.Decision == "satisfied" && index == st.Current {
			return s.advanceInterview(rec, st)
		}
		return nil
	})
	if err != nil {
		log.Printf("interview %d: filing the interviewer's decision: %v", id, err)
	}
}

// jsonObject is the outermost {...} in a reply, which is how the submission
// flow already finds a decision inside whatever prose came with it.
func jsonObject(text string) string {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}

func capInterview(s string, limit int) string {
	if len(s) > limit {
		return s[:limit] + "\n…(truncated)"
	}
	return s
}

// clampAtoi reads a form number, falling back rather than failing: these come
// from a select, so a value outside the range is a tampered form, not a typo
// worth an error page.
func clampAtoi(raw string, fallback, lo, hi int) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return min(hi, max(lo, n))
}
