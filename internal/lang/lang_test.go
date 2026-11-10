package lang

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAcceptsTheUsualSpellingsAndDefaultsToGo(t *testing.T) {
	for in, want := range map[string]ID{"": Go, "go": Go, "Golang": Go, "python": Python, "py": Python, "python3": Python} {
		l, err := Parse(in)
		if err != nil || l.ID != want {
			t.Errorf("Parse(%q) = %v, %v", in, l.ID, err)
		}
	}
	if _, err := Parse("rust"); err == nil {
		t.Error("an unknown language parsed")
	}
}

// The resolution rule is used by the CLI and the site; it is pinned here so
// neither grows its own.
func TestResolvePrefersTheEntryThenThePerson(t *testing.T) {
	for _, tc := range []struct {
		entry, user string
		want        ID
	}{
		{"", "", Go},
		{"", "python", Python},
		{"go", "python", Go},
		{"python", "", Python},
		{"nonsense", "python", Python},
		{"", "nonsense", Go},
	} {
		if got := Resolve(tc.entry, tc.user).ID; got != tc.want {
			t.Errorf("Resolve(%q, %q) = %s, want %s", tc.entry, tc.user, got, tc.want)
		}
	}
}

func TestFormatIsGofmtForGoAndIdentityForPython(t *testing.T) {
	if out, err := Of(Go).Format("package x\nfunc  f(){}\n"); err != nil || out != "package x\n\nfunc f() {}\n" {
		t.Errorf("gofmt: %q %v", out, err)
	}
	src := "def  f():\n  return 1\n"
	if out, err := Of(Python).Format(src); err != nil || out != src {
		t.Errorf("python source was changed: %q %v", out, err)
	}
}

func TestTestFilesFollowTheLanguage(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"solution.go", "solution_test.go", "practice_cases_test.go", "solution.py", "test_solution.py", "practice_cases.py"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := Of(Go).TestFiles(dir); len(got) != 2 {
		t.Errorf("go test files: %v", got)
	}
	got := Of(Python).TestFiles(dir)
	if len(got) != 2 || filepath.Base(got[0]) != "test_solution.py" || filepath.Base(got[1]) != "practice_cases.py" {
		t.Errorf("python test files: %v", got)
	}
	if Of(Python).IsTestFile("solution.py") || !Of(Python).IsTestFile("x/test_mine.py") {
		t.Error("IsTestFile is wrong for python")
	}
}
