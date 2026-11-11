// Package textdiff is a line diff, used to compare a fresh re-solve against the
// archived earlier attempt.
package textdiff

import "strings"

// Op is what happened to a line.
type Op int

// The three line operations.
const (
	Equal Op = iota
	Delete
	Insert
)

// Line is one line of output, tagged with its operation.
type Line struct {
	Op   Op
	Text string
}

// maxLines caps the quadratic table. Solutions are tens of lines; anything
// past this is not a solution, and a diff of it would not be read anyway.
const maxLines = 4000

// TooBig reports whether Lines would refuse the inputs.
func TooBig(old, new string) bool {
	return len(split(old)) > maxLines || len(split(new)) > maxLines
}

// Report is a whole comparison: the lines, the counts, and the two outcomes
// that are not a list of lines.
type Report struct {
	Lines     []Line
	Added     int
	Removed   int
	Identical bool
	TooBig    bool
}

// Compare diffs old against new and reports a refusal rather than an empty
// diff.
//
// The order matters and is easy to get wrong: an identical pair and an
// oversized pair both yield no lines, so a caller that asks Lines first cannot
// tell them apart and will say "identical, line for line" about a file it
// declined to read.
func Compare(old, new string) Report {
	if TooBig(old, new) {
		return Report{TooBig: true}
	}
	lines := Lines(old, new)
	added, removed := Stat(lines)
	return Report{Lines: lines, Added: added, Removed: removed, Identical: Identical(lines)}
}

func split(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Lines diffs old against new, returning every line in order. It is a plain
// longest-common-subsequence diff: no heuristics, no hunk grouping, because a
// solution is short enough to read whole.
func Lines(old, new string) []Line {
	a, b := split(old), split(new)
	if len(a) > maxLines || len(b) > maxLines {
		return nil
	}

	// lcs[i][j] is the length of the longest common subsequence of a[i:] and
	// b[j:]. Filling it backwards lets the walk below run forwards, which is
	// what keeps the output in file order.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	var out []Line
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, Line{Equal, a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Line{Delete, a[i]})
			i++
		default:
			out = append(out, Line{Insert, b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, Line{Delete, a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, Line{Insert, b[j]})
	}
	return out
}

// Stat counts the changed lines.
func Stat(lines []Line) (added, removed int) {
	for _, l := range lines {
		switch l.Op {
		case Insert:
			added++
		case Delete:
			removed++
		}
	}
	return added, removed
}

// Identical reports whether the two texts differ in no line.
func Identical(lines []Line) bool {
	a, r := Stat(lines)
	return a == 0 && r == 0
}
