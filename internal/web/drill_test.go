package web

import (
	"strings"
	"testing"

	"neetcode150/internal/catalog"
	"neetcode150/internal/progress"
)

// The whole value of a drill is that the pattern is not handed over, so this is
// the assertion that matters most on this page.
func TestDrillWithholdsThePattern(t *testing.T) {
	srv, _, _ := testSite(t)
	srv.pick = func(int) int { return 0 }
	h := srv.Handler()

	body := get(t, h, "/drill/").Body.String()
	if !strings.Contains(body, "Contains Duplicate") {
		t.Fatalf("the first candidate was not drawn:\n%s", body)
	}
	// The pattern select names every pattern, so only the card is checked.
	at := strings.Index(body, "drill-card")
	if at < 0 {
		t.Fatal("no drill card was rendered")
	}
	for _, spoiler := range []string{"Arrays &amp; Hashing", "Arrays & Hashing", "arrays-hashing"} {
		if strings.Contains(body[at:], spoiler) {
			t.Errorf("the drawn card gives away the pattern (%q)", spoiler)
		}
	}
}

func TestDrillFiltersByPatternAndPool(t *testing.T) {
	srv, _, prog := testSite(t)
	srv.pick = func(int) int { return 0 }
	h := srv.Handler()

	body := get(t, h, "/drill/?pattern=two-pointers").Body.String()
	if !strings.Contains(body, "Valid Palindrome") {
		t.Error("a pattern filter did not narrow the draw to that pattern")
	}

	// Nothing is solved yet, so the retrieval pool is empty and says so.
	if body := get(t, h, "/drill/?solved=1").Body.String(); !strings.Contains(body, "Nothing solved yet") {
		t.Error("an empty solved pool does not say why nothing was drawn")
	}

	if _, err := prog.SetStatus("two-sum", progress.Solved, srv.now()); err != nil {
		t.Fatal(err)
	}
	if body := get(t, h, "/drill/?solved=1").Body.String(); !strings.Contains(body, "Two Sum") {
		t.Error("a solved problem is not drawn for retrieval practice")
	}
	// And it leaves the unsolved pool.
	if body := get(t, h, "/drill/?solved=1&pattern=two-pointers").Body.String(); !strings.Contains(body, "Nothing solved yet") {
		t.Error("the solved pool ignored the pattern filter")
	}

	if body := get(t, h, "/drill/?pattern=nonsense").Body.String(); !strings.Contains(body, "No pattern matches") {
		t.Error("an unmatched pattern does not say so")
	}
}

func TestDrillPoolTreatsStartedWorkAsUnsolved(t *testing.T) {
	srv, _, prog := testSite(t)
	now := srv.now()
	if _, err := prog.SetStatus("two-sum", progress.Attempted, now); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.SetStatus("valid-palindrome", progress.Shaky, now); err != nil {
		t.Fatal(err)
	}

	unsolved := prog.DrillPool(srv.cat.Problems, false)
	if !containsSlug(unsolved, "two-sum") {
		t.Error("an attempted problem left the unsolved pool; started is not finished")
	}
	if containsSlug(unsolved, "valid-palindrome") {
		t.Error("a shaky problem is in the unsolved pool")
	}

	solved := prog.DrillPool(srv.cat.Problems, true)
	if !containsSlug(solved, "valid-palindrome") {
		t.Error("a shaky problem is not available for retrieval practice")
	}
	if containsSlug(solved, "two-sum") {
		t.Error("an attempted problem is in the solved pool")
	}
}

func containsSlug(problems []catalog.Problem, slug string) bool {
	for _, p := range problems {
		if p.Slug == slug {
			return true
		}
	}
	return false
}
