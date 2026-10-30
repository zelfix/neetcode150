package textdiff

import (
	"strings"
	"testing"
)

// render turns a diff back into a compact string so a test can state the whole
// expected result on one line.
func render(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		switch l.Op {
		case Equal:
			b.WriteString(" ")
		case Delete:
			b.WriteString("-")
		case Insert:
			b.WriteString("+")
		}
		b.WriteString(l.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func TestLines(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"identical", "a\nb\n", "a\nb\n", " a\n b\n"},
		{"both empty", "", "", ""},
		{"insert into empty", "", "a\n", "+a\n"},
		{"delete to empty", "a\n", "", "-a\n"},
		{"append at end", "a\n", "a\nb\n", " a\n+b\n"},
		{"prepend at start", "b\n", "a\nb\n", "+a\n b\n"},
		{"delete in middle", "a\nb\nc\n", "a\nc\n", " a\n-b\n c\n"},
		{"replace a line", "a\nx\nc\n", "a\ny\nc\n", " a\n-x\n+y\n c\n"},
		{"nothing in common", "a\nb\n", "c\nd\n", "-a\n-b\n+c\n+d\n"},
		{"reorder", "a\nb\n", "b\na\n", "-a\n b\n+a\n"},
		{"trailing newline is not a change", "a", "a\n", " a\n"},
		{"crlf matches lf", "a\r\nb\r\n", "a\nb\n", " a\n b\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := render(Lines(tt.old, tt.new)); got != tt.want {
				t.Errorf("Lines()\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestStatAndIdentical(t *testing.T) {
	d := Lines("a\nb\nc\n", "a\nx\nc\nd\n")
	added, removed := Stat(d)
	if added != 2 || removed != 1 {
		t.Errorf("Stat() = %d added, %d removed; want 2, 1", added, removed)
	}
	if Identical(d) {
		t.Error("Identical() = true on a diff with changes")
	}
	if !Identical(Lines("a\n", "a\n")) {
		t.Error("Identical() = false on equal input")
	}
}

// The common line must be kept, not deleted and re-inserted: a diff that
// rewrites every line is useless for spotting what actually changed.
func TestLinesKeepsTheCommonSubsequence(t *testing.T) {
	old := "func f() int {\n\treturn 1\n}\n"
	new := "func f() int {\n\treturn 2\n}\n"
	d := Lines(old, new)
	added, removed := Stat(d)
	if added != 1 || removed != 1 {
		t.Fatalf("Stat() = %d added, %d removed; want 1, 1\n%s", added, removed, render(d))
	}
	if d[0].Op != Equal || d[0].Text != "func f() int {" {
		t.Errorf("first line = %+v, want the unchanged signature", d[0])
	}
}

func TestTooBigRefusesRatherThanHangs(t *testing.T) {
	huge := strings.Repeat("x\n", maxLines+1)
	if !TooBig(huge, "a\n") {
		t.Error("TooBig() = false on an oversized input")
	}
	if Lines(huge, "a\n") != nil {
		t.Error("Lines() on an oversized input returned a diff, want nil")
	}
	if TooBig("a\n", "b\n") {
		t.Error("TooBig() = true on a small input")
	}
}
