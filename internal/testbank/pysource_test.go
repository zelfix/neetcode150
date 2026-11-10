package testbank

import (
	"strings"
	"testing"
)

func TestPyTestSourceDeclaresSpecAndEntry(t *testing.T) {
	src, err := PyTestSource("two-sum", PyEntry{Class: "Solution", Methods: []string{"twoSum"}, Args: []string{"", ""}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SPEC = r'''",
		`"slug": "two-sum"`,
		`ENTRY = {"cls": "Solution", "methods": ["twoSum"], "args": ["", ""]}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	if strings.Contains(src, "import") {
		t.Error("practice_cases.py must import nothing")
	}
}

func TestPyTestSourceRejectsAnUnknownSlug(t *testing.T) {
	if _, err := PyTestSource("no-such-problem", PyEntry{}); err == nil {
		t.Fatal("unknown slug accepted")
	}
}
