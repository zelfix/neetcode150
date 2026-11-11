package progress

import (
	"testing"

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
