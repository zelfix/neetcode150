package progress

import (
	"testing"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/studydb"
)

func TestLastArchivedInPicksTheNewestOfThatLanguage(t *testing.T) {
	// Attempts come back oldest first, which is what makes "the last match
	// wins" the newest one.
	attempts := []studydb.Attempt{
		{ID: 1, Language: "go", Code: "first"},
		{ID: 2, Language: "python", Code: "python one"},
		{ID: 3, Language: "go", Code: "second"},
		{ID: 4, Language: "", Code: "before the language was recorded"},
	}

	cases := []struct {
		name     string
		attempts []studydb.Attempt
		want     lang.ID
		code     string
		found    bool
		matching int
		skipped  int
	}{
		{
			// The empty language reads as Go, which is what it was.
			name: "go takes the newest go attempt", attempts: attempts, want: lang.Go,
			code: "before the language was recorded", found: true, matching: 3, skipped: 1,
		},
		{
			name: "python takes the only python attempt", attempts: attempts, want: lang.Python,
			code: "python one", found: true, matching: 1, skipped: 3,
		},
		{
			name: "nothing archived", attempts: nil, want: lang.Go,
		},
		{
			name: "only the other language", attempts: attempts[1:2], want: lang.Go, skipped: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LastArchivedIn(c.attempts, c.want)
			if got.Found != c.found || got.Matching != c.matching || got.Skipped != c.skipped {
				t.Errorf("found=%v matching=%d skipped=%d; want %v, %d, %d",
					got.Found, got.Matching, got.Skipped, c.found, c.matching, c.skipped)
			}
			if got.Attempt.Code != c.code {
				t.Errorf("code = %q, want %q", got.Attempt.Code, c.code)
			}
		})
	}
}

// The interview draws from this pool, and `nc drill` used to. Started but
// unfinished counts as unsolved: leaving those out made a pool of twenty open
// problems report "nothing left".
func TestDrillPoolCountsAnAttemptedProblemAsUnsolved(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	problems := []catalog.Problem{
		{ID: 1, Slug: "two-sum", Dir: "0001-two-sum"},
		{ID: 2, Slug: "valid-anagram", Dir: "0002-valid-anagram"},
		{ID: 3, Slug: "group-anagrams", Dir: "0003-group-anagrams"},
	}
	if _, err := s.SetStatus("two-sum", Attempted, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus("valid-anagram", Solved, now); err != nil {
		t.Fatal(err)
	}

	slugs := func(got []catalog.Problem) []string {
		out := make([]string, 0, len(got))
		for _, p := range got {
			out = append(out, p.Slug)
		}
		return out
	}
	unsolved := slugs(s.DrillPool(problems, false))
	if len(unsolved) != 2 || unsolved[0] != "two-sum" || unsolved[1] != "group-anagrams" {
		t.Errorf("unsolved pool = %v, want the attempted one and the untouched one", unsolved)
	}
	if solved := slugs(s.DrillPool(problems, true)); len(solved) != 1 || solved[0] != "valid-anagram" {
		t.Errorf("solved pool = %v, want just valid-anagram", solved)
	}
	// Shaky is solved-but-insecure, which is still a candidate for revision.
	if _, err := s.SetStatus("group-anagrams", Shaky, now); err != nil {
		t.Fatal(err)
	}
	if solved := slugs(s.DrillPool(problems, true)); len(solved) != 2 {
		t.Errorf("solved pool = %v, want the shaky one in it", solved)
	}
}
