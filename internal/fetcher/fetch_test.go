package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neetcode150/internal/catalog"
)

// stubCatalogServer returns an httptest.Server whose handler writes body with
// the given status code. The caller must close the server.
func stubCatalogServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

// twoRecords is a minimal valid payload for FetchCatalog: one neetcode150
// problem (Arrays & Hashing) and one non-150 problem that must be filtered out.
var twoRecords = []ncRecord{
	{
		Problem:     "Two Sum",
		Pattern:     "Arrays & Hashing",
		Link:        "two-sum",
		Video:       "KLlXCFG5TnA",
		Difficulty:  "Easy",
		Code:        "0001-two-sum",
		NeetCode150: true,
		Blind75:     true,
	},
	{
		Problem:     "Contains Duplicate",
		Pattern:     "Arrays & Hashing",
		Link:        "contains-duplicate",
		Difficulty:  "Easy",
		Code:        "0217-contains-duplicate",
		NeetCode150: false, // should be filtered out
	},
}

func marshalRecords(t *testing.T, records []ncRecord) string {
	t.Helper()
	b, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal records: %v", err)
	}
	return string(b)
}

// clientWithNoDelay returns a Client with all delays zeroed so tests finish
// instantly. retryBackoff is set to return 0 so Statement retry loops do not
// wait for the real exponential back-off (which would add 6 s per test).
func clientWithNoDelay() *Client {
	return &Client{
		HTTP:         &http.Client{},
		Delay:        0,
		retryBackoff: func(int) time.Duration { return 0 },
	}
}

// ---- FetchCatalog -----------------------------------------------------------

func TestFetchCatalog(t *testing.T) {
	t.Run("parses two problems and filters non-150", func(t *testing.T) {
		srv := stubCatalogServer(t, http.StatusOK, marshalRecords(t, twoRecords))
		defer srv.Close()
		catalogURL = srv.URL

		cat, err := clientWithNoDelay().FetchCatalog(context.Background())
		if err != nil {
			t.Fatalf("FetchCatalog: %v", err)
		}
		if len(cat.Problems) != 1 {
			t.Fatalf("got %d problems, want 1", len(cat.Problems))
		}
		p := cat.Problems[0]
		if p.ID != 1 {
			t.Errorf("ID = %d, want 1", p.ID)
		}
		if p.Slug != "two-sum" {
			t.Errorf("Slug = %q, want two-sum", p.Slug)
		}
		if p.Title != "Two Sum" {
			t.Errorf("Title = %q, want Two Sum", p.Title)
		}
		if p.Pattern != "Arrays & Hashing" {
			t.Errorf("Pattern = %q, want Arrays & Hashing", p.Pattern)
		}
		if p.Difficulty != "Easy" {
			t.Errorf("Difficulty = %q, want Easy", p.Difficulty)
		}
		if !p.Blind75 {
			t.Error("Blind75 should be true")
		}
		if p.Dir != "0001-two-sum" {
			t.Errorf("Dir = %q, want 0001-two-sum", p.Dir)
		}
	})

	t.Run("non-200 response returns error with status", func(t *testing.T) {
		srv := stubCatalogServer(t, http.StatusForbidden, "rate limited")
		defer srv.Close()
		catalogURL = srv.URL

		_, err := clientWithNoDelay().FetchCatalog(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "403") {
			t.Errorf("error %q does not mention status 403", err.Error())
		}
	})

	t.Run("malformed JSON returns parse error", func(t *testing.T) {
		srv := stubCatalogServer(t, http.StatusOK, `not valid json{`)
		defer srv.Close()
		catalogURL = srv.URL

		_, err := clientWithNoDelay().FetchCatalog(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "parse problem list") {
			t.Errorf("error %q does not mention 'parse problem list'", err.Error())
		}
	})

	t.Run("empty neetcode150 set returns error", func(t *testing.T) {
		none := []ncRecord{{Problem: "Other", NeetCode150: false, Code: "0999-other", Link: "other"}}
		srv := stubCatalogServer(t, http.StatusOK, marshalRecords(t, none))
		defer srv.Close()
		catalogURL = srv.URL

		_, err := clientWithNoDelay().FetchCatalog(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "neetcode150") {
			t.Errorf("error %q should mention neetcode150", err.Error())
		}
	})

	t.Run("sorts problems by PatternIdx across patterns", func(t *testing.T) {
		// Greedy (index 14) before Arrays & Hashing (index 0) in the raw list —
		// result must be sorted by curriculum position.
		mixed := []ncRecord{
			{Problem: "Jump Game", Pattern: "Greedy", Link: "jump-game", Difficulty: "Medium", Code: "0055-jump-game", NeetCode150: true},
			{Problem: "Two Sum", Pattern: "Arrays & Hashing", Link: "two-sum", Difficulty: "Easy", Code: "0001-two-sum", NeetCode150: true},
		}
		srv := stubCatalogServer(t, http.StatusOK, marshalRecords(t, mixed))
		defer srv.Close()
		catalogURL = srv.URL

		cat, err := clientWithNoDelay().FetchCatalog(context.Background())
		if err != nil {
			t.Fatalf("FetchCatalog: %v", err)
		}
		if cat.Problems[0].Slug != "two-sum" {
			t.Errorf("first problem = %q, want two-sum (Arrays & Hashing sorts before Greedy)", cat.Problems[0].Slug)
		}
	})
}

