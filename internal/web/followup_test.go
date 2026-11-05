package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"neetcode150/internal/content"
)

func TestPostAttemptChatKeepsTheRatingAndTimerUnchanged(t *testing.T) {
	srv, root, prog := testSite(t)
	p, _ := srv.cat.BySlug("two-sum")
	advice := "Consider a map size hint when most entries will be inserted; an early return can leave that space unused."
	ref, err := srv.recordSubmission(p, pendingSubmission{Code: "reviewed-code-marker", Passed: true}, tutorEvaluation{Confidence: 4, Feedback: "Independent reasoning.", Followup: advice})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := prog.DB().Attempt(ref)
	if err != nil {
		t.Fatal(err)
	}
	var report submissionReport
	if err := json.Unmarshal(attempt.Review, &report); err != nil || report.Followup != advice {
		t.Fatal("follow-up was not persisted")
	}
	before := prog.Get(p.Slug)
	const script = `
while IFS= read -r line; do
 case "$line" in
  *'post-attempt learning conversation'*'reviewed-code-marker'*) ;;
  *) exit 1 ;;
 esac
 echo '{"type":"assistant","message":{"content":[{"type":"text","text":"An initial-space hint is optional; let us discuss the trade-off."}]}}'
 echo '{"type":"result","subtype":"success"}'
done
`
	for i := 0; i < 2; i++ {
		fakeTutor(t, srv, script)
		body := get(t, srv.Handler(), "/problems/two-sum/solve").Body.String()
		if !strings.Contains(body, "data-tutor-input") || !strings.Contains(body, "Continue discussing your solution") || !strings.Contains(body, advice) {
			t.Fatal("completed attempt should offer a chat and retained follow-up")
		}
		question := "Is an initial space hint useful here?"
		rec := postJSON(t, srv.Handler(), "/problems/two-sum/tutor", tutorSendRequest{Text: question})
		if rec.Code != http.StatusOK {
			t.Fatalf("follow-up: %d %s", rec.Code, rec.Body.String())
		}
		events := readSSE(t, srv.Handler(), "/problems/two-sum/tutor/events", (i+1)*3)
		user, reply := 0, 0
		for _, e := range events {
			if e["kind"] == "user" {
				user++
				if e["text"] != question {
					t.Fatal("private follow-up instructions appeared in the chat")
				}
			}
			if e["kind"] == "text" {
				reply++
				if !strings.Contains(e["text"].(string), "optional") {
					t.Fatal("missing conversational answer")
				}
			}
		}
		if user != i+1 || reply != i+1 {
			t.Fatalf("got %d user turns and %d replies", user, reply)
		}
		if after := prog.Get(p.Slug); !reflect.DeepEqual(before, after) {
			t.Fatal("follow-up changed the rating, time, or schedule")
		}
		attempts, err := prog.DB().Attempts(p.Slug)
		if err != nil || len(attempts) != 1 {
			t.Fatal("follow-up created another attempt")
		}
		pending, err := prog.DB().Pending()
		if err != nil || len(pending) != 0 {
			t.Fatal("follow-up restarted assessment")
		}
		srv.CloseTutor()
		// Reopen the application, not just the chat, to exercise persisted context.
		srv, err = New(root, srv.cat, content.NewStore(root, content.NewRenderer()), prog)
		if err != nil {
			t.Fatal(err)
		}
	}
	srv.CloseTutor()
}
