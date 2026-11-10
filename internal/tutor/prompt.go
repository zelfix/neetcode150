package tutor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"neetcode150/internal/catalog"
	"neetcode150/internal/lang"
	"neetcode150/internal/studydb"
	"neetcode150/internal/workspace"
)

// An HTTP provider is handed its context instead of fetching it, so everything
// a CLI would have read off disk has to be assembled here.
//
// The split between the system prompt and the turn is deliberate and is about
// cost, not tidiness. DeepSeek caches on a prefix, and a cache hit costs about
// a thirtieth of a miss, so the stable half — the protocol, the policy, the
// statement, the recorded ladder — goes in the system prompt and stays
// byte-identical for the whole session, while the half that changes every turn
// — the current source and the person's message — goes in the turn. Putting the
// code in the system prompt would be simpler and would miss the cache on every
// single turn.
const (
	maxProtocol  = 48_000 // the protocol and its references
	maxStatement = 12_000
	maxSource    = 24_000
	maxLadder    = 16_000
)

// systemPrompt is everything that does not change while a problem is open.
func (m *Manager) systemPrompt(scope *studydb.Scope, p catalog.Problem) string {
	var b strings.Builder
	l := m.language(scope, p.Slug)

	fmt.Fprintf(&b, "You are tutoring one NeetCode 150 problem. The person is solving it in %s. Follow the protocol below exactly.\n", l.Label)
	b.WriteString("It is the whole of your instructions: you have no access to this repository, " +
		"no tools, and no way to read or run anything. Everything you are given arrives in these messages.\n\n")

	b.WriteString("=== TUTORING PROTOCOL ===\n")
	b.WriteString(readCapped(filepath.Join(m.root, "tutor", "TUTOR.md"), maxProtocol))
	b.WriteString("\n\n=== REFERENCES CITED BY THE PROTOCOL ===\n")
	refs := referencesFor(m.root, l)
	for _, ref := range refs {
		b.WriteString("\n--- " + filepath.Base(ref) + " ---\n")
		b.WriteString(readCapped(ref, maxProtocol/len(max1(refs))))
	}

	b.WriteString("\n\n=== LEARNING POLICY ===\n")
	b.WriteString(LearningPolicy)

	fmt.Fprintf(&b, "\n\n=== THE PROBLEM ===\n%d. %s (%s), pattern %q.\n\n", p.ID, p.Title, p.Slug, p.Pattern)
	b.WriteString(readCapped(filepath.Join(m.root, p.ContentDir(), "problem.md"), maxStatement))

	if scope != nil {
		if hints, err := scope.Document(p.Slug, "hints"); err == nil && strings.TrimSpace(hints) != "" {
			b.WriteString("\n\n=== RECORDED HINT LADDER ===\n")
			b.WriteString("Private reference. Reveal only the rung actually asked for, one per request.\n")
			b.WriteString(capAt(hints, maxLadder))
		}
	}

	b.WriteString("\n\n=== HOW TO ANSWER ===\n")
	b.WriteString("The person is in a browser workspace with an editor and a Run-tests button, " +
		"so never tell them to run `nc start` or the test command — the page does both and logs the time and the rating.\n" +
		"Your replies render as Markdown in a narrow side panel. Keep them short: a few sentences, or a short list. " +
		"Ask one question at a time and wait for the answer. Reply in the person's language. " +
		"Be warm and supportive: acknowledge a specific sound idea, effort, or self-correction before the next question. " +
		"Treat mistakes as something to explore together, not as a judgement of ability. " +
		"Avoid exaggerated praise and keep ratings honest.\n" +
		"You cannot see their code unless it is included in the message you are answering. " +
		"If it is absent and you need it, say so rather than guessing.")

	return b.String()
}

// turnContext is the part that changes: whatever their files say right now.
//
// Appended to the person's own message rather than kept in the system prompt,
// so editing a solution does not invalidate the cached prefix.
func (m *Manager) turnContext(scope *studydb.Scope, p catalog.Problem) string {
	if scope == nil {
		return ""
	}
	lay := workspace.For(m.root, scope.UserID())
	l := m.language(scope, p.Slug)
	var b strings.Builder
	if code := readCapped(lay.Path(p, l.SolutionFile), maxSource); strings.TrimSpace(code) != "" {
		b.WriteString("\n\n=== THEIR SOLUTION AS IT STANDS ===\n```" + l.Fence + "\n" + code + "\n```")
	}
	for _, path := range l.TestFiles(lay.Dir(p)) {
		// The generated practice suite is a wall of fixtures that says nothing
		// about how they are thinking, and it would crowd out everything else.
		if filepath.Base(path) == l.PracticeFile {
			continue
		}
		if body := readCapped(path, maxSource); strings.TrimSpace(body) != "" {
			b.WriteString("\n\n=== THEIR TESTS (" + filepath.Base(path) + ") ===\n```" + l.Fence + "\n" + body + "\n```")
		}
	}
	if b.Len() == 0 {
		return "\n\n(They have not written any code yet.)"
	}
	return b.String()
}

// referencesFor is every reference file but the other language's idioms. Sending
// both would cut the active one's share of the budget and invite advice about
// the language the person is not writing.
func referencesFor(root string, l lang.Lang) []string {
	refs, _ := filepath.Glob(filepath.Join(root, "tutor", "references", "*.md"))
	var out []string
	for _, ref := range refs {
		base := filepath.Base(ref)
		if strings.HasSuffix(base, "-idioms.md") && base != l.IdiomsRef {
			continue
		}
		out = append(out, ref)
	}
	return out
}

// readCapped reads a file, returning "" when it is not there, and keeps the
// head so a runaway file cannot push the protocol out of the prompt.
func readCapped(path string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return capAt(string(data), limit)
}

func capAt(s string, limit int) string {
	if limit > 0 && len(s) > limit {
		return s[:limit] + "\n…(truncated)"
	}
	return s
}

// max1 avoids dividing by zero when there are no reference files.
func max1[T any](xs []T) []T {
	if len(xs) == 0 {
		return make([]T, 1)
	}
	return xs
}
