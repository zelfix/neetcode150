package testbank

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PyEntry is what practice_cases.py declares about the solution: the class to
// instantiate, the methods to call, and how each argument is built from JSON.
//
// Python has no static types for the harness to read, so the argument kinds
// are decided once, at scaffold time, from the official signature's
// annotations rather than from whatever the user later writes.
type PyEntry struct {
	Class   string   `json:"cls"`
	Methods []string `json:"methods"`
	Args    []string `json:"args"`
}

// PyTestSource writes practice_cases.py, the Python counterpart of the file
// TestSource writes. It is declarative on purpose: the harness loads it for
// SPEC and ENTRY and never has to import anything from the user's directory.
func PyTestSource(slug string, entry PyEntry) (string, error) {
	s, err := Load(slug)
	if err != nil {
		return "", err
	}
	spec, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	// A raw triple-quoted literal keeps the JSON readable in the Tests tab.
	// No fixture contains a triple quote today; the other delimiter is the
	// fallback, and both at once is an error rather than a corrupt file.
	quote := "'''"
	text := string(spec)
	if strings.Contains(text, "'''") {
		if strings.Contains(text, `"""`) {
			return "", fmt.Errorf("test bank %s: fixture contains both triple quotes", slug)
		}
		quote = `"""`
	}
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Practice examples and corner cases. Add your own cases below in SPEC.\n")
	b.WriteString("# The harness reads SPEC (the fixtures) and ENTRY (what to call); this\n")
	b.WriteString("# file runs no code of its own.\n\n")
	fmt.Fprintf(&b, "SPEC = r%s%s%s\n\n", quote, text, quote)
	fmt.Fprintf(&b, "ENTRY = %s\n", pyLiteral(entryJSON))
	return b.String(), nil
}

// pyLiteral turns a JSON object into the same thing spelled as Python: only
// the casing of null/true/false differs, and ENTRY holds strings and lists.
func pyLiteral(raw []byte) string {
	s := string(raw)
	s = strings.ReplaceAll(s, ":", ": ")
	s = strings.ReplaceAll(s, ",", ", ")
	return s
}
