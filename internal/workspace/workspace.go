// Package workspace locates one person's working Go files.
//
// The files have to stay inside the neetcode150 module. A generated practice
// test imports neetcode150/internal/testkit, and Go's internal-package rule is
// a plain import-path prefix test — an importer under any other module path is
// refused however the module is wired, so neither a separate go.mod with a
// replace directive nor a go.work file can host these directories.
package workspace

import (
	"path/filepath"
	"strconv"

	"neetcode150/internal/catalog"
	"neetcode150/internal/studydb"
)

// solutions is where the owner's work has always lived, and stays. Keeping it
// means none of their files move, the CLI is unaffected, and the existing
// .gitignore entry still covers them.
const solutions = "solutions"

// guests holds everybody else, one directory per user id.
//
// The leading underscore is load-bearing: the go tool skips _-prefixed
// directories when expanding ./..., so guest code never reaches `go vet ./...`,
// `go build ./...` or the CI test run, while an explicit package path still
// builds. Verified on Go 1.26 rather than assumed.
const guests = "_work"

// Layout is where one user's working Go files live.
//
// Three path shapes mean a bug can hide in one of them alone, so the branch
// lives in PkgDir only, and its test covers an owner, a guest and an interview.
type Layout struct {
	Root   string // absolute project root
	UserID int64
	// Interview is non-zero while these files belong to an interview rather
	// than to the person's own study. An interview draws problems at random and
	// scaffolds them blank, which would overwrite a solution they already have,
	// so it gets a directory of its own and leaves the study files alone.
	Interview int64
}

// Owner is the layout the CLI uses: the repository owner's own files.
func Owner(root string) Layout { return Layout{Root: root, UserID: studydb.OwnerID} }

// For is the layout belonging to one user.
func For(root string, userID int64) Layout { return Layout{Root: root, UserID: userID} }

// Interview is the layout for one interview's working files.
func Interview(root string, userID, interviewID int64) Layout {
	return Layout{Root: root, UserID: userID, Interview: interviewID}
}

// PkgDir is the problem's package directory, relative to Root.
func (l Layout) PkgDir(p catalog.Problem) string {
	// Interview files go under guests even for the owner: the point is that
	// they are not the owner's solutions/ tree.
	if l.Interview != 0 {
		return filepath.Join(guests, strconv.FormatInt(l.UserID, 10),
			"interview-"+strconv.FormatInt(l.Interview, 10), solutions, p.Dir)
	}
	if l.UserID == studydb.OwnerID {
		return filepath.Join(solutions, p.Dir)
	}
	return filepath.Join(guests, strconv.FormatInt(l.UserID, 10), solutions, p.Dir)
}

// Dir is the problem's package directory as an absolute path.
func (l Layout) Dir(p catalog.Problem) string { return filepath.Join(l.Root, l.PkgDir(p)) }

// Path is a file inside the problem's package directory.
func (l Layout) Path(p catalog.Problem, name string) string {
	return filepath.Join(l.Dir(p), name)
}

// Rel is a project-relative path to a file in the package directory, in slash
// form — what the site shows and what error messages quote.
func (l Layout) Rel(p catalog.Problem, name string) string {
	return filepath.ToSlash(filepath.Join(l.PkgDir(p), name))
}

// Pattern is the package argument for `go test`.
func (l Layout) Pattern(p catalog.Problem) string {
	return "./" + filepath.ToSlash(l.PkgDir(p)) + "/"
}