// ---- Statement / statementOnce ---------------------------------------------

// graphQLBody builds the JSON response the LeetCode GraphQL endpoint would
// return for a single problem.
func graphQLBody(t *testing.T, q *struct {
	QuestionFrontendID string
	Title              string
	Difficulty         string
	IsPaidOnly         bool
	Content            string
	Topics             []string
	GoCode             string
	OtherLangCode      string
}) string {
	t.Helper()

	type snippetJSON struct {
		LangSlug string `json:"langSlug"`
		Code     string `json:"code"`
	}
	type tagJSON struct {
		Name string `json:"name"`
	}
	type questionJSON struct {
		QuestionFrontendID string        `json:"questionFrontendId"`
		Title              string        `json:"title"`
		Difficulty         string        `json:"difficulty"`
		IsPaidOnly         bool          `json:"isPaidOnly"`
		Content            string        `json:"content"`
		TopicTags          []tagJSON     `json:"topicTags"`
		CodeSnippets       []snippetJSON `json:"codeSnippets"`
	}
	var tags []tagJSON
	for _, name := range q.Topics {
		tags = append(tags, tagJSON{Name: name})
	}
	snips := []snippetJSON{}
	if q.OtherLangCode != "" {
		snips = append(snips, snippetJSON{LangSlug: "python3", Code: q.OtherLangCode})
	}
	if q.GoCode != "" {
		snips = append(snips, snippetJSON{LangSlug: "golang", Code: q.GoCode})
	}
	outer := map[string]any{
		"data": map[string]any{
			"question": questionJSON{
				QuestionFrontendID: q.QuestionFrontendID,
				Title:              q.Title,
				Difficulty:         q.Difficulty,
				IsPaidOnly:         q.IsPaidOnly,
				Content:            q.Content,
				TopicTags:          tags,
				CodeSnippets:       snips,
			},
		},
	}
	b, err := json.Marshal(outer)
	if err != nil {
		t.Fatalf("marshal graphql body: %v", err)
	}
	return string(b)
}

