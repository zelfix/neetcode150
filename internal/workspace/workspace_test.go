package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
)

var twoSum = catalog.Problem{ID: 1, Slug: "two-sum", Dir: "0001-two-sum"}

// An interview scaffolds its problems blank, so it must not land anywhere near
// the study files — including the owner's, who has no _work directory otherwise.
func TestInterviewFilesStayOutOfTheStudyTree(t *testing.T) {
	root := filepath.Join("/tmp", "project")
	for _, userID := range []int64{studydb.OwnerID, 7} {
		lay := Interview(root, userID, 3)
		got := lay.PkgDir(twoSum)
		if strings.HasPrefix(got, solutions) {
			t.Errorf("interview PkgDir = %q, which is inside the study tree", got)
		}
		if !strings.Contains(got, "interview-3") {
			t.Errorf("interview PkgDir = %q, want the interview id in it", got)
		}
		if got == For(root, userID).PkgDir(twoSum) {
			t.Errorf("interview and study layouts agree on %q", got)
		}
		if !strings.HasPrefix(got, guests) {
			t.Errorf("interview PkgDir = %q, want it under %q so ./... skips it", got, guests)
		}
	}
	// Two interviews are two directories, or the second would resume the first.
	if a, b := Interview(root, 7, 1).PkgDir(twoSum), Interview(root, 7, 2).PkgDir(twoSum); a == b {
		t.Errorf("interviews 1 and 2 share %q", a)
	}
}

// There are two study path shapes, so every assertion here is made twice: a bug
// that only reaches guests is the one this package exists to prevent.
func TestOwnerKeepsSolutionsAndGuestsGetTheirOwn(t *testing.T) {
	root := filepath.Join("/tmp", "project")
	owner := Owner(root)
	guest := For(root, 7)

	if got, want := owner.PkgDir(twoSum), filepath.Join("solutions", "0001-two-sum"); got != want {
		t.Errorf("owner PkgDir = %q, want %q", got, want)
	}
	if got, want := guest.PkgDir(twoSum), filepath.Join("_work", "7", "solutions", "0001-two-sum"); got != want {
		t.Errorf("guest PkgDir = %q, want %q", got, want)
	}
	if got := owner.Dir(twoSum); got != filepath.Join(root, "solutions", "0001-two-sum") {
		t.Errorf("owner Dir = %q", got)
	}
	if got := guest.Path(twoSum, "solution.go"); got != filepath.Join(root, "_work", "7", "solutions", "0001-two-sum", "solution.go") {
		t.Errorf("guest Path = %q", got)
	}
	if got, want := guest.Rel(twoSum, "solution.go"), "_work/7/solutions/0001-two-sum/solution.go"; got != want {
		t.Errorf("guest Rel = %q, want %q", got, want)
	}
}

// Owner() and For(root, OwnerID) must not disagree, or the CLI and the site
// would write to different directories for the same person.
func TestTheOwnerIdResolvesToTheOwnerLayout(t *testing.T) {
	if a, b := Owner("/r").PkgDir(twoSum), For("/r", studydb.OwnerID).PkgDir(twoSum); a != b {
		t.Errorf("Owner() = %q but For(OwnerID) = %q", a, b)
	}
}

// go test needs a slash-form relative pattern on every platform.
func TestPatternIsASlashPathTheGoToolAccepts(t *testing.T) {
	for _, l := range []Layout{Owner("/r"), For("/r", 7)} {
		got := l.Pattern(twoSum)
		if !strings.HasPrefix(got, "./") || !strings.HasSuffix(got, "/") {
			t.Errorf("Pattern = %q, want ./…/ form", got)
		}
		if strings.Contains(got, `\`) {
			t.Errorf("Pattern = %q, want forward slashes", got)
		}
	}
}

// The underscore is why guest packages stay out of ./... — if this name ever
// loses it, CI starts compiling other people's half-written Go.
func TestGuestDirectoriesAreHiddenFromPackageWildcards(t *testing.T) {
	if !strings.HasPrefix(guests, "_") {
		t.Fatalf("guests = %q: without a leading underscore ./... picks up guest code", guests)
	}
}
