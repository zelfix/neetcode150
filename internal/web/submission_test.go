package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/content"
	"neetcode150/internal/progress"
	"neetcode150/internal/studydb"
	"neetcode150/internal/tutor"
	"neetcode150/internal/workspace"
)

func TestSubmissionAnswerProducesOneVisibleReply(t *testing.T) {
	srv, _, prog := testSite(t)
	fakeTutor(t, srv, `
n=0
while IFS= read -r line; do
  n=$((n + 1))
  case "$n" in
    1) echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Explain what your stored entries mean."}]}}' ;;
    2) case "$line" in
         *'my first explanation'*) echo '{"type":"assistant","message":{"content":[{"type":"text","text":"{\"decision\":\"ask\",\"question\":\"What does one entry represent?\"}"}]}}' ;;
         *) exit 1 ;;
       esac ;;
    3) case "$line" in
         *'my clarified explanation'*) echo '{"type":"assistant","message":{"content":[{"type":"text","text":"{\"decision\":\"rate\",\"confidence\":4,\"rung\":0,\"feedback\":\"You clarified the entry meaning independently. Keep that explanation in your notes.\",\"rationale\":\"Comfortable understanding after clarification.\"}"}]}}' ;;
         *) exit 1 ;;
       esac ;;
    *) exit 1 ;;
  esac
  echo '{"type":"result","subtype":"success"}'
done
`)
	p, _ := srv.cat.BySlug("two-sum")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ownerSession(t, srv).askTutorAs(ctx, p, "begin follow-up", "Submit"); err != nil {
		t.Fatal(err)
	}
	sess := srv.tutor.Get(prog.Scope(), p.Slug)
	replay, events, stop := sess.Subscribe()
	defer stop()
	srv.pendingSubmissions[submissionKey{studydb.OwnerID, p.Slug}] = pendingSubmission{
		Code: "package solution\n", Passed: true, Entry: prog.Get(p.Slug),
	}
	if rec := postJSON(t, srv.Handler(), "/problems/two-sum/tutor", tutorSendRequest{Text: "  "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty answer: got status %d", rec.Code)
	}
	for i, reply := range []string{"my first explanation", "my clarified explanation"} {
		rec := postJSON(t, srv.Handler(), "/problems/two-sum/tutor", tutorSendRequest{Text: reply})
		if rec.Code != http.StatusOK {
			t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
		}
		want := tutor.KindSubmissionQuestion
		if i == 1 {
			want = tutor.KindSubmissionDone
		}
		answered := false
		for !answered {
			select {
			case event := <-events:
				switch event.Kind {
				case tutor.KindText:
					t.Fatalf("unexpected public model reply: %s", event.Text)
				case tutor.KindSubmissionQuestion, tutor.KindSubmissionDone:
					if event.Kind != want || strings.Contains(event.Text, `"decision"`) {
						t.Fatalf("unexpected reply: %+v", event)
					}
					answered = true
				case tutor.KindError, tutor.KindExit:
					t.Fatalf("tutor failed: %+v", event)
				}
			case <-ctx.Done():
				t.Fatal("timed out waiting for submission reply")
			}
		}
	}
	transcript := sess.Transcript()[len(replay):]
	users, replies := 0, 0
	for _, event := range transcript {
		if event.Kind == tutor.KindUser {
			users++
		}
		if event.Kind == tutor.KindText || event.Kind == tutor.KindSubmissionQuestion || event.Kind == tutor.KindSubmissionDone {
			replies++
		}
	}
	if users != 2 || replies != 2 {
		t.Fatalf("replay has %d user messages and %d visible replies", users, replies)
	}
	if got := prog.Get(p.Slug); len(got.History) != 1 || got.History[0].Confidence != 4 {
		t.Fatalf("rating not saved once: %+v", got)
	}
}

func TestLearningEvidenceSurvivesANewSessionWithoutArchivedCode(t *testing.T) {
	srv, _, prog := testSite(t)
	p, _ := srv.cat.BySlug("two-sum")
	pending := pendingSubmission{Code: "ARCHIVED_CODE_MUST_STAY_PRIVATE", Passed: true}
	summary := "Explained the entry meaning and complexity confidently; asked only Go syntax questions."
	archive, err := ownerSession(t, srv).recordSubmission(p, pending, tutorEvaluation{Confidence: 4, Rung: 0, Feedback: "Sound understanding.", LearningSummary: summary})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := prog.Scope().Attempt(archive)
	data := attempt.Review
	if err != nil {
		t.Fatal(err)
	}
	var saved submissionReport
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if body := get(t, srv.Handler(), "/problems/two-sum/solve").Body.String(); !strings.Contains(body, "Latest tutor feedback") || !strings.Contains(body, "Sound understanding.") {
		t.Fatal("saved tutor feedback missing after page reload")
	}
	if saved.LearningSummary != summary {
		t.Fatalf("summary not persisted: %+v", saved)
	}
	for _, prompt := range []string{ownerSession(t, srv).openingPrompt(p), ownerSession(t, srv).submissionDecisionPrompt(p, pendingSubmission{}, "new conversation")} {
		if !strings.Contains(prompt, summary) {
			t.Fatal("prior understanding missing from new session")
		}
		if strings.Contains(prompt, pending.Code) {
			t.Fatal("archived code leaked into prompt")
		}
	}
	if prompt := ownerSession(t, srv).openingPrompt(p); !strings.Contains(prompt, "Welcome them back") || strings.Contains(prompt, "then ask them to restate") {
		t.Fatal("repeat solve still forces introductory questions")
	}
	// Reports written before learning summaries existed still provide feedback.
	legacy := `{"confidence":4,"rung":0,"feedback":"Earlier explanation was clear."}`
	if err := func() error {
		_, err := prog.DB().SQL.Exec("UPDATE attempts SET review=? WHERE id=?", legacy, attempt.ID)
		return err
	}(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ownerSession(t, srv).openingPrompt(p), "Earlier explanation was clear.") {
		t.Fatal("legacy feedback lost")
	}
}

func TestSubmitCanRateWithoutAnObligatoryQuestion(t *testing.T) {
	srv, _, prog := testSite(t)
	p, _ := srv.cat.BySlug("two-sum")
	dir := workspace.Owner(srv.root).Dir(p)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "solution.go"), []byte("package solution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fakeTutor(t, srv, `
while IFS= read -r line; do
 echo '{"type":"assistant","message":{"content":[{"type":"text","text":"{\"decision\":\"rate\",\"confidence\":4,\"rung\":0,\"feedback\":\"You solved it independently.\",\"rationale\":\"Understanding is already demonstrated.\",\"learningSummary\":\"Complexity already explained confidently.\"}"}]}}'
 echo '{"type":"result","subtype":"success"}'
done
`)
	srv.runTests = runnerFunc(func(context.Context, runRequest) (testRun, error) {
		return testRun{Output: "PASS", OK: true, Ran: true, Passed: 1}, nil
	})
	rec := postJSON(t, srv.Handler(), "/problems/two-sum/submit", saveRequest{Code: "package solution\n"})
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}
	sess := srv.tutor.Get(prog.Scope(), "two-sum")
	replay, events, stop := sess.Subscribe()
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var e tutor.Event
		if len(replay) > 0 {
			e = replay[0]
			replay = replay[1:]
		} else {
			select {
			case e = <-events:
			case <-ctx.Done():
				t.Fatal("rating timed out")
			}
		}
		switch e.Kind {
		case tutor.KindSubmissionDone:
			return
		case tutor.KindSubmissionQuestion, tutor.KindText, tutor.KindError, tutor.KindExit:
			t.Fatalf("unexpected public reply: %+v", e)
		}
	}
}

func TestLatestFeedbackUsesTheNewestOfManySameDayAttempts(t *testing.T) {
	srv, _, _ := testSite(t)
	p, _ := srv.cat.BySlug("two-sum")
	for i := 0; i < 12; i++ {
		review := tutorEvaluation{Confidence: 3, Feedback: "Old feedback asking a question."}
		if i == 11 {
			review = tutorEvaluation{Confidence: 4, Feedback: "Newest feedback: independent understanding."}
		}
		if _, err := ownerSession(t, srv).recordSubmission(p, pendingSubmission{Code: "archived attempt", Passed: true}, review); err != nil {
			t.Fatal(err)
		}
	}
	body := get(t, srv.Handler(), "/problems/two-sum/solve").Body.String()
	_, after, ok := strings.Cut(body, `aria-label="Latest tutor feedback"`)
	if !ok {
		t.Fatal("missing latest feedback")
	}
	card, _, _ := strings.Cut(after, "</section>")
	if !strings.Contains(card, "4/5") || !strings.Contains(card, "Newest feedback") || strings.Contains(card, "Old feedback") {
		t.Fatalf("wrong latest feedback: %s", card)
	}
}

func TestCompletedReviewIsAtomicAndIdempotent(t *testing.T) {
	srv, _, prog := testSite(t)
	p, _ := srv.cat.BySlug("two-sum")
	pending := pendingSubmission{Token: "unique-submission", Code: "submitted snapshot", Passed: true, Elapsed: 3 * time.Minute}
	srv.submissionMu.Lock()
	err := ownerSession(t, srv).persistPendingLocked(p.Slug, pending)
	srv.submissionMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prog.DB().SQL.Exec(`CREATE TRIGGER fail_progress BEFORE INSERT ON progress BEGIN SELECT RAISE(ABORT,'simulated failure'); END`); err != nil {
		t.Fatal(err)
	}
	review := tutorEvaluation{Confidence: 4, Feedback: "Understood independently."}
	if _, err := ownerSession(t, srv).recordSubmission(p, pending, review); err == nil {
		t.Fatal("transaction should fail")
	}
	var attempts, pendingCount int
	if err := prog.DB().SQL.QueryRow("SELECT count(*) FROM attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := prog.DB().SQL.QueryRow("SELECT count(*) FROM pending_submissions").Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || pendingCount != 1 || len(prog.Get(p.Slug).History) != 0 {
		t.Fatal("partial review escaped transaction rollback")
	}
	if _, err := prog.DB().SQL.Exec("DROP TRIGGER fail_progress"); err != nil {
		t.Fatal(err)
	}
	first, err := ownerSession(t, srv).recordSubmission(p, pending, review)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ownerSession(t, srv).recordSubmission(p, pending, review)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatal("retry created a second attempt")
	}
	if entry := prog.Get(p.Slug); len(entry.History) != 1 || entry.MinutesSpent != 3 {
		t.Fatalf("duplicate rating/time: %+v", entry)
	}
	if err := prog.DB().SQL.QueryRow("SELECT count(*) FROM pending_submissions").Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 0 {
		t.Fatal("completed submission still pending")
	}
}

func TestPendingAssessmentCanResumeAfterServerRestart(t *testing.T) {
	srv, root, prog := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	p, _ := srv.cat.BySlug("two-sum")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ownerSession(t, srv).askTutorAs(ctx, p, "opening question", "Submit"); err != nil {
		t.Fatal(err)
	}
	pending := pendingSubmission{Token: "resumed-assessment", Code: "pending submitted source", Passed: true, Checking: true}
	srv.submissionMu.Lock()
	err := ownerSession(t, srv).persistPendingLocked(p.Slug, pending)
	srv.submissionMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	srv.CloseTutor()
	reopened, err := progress.Load(filepath.Join(root, "study.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	next, err := New(root, srv.cat, content.NewStore(root, content.NewRenderer()), reopened)
	if err != nil {
		t.Fatal(err)
	}
	fakeTutor(t, next, `
while IFS= read -r line; do
 echo '{"type":"assistant","message":{"content":[{"type":"text","text":"{\"decision\":\"rate\",\"confidence\":4,\"rung\":0,\"feedback\":\"Independent reasoning.\",\"rationale\":\"Enough evidence.\"}"}]}}'
 echo '{"type":"result","subtype":"success"}'
done
`)
	restored := next.pendingSubmissions[submissionKey{studydb.OwnerID, p.Slug}]
	if restored.Code != pending.Code || restored.Checking {
		t.Fatal("pending state not restored for retry")
	}
	if next.tutor.Get(next.db.Owner(), p.Slug) == nil {
		t.Fatal("conversation was lost")
	}
	rec := postJSON(t, next.Handler(), "/problems/two-sum/tutor", tutorSendRequest{Text: "My independent explanation."})
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body.String())
	}
	sess := next.tutor.Get(next.db.Owner(), p.Slug)
	replay, ch, stop := sess.Subscribe()
	defer stop()
	for {
		var event tutor.Event
		if len(replay) > 0 {
			event = replay[0]
			replay = replay[1:]
		} else {
			select {
			case event = <-ch:
			case <-ctx.Done():
				t.Fatal("resumed evaluation timed out")
			}
		}
		if event.Kind == tutor.KindSubmissionDone {
			break
		}
		if event.Kind == tutor.KindError {
			t.Fatalf("evaluation failed: %+v", event)
		}
	}
	if entry := prog.Get(p.Slug); len(entry.History) != 1 || entry.Confidence != 4 {
		t.Fatal("resumed rating was not committed")
	}
	saved, err := reopened.Scope().Pending()
	if err != nil || len(saved) != 0 {
		t.Fatal("completed submission remains pending")
	}
	next.CloseTutor()
}