func TestStatement(t *testing.T) {
	t.Run("selects Go snippet and converts HTML content to Markdown", func(t *testing.T) {
		body := graphQLBody(t, &struct {
			QuestionFrontendID string
			Title              string
			Difficulty         string
			IsPaidOnly         bool
			Content            string
			Topics             []string
			GoCode             string
			OtherLangCode      string
		}{
			QuestionFrontendID: "1",
			Title:              "Two Sum",
			Difficulty:         "Easy",
			Content:            "<p>Find two numbers.</p>",
			Topics:             []string{"Array", "Hash Table"},
			GoCode:             "func twoSum(nums []int, target int) []int {}",
			OtherLangCode:      "def two_sum(): pass",
		})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(body)) //nolint:errcheck
		}))
		defer srv.Close()
		graphQLURL = srv.URL

		st, err := clientWithNoDelay().Statement(context.Background(), "two-sum")
		if err != nil {
			t.Fatalf("Statement: %v", err)
		}
		if st.GoStub != "func twoSum(nums []int, target int) []int {}" {
			t.Errorf("GoStub = %q", st.GoStub)
		}
		// HTML must have been converted; no raw tags may survive.
		if strings.Contains(st.ContentMD, "<p>") {
			t.Errorf("ContentMD still contains HTML <p> tag")
		}
		if !strings.Contains(st.ContentMD, "Find two numbers") {
			t.Errorf("ContentMD missing expected text; got %q", st.ContentMD)
		}
		if len(st.Topics) != 2 || st.Topics[0] != "Array" {
			t.Errorf("Topics = %v, want [Array Hash Table]", st.Topics)
		}
	})

	t.Run("empty content returns ErrNoStatement", func(t *testing.T) {
		body := graphQLBody(t, &struct {
			QuestionFrontendID string
			Title              string
			Difficulty         string
			IsPaidOnly         bool
			Content            string
			Topics             []string
			GoCode             string
			OtherLangCode      string
		}{
			QuestionFrontendID: "99",
			Title:              "Premium Problem",
			Difficulty:         "Hard",
			IsPaidOnly:         true,
			Content:            "   ", // whitespace only → premium
		})
		srv := stubCatalogServer(t, http.StatusOK, body)
		defer srv.Close()
		graphQLURL = srv.URL

		_, err := clientWithNoDelay().Statement(context.Background(), "premium-problem")
		if err == nil {
			t.Fatal("expected ErrNoStatement, got nil")
		}
		if !errors.Is(err, ErrNoStatement) {
			t.Errorf("error %q does not wrap ErrNoStatement", err.Error())
		}
	})

	t.Run("null question field returns ErrNoStatement", func(t *testing.T) {
		body := `{"data":{"question":null}}`
		srv := stubCatalogServer(t, http.StatusOK, body)
		defer srv.Close()
		graphQLURL = srv.URL

		_, err := clientWithNoDelay().Statement(context.Background(), "unknown-slug")
		if !errors.Is(err, ErrNoStatement) {
			t.Errorf("error %v does not wrap ErrNoStatement", err)
		}
	})

	t.Run("GraphQL errors field returns error with message", func(t *testing.T) {
		body := `{"errors":[{"message":"access denied"}]}`
		srv := stubCatalogServer(t, http.StatusOK, body)
		defer srv.Close()
		graphQLURL = srv.URL

		_, err := clientWithNoDelay().Statement(context.Background(), "some-slug")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("error %q should contain GraphQL message", err.Error())
		}
	})

	t.Run("429 is reported as an error", func(t *testing.T) {
		// maxAttempts retries on 429; with a test server that always returns 429
		// the final error must still surface the status.
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("slow down")) //nolint:errcheck
		}))
		defer srv.Close()
		graphQLURL = srv.URL

		_, err := clientWithNoDelay().Statement(context.Background(), "two-sum")
		if err == nil {
			t.Fatal("expected error from 429, got nil")
		}
		if !strings.Contains(err.Error(), "429") {
			t.Errorf("error %q does not mention 429", err.Error())
		}
		if calls != maxAttempts {
			t.Errorf("expected %d retries, got %d", maxAttempts, calls)
		}
	})

	t.Run("non-200 non-retryable returns error without retry", func(t *testing.T) {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		graphQLURL = srv.URL

		_, err := clientWithNoDelay().Statement(context.Background(), "two-sum")
		if err == nil {
			t.Fatal("expected error from 404, got nil")
		}
		if calls != 1 {
			t.Errorf("404 should not retry, but got %d calls", calls)
		}
	})
}

// ---- Build ------------------------------------------------------------------

