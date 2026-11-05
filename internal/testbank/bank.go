// Package testbank holds public examples and additional practice fixtures.
// It contains no solution implementations.
package testbank

import (
	"embed"
	"encoding/json"
	"fmt"
	"go/format"
	"strings"
)

//go:embed cases/*.json
var files embed.FS

type Suite struct {
	Slug    string `json:"slug"`
	Entry   string `json:"entry"`
	Mode    string `json:"mode"`
	Policy  string `json:"policy,omitempty"`
	Mutates bool   `json:"mutates,omitempty"`
	Cases   []Case `json:"cases"`
}

type Case struct {
	Name     string              `json:"name"`
	Kind     string              `json:"kind"`
	Args     []json.RawMessage   `json:"args,omitempty"`
	Want     json.RawMessage     `json:"want"`
	Ops      []string            `json:"ops,omitempty"`
	Calls    [][]json.RawMessage `json:"calls,omitempty"`
	CyclePos *int                `json:"cyclePos,omitempty"`
}

func Load(slug string) (Suite, error) {
	b, err := files.ReadFile("cases/" + slug + ".json")
	if err != nil {
		return Suite{}, err
	}
	var s Suite
	if err := json.Unmarshal(b, &s); err != nil {
		return Suite{}, fmt.Errorf("test bank %s: %w", slug, err)
	}
	return s, nil
}

// TestSource keeps the fixtures visible and editable in the browser's Tests tab.
func TestSource(pkg, slug string) (string, error) {
	s, err := Load(slug)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	args := s.Entry
	switch s.Mode {
	case "strings-codec":
		args = "encode, decode"
	case "tree-codec":
		args = `func(root *TreeNode) string { c := Constructor(); return c.serialize(root) },
   func(data string) *TreeNode { c := Constructor(); return c.deserialize(data) }`
	}
	// Backticks are allowed inside codec fixtures; split those literals safely.
	literal := "`" + strings.ReplaceAll(string(b), "`", "` + \"`\" + `") + "`"
	src := fmt.Sprintf(`// Practice examples and corner cases. Add your own cases below.
package %s

import (
 "testing"
 "neetcode150/internal/testkit"
)

func TestPracticeCases(t *testing.T) {
 testkit.Run(t, %s, %s)
}
`, pkg, literal, args)
	out, err := format.Source([]byte(src))
	return string(out), err
}
