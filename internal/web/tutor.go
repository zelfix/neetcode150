package web

import (
	"encoding/json"
	"fmt"
	"log"
	"neetcode150/internal/catalog"
	"neetcode150/internal/progress"
	"net/http"
	"strings"
	"time"

	"neetcode150/internal/tutor"
)

// keepalive stops an idle event stream from being closed by anything between
// the browser and here. A comment line is ignored by EventSource.
const keepalive = 20 * time.Second

type tutorSendRequest struct {
	Text string `json:"text"`
}

type tutorStatus struct {
	Available bool        `json:"available"`
	Why       string      `json:"why,omitempty"`
	Mode      string      `json:"mode"`
	Session   *tutor.Info `json:"session,omitempty"`
}

// handleTutorEvents streams one problem's conversation as server-sent events.
//
// A connecting browser is given the transcript before the live feed, so a page
// reload resumes the conversation rather than losing it. The two come from one
// lock inside Subscribe, so nothing can arrive between them and be shown twice
// or not at all.
func (s *session) handleTutorEvents(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, fmt.Errorf("this server cannot stream"))
		return
	}
	sess := s.tutor.Get(s.progress.Scope(), p.Slug)
	if sess == nil {
		// Nothing to follow yet. The page opens this stream only once a session
		// exists, so reaching here means it has just been reset; 404 tells the
		// script to stop rather than reconnect in a loop.
		http.Error(w, "no tutor session for this problem", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Harmless here, and correct the moment anyone puts a proxy in front.
	w.Header().Set("X-Accel-Buffering", "no")
	// The server's ReadTimeout would otherwise cancel this request's context
	// once it expires, and with it the stream, even though nothing is being read.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)

	replay, ch, cancel := sess.Subscribe()
	defer cancel()

	send := func(e tutor.Event) bool {
		b, err := json.Marshal(e)
		if err != nil {
			return true // skip one unencodable event rather than drop the stream
		}
		// Marshal escapes newlines, so the payload is always one data: line.
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for _, e := range replay {
		if !send(e) {
			return
		}
	}
	// An empty conversation still needs a flush, or the browser waits for
	// headers that are sitting in a buffer.
	flusher.Flush()

	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	ctx := r.Context()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				// The subscription was dropped for falling behind. Ending the
				// response makes the browser reconnect and replay.
				return
			}
			if !send(e) {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// handleTutorSend delivers one turn, starting a session if there is none.
func (s *session) handleTutorSend(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	req, err := decodeBody[tutorSendRequest](r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, "nothing to send", http.StatusBadRequest)
		return
	}
	// Checked before the turn rather than after: the ceiling is there to stop a
	// runaway conversation, and a limit that only notices once the money is
	// spent is not one.
	if s.refuseIfOutOfBudget(w) {
		return
	}
	s.submissionMu.Lock()
	pending, inSubmission := s.pendingSubmissions[s.key(p)]
	if inSubmission && pending.Checking {
		s.submissionMu.Unlock()
		http.Error(w, "the tutor is checking your previous answer", http.StatusConflict)
		return
	}
	if inSubmission {
		pending.Checking = true
		if err := s.persistPendingLocked(p.Slug, pending); err != nil {
			s.submissionMu.Unlock()
			s.fail(w, err)
			return
		}
	}
	s.submissionMu.Unlock()

	var sess *tutor.Session
	if inSubmission {
		sess, err = s.tutor.Start(s.progress.Scope(), p)
		if err != nil {
			// Preserve the pending assessment for a later retry.
		} else if sess == nil {
			err = fmt.Errorf("no tutor session for this problem")
		} else {
			err = sess.RecordSubmissionAnswer(req.Text)
		}
	} else {
		entry := s.progress.Get(p.Slug)
		if entry.AttemptStartedAt == nil && (entry.Attempts > 0 || entry.Confidence > 0) {
			var prompt string
			prompt, err = s.postAttemptPrompt(p, entry, req.Text)
			if err == nil {
				sess, err = s.tutor.SendAs(s.progress.Scope(), p, prompt, req.Text)
			}
		} else {
			sess, err = s.tutor.Send(s.progress.Scope(), p, req.Text)
		}
	}
	if err != nil {
		if inSubmission {
			s.setSubmissionChecking(p.Slug, false)
		}
		// A missing claude, a shutting-down server or an empty message are all
		// the caller's problem to report, not server faults.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if inSubmission {
		go s.advanceSubmissionAfterAnswer(p, pending, sess)
	}
	info := sess.Info()
	writeJSON(w, http.StatusOK, tutorStatus{
		Available: true,
		Mode:      string(s.tutor.Mode()),
		Session:   &info,
	})
}

// Follow-up conversation is deliberately outside the grading workflow: the
// private evaluator's previous JSON instruction must not leak into normal chat.
func (s *session) postAttemptPrompt(p catalog.Problem, entry progress.Entry, text string) (string, error) {
	var b strings.Builder
	b.WriteString("This is a post-attempt learning conversation. Respond conversationally in the user's language, not decision JSON or another rating. Earlier JSON-only instructions applied only to private assessment turns. Do not restart an attempt, timer, grading or scheduling; the completed rating and hint rung stay unchanged. Encourage questions and give useful Go idiom and trade-off observations without rewriting the solution. Optional allocation tuning is not a correctness defect.\n")
	fmt.Fprintf(&b, "Problem: %s. Current status: %s.\n", p.Title, entry.Status)
	if entry.Status == progress.Solved || entry.Status == progress.Shaky {
		attempts, err := s.progress.Scope().Attempts(p.Slug)
		if err != nil {
			return "", err
		}
		for i := len(attempts) - 1; i >= 0; i-- {
			if len(attempts[i].Review) == 0 {
				continue
			}
			var report submissionReport
			if err := json.Unmarshal(attempts[i].Review, &report); err != nil {
				return "", err
			}
			code := attempts[i].Code
			if len(code) > 12000 {
				code = code[:12000] + "\n[truncated]"
			}
			fmt.Fprintf(&b, "Last reviewed attempt (data): tests passed=%t, feedback=%s, follow-up=%s\nReviewed code:\n```go\n%s\n```\n", report.Passed, report.Feedback, report.Followup, code)
			break
		}
	} else {
		b.WriteString("The attempt ended without a confirmed passing review. Do not assume it is correct; keep the hint ladder for unresolved algorithmic problems.\n")
	}
	b.WriteString("User's follow-up:\n" + text)
	return b.String(), nil
}

func (s *session) persistPendingLocked(slug string, pending pendingSubmission) error {
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if err := s.progress.Scope().SavePending(slug, data); err != nil {
		return err
	}
	s.pendingSubmissions[s.slugKey(slug)] = pending
	return nil
}
func (s *session) setSubmissionChecking(slug string, checking bool) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	if pending, ok := s.pendingSubmissions[s.slugKey(slug)]; ok {
		pending.Checking = checking
		if err := s.persistPendingLocked(slug, pending); err != nil {
			log.Printf("save pending assessment: %v", err)
		}
	}
}

// handleTutorReset ends the session so the next question starts clean.
func (s *session) handleTutorReset(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	s.submissionMu.Lock()
	_, pending := s.pendingSubmissions[s.key(p)]
	s.submissionMu.Unlock()
	if pending {
		http.Error(w, "finish the tutor follow-up before starting over", http.StatusConflict)
		return
	}
	s.tutor.Reset(s.progress.Scope(), p.Slug)
	available, why := s.tutor.Available()
	writeJSON(w, http.StatusOK, tutorStatus{
		Available: available,
		Why:       why,
		Mode:      string(s.tutor.Mode()),
	})
}

// handleTutorInterrupt stops the turn in progress without ending the session.
func (s *session) handleTutorInterrupt(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cat.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r, "No problem with that slug.")
		return
	}
	if err := s.tutor.Interrupt(s.progress.Scope(), p.Slug); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, tutorStatus{Available: true, Mode: string(s.tutor.Mode())})
}