// minimalCatalog builds a two-problem catalog suitable for Build tests.
// One problem is regular, one is premium.
func minimalCatalog() *catalog.Catalog {
	problems := []catalog.Problem{
		{
			ID:          1,
			Slug:        "two-sum",
			Title:       "Two Sum",
			Pattern:     "Arrays & Hashing",
			PatternSlug: "arrays-hashing",
			PatternIdx:  0,
			Order:       0,
			Difficulty:  "Easy",
			Blind75:     true,
			LeetCodeURL: "https://leetcode.com/problems/two-sum/",
			Dir:         "0001-two-sum",
		},
		{
			ID:          149,
			Slug:        "max-points-on-a-line",
			Title:       "Max Points on a Line",
			Pattern:     "Math & Geometry",
			PatternSlug: "math-geometry",
			PatternIdx:  16,
			Order:       1,
			Difficulty:  "Hard",
			Premium:     true,
			LeetCodeURL: "https://leetcode.com/problems/max-points-on-a-line/",
			Dir:         "0149-max-points-on-a-line",
		},
	}
	return catalog.New(problems)
}

// goodGraphQLHandler serves a valid statement for any POST request so Build
// can exercise the "wrote" path.
func goodGraphQLHandler(t *testing.T) http.Handler {
	t.Helper()
	body := graphQLBody(t, &struct {
		QuestionFrontendID string
		Title              string
		Difficulty         string
		IsPaidOnly         bool
		Content            string
		Topics             []string
		GoCode             string
		OtherLangCode      string
	}{
		QuestionFrontendID: "1",
		Title:              "Two Sum",
		Difficulty:         "Easy",
		Content:            "<p>Find two numbers.</p>",
		Topics:             []string{"Array"},
		GoCode:             "func twoSum(nums []int, target int) []int {}",
		OtherLangCode:      "class Solution:\n    def twoSum(self, nums: List[int], target: int) -> List[int]:\n        ",
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	})
}

func TestBuild(t *testing.T) {
	t.Run("writes problem.md and signature.txt for regular problems, skips premium", func(t *testing.T) {
		srv := httptest.NewServer(goodGraphQLHandler(t))
		defer srv.Close()
		graphQLURL = srv.URL

		root := t.TempDir()
		cat := minimalCatalog()

		results, err := clientWithNoDelay().Build(context.Background(), cat, BuildOptions{Root: root})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}

		// Regular problem must be written.
		wroteSeen := false
		for _, r := range results {
			if r.Problem.Slug == "two-sum" {
				if r.Action != "wrote" {
					t.Errorf("two-sum: action = %q, want wrote", r.Action)
				}
				wroteSeen = true
			}
			if r.Problem.Slug == "max-points-on-a-line" {
				if r.Action != "needs-hand-written" {
					t.Errorf("premium: action = %q, want needs-hand-written", r.Action)
				}
			}
		}
		if !wroteSeen {
			t.Error("two-sum result not found in results slice")
		}

		// Files must exist and be non-empty.
		statementPath := filepath.Join(root, "content", "problems", "0001-two-sum", "problem.md")
		if _, err := os.Stat(statementPath); err != nil {
			t.Errorf("problem.md not written: %v", err)
		}
		sigPath := filepath.Join(root, "content", "problems", "0001-two-sum", "signature.txt")
		if _, err := os.Stat(sigPath); err != nil {
			t.Errorf("signature.txt not written: %v", err)
		}
		pyPath := filepath.Join(root, "content", "problems", "0001-two-sum", "signature.py")
		if b, err := os.ReadFile(pyPath); err != nil || !strings.HasPrefix(string(b), "class Solution:") {
			t.Errorf("signature.py not written: %v %q", err, b)
		}
	})

	t.Run("second Build without force skips existing files", func(t *testing.T) {
		srv := httptest.NewServer(goodGraphQLHandler(t))
		defer srv.Close()
		graphQLURL = srv.URL

		root := t.TempDir()
		cat := minimalCatalog()
		opts := BuildOptions{Root: root}

		if _, err := clientWithNoDelay().Build(context.Background(), cat, opts); err != nil {
			t.Fatalf("first Build: %v", err)
		}

		// Disconnect the server — any network call on the second run is a bug.
		srv.Close()

		results, err := clientWithNoDelay().Build(context.Background(), cat, opts)
		if err != nil {
			t.Fatalf("second Build: %v", err)
		}
		for _, r := range results {
			if r.Problem.Premium {
				continue // premium always goes to needs-hand-written
			}
			if r.Action != "skipped" {
				t.Errorf("%s: action = %q on second run without force, want skipped", r.Problem.Slug, r.Action)
			}
		}
	})

	t.Run("force rewrites existing files", func(t *testing.T) {
		srv := httptest.NewServer(goodGraphQLHandler(t))
		defer srv.Close()
		graphQLURL = srv.URL

		root := t.TempDir()
		cat := minimalCatalog()

		if _, err := clientWithNoDelay().Build(context.Background(), cat, BuildOptions{Root: root}); err != nil {
			t.Fatalf("first Build: %v", err)
		}
		results, err := clientWithNoDelay().Build(context.Background(), cat, BuildOptions{Root: root, Force: true})
		if err != nil {
			t.Fatalf("forced Build: %v", err)
		}
		for _, r := range results {
			if r.Problem.Premium {
				continue
			}
			if r.Action != "wrote" {
				t.Errorf("%s: action = %q with force, want wrote", r.Problem.Slug, r.Action)
			}
		}
	})

	t.Run("empty statement yields needs-hand-written action, not a crash", func(t *testing.T) {
		// Return a response where content is blank — same as premium problems
		// served by LeetCode: ErrNoStatement should produce needs-hand-written.
		emptyContent := `{"data":{"question":{"questionFrontendId":"1","title":"Two Sum","difficulty":"Easy","isPaidOnly":false,"content":"  ","topicTags":[],"codeSnippets":[]}}}`
		srv := stubCatalogServer(t, http.StatusOK, emptyContent)
		defer srv.Close()
		graphQLURL = srv.URL

		root := t.TempDir()
		// Catalog with only the regular problem so we can test this path.
		oneProblem := catalog.New([]catalog.Problem{{
			ID: 1, Slug: "two-sum", Title: "Two Sum",
			Pattern: "Arrays & Hashing", PatternSlug: "arrays-hashing",
			Difficulty: "Easy", LeetCodeURL: "https://leetcode.com/problems/two-sum/",
			Dir: "0001-two-sum",
		}})

		results, err := clientWithNoDelay().Build(context.Background(), oneProblem, BuildOptions{Root: root})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		if results[0].Action != "needs-hand-written" {
			t.Errorf("action = %q, want needs-hand-written", results[0].Action)
		}
	})
}

// ---- ProblemFile ------------------------------------------------------------

func TestProblemFile(t *testing.T) {
	p := catalog.Problem{
		ID:          1,
		Slug:        "two-sum",
		Title:       "Two Sum",
		Pattern:     "Arrays & Hashing",
		Difficulty:  "Easy",
		Blind75:     true,
		Topics:      []string{"Array", "Hash Table"},
		LeetCodeURL: "https://leetcode.com/problems/two-sum/",
		// VideoID intentionally empty so no video line appears.
	}
	statement := "Find two numbers that add to target.\n"

	got := ProblemFile(p, statement)

	want := `---
title: Two Sum
id: 1
slug: two-sum
pattern: Arrays & Hashing
difficulty: Easy
blind75: true
topics: Array, Hash Table
leetcode: https://leetcode.com/problems/two-sum/
---

# 1. Two Sum

Find two numbers that add to target.
`
	if got != want {
		t.Errorf("ProblemFile output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}

	t.Run("video line appears when VideoID is set", func(t *testing.T) {
		pv := p
		pv.VideoID = "KLlXCFG5TnA"
		out := ProblemFile(pv, "body\n")
		if !strings.Contains(out, "video: https://www.youtube.com/watch?v=KLlXCFG5TnA") {
			t.Errorf("video line missing in:\n%s", out)
		}
	})

	t.Run("topics line omitted when Topics is empty", func(t *testing.T) {
		pn := p
		pn.Topics = nil
		out := ProblemFile(pn, "body\n")
		if strings.Contains(out, "topics:") {
			t.Errorf("topics line present with no topics in:\n%s", out)
		}
	})

	t.Run("trailing newlines in statement are normalised to exactly one", func(t *testing.T) {
		out := ProblemFile(p, "body\n\n\n")
		if strings.HasSuffix(out, "\n\n") {
			t.Error("output ends with multiple newlines")
		}
	})
}
