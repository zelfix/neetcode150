package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"neetcode150/internal/catalog"
)

const (
	// catalogURL serves .problemSiteData.json from the MIT-licensed NeetCode
	// solutions repo. The GitHub contents API is used rather than
	// raw.githubusercontent.com, which is not reachable from every network.
	catalogURL = "https://api.github.com/repos/neetcode-gh/leetcode/contents/.problemSiteData.json"
	graphQLURL = "https://leetcode.com/graphql"
	userAgent  = "neetcode150-local-study-kit"

	// politeDelay separates LeetCode requests. The statements are fetched once
	// and cached on disk, so there is no reason to go faster.
	politeDelay = 1200 * time.Millisecond
	maxAttempts = 3
)

// Client fetches problem metadata and statements.
type Client struct {
	HTTP  *http.Client
	Delay time.Duration
}

// NewClient returns a Client with sensible timeouts.
func NewClient() *Client {
	return &Client{
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Delay: politeDelay,
	}
}

// ncRecord is one entry of the upstream .problemSiteData.json.
type ncRecord struct {
	Problem     string `json:"problem"`
	Pattern     string `json:"pattern"`
	Link        string `json:"link"`
	Video       string `json:"video"`
	Difficulty  string `json:"difficulty"`
	Code        string `json:"code"`
	NeetCode150 bool   `json:"neetcode150"`
	Blind75     bool   `json:"blind75"`
	Premium     bool   `json:"premium"`
}

var codeRE = regexp.MustCompile(`^(\d{3,4})-(.+)$`)

// FetchCatalog downloads the upstream list and returns the 150, ordered by the
// roadmap: patterns in curriculum order, problems in the upstream order within
// each pattern.
func (c *Client) FetchCatalog(ctx context.Context) (*catalog.Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("User-Agent", userAgent)
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch problem list: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch problem list: %s: %s", resp.Status, snippet(body))
	}

	var records []ncRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("parse problem list: %w", err)
	}

	type ordered struct {
		p   catalog.Problem
		pos int
	}
	var picked []ordered
	for i, r := range records {
		if !r.NeetCode150 {
			continue
		}
		m := codeRE.FindStringSubmatch(r.Code)
		if m == nil {
			return nil, fmt.Errorf("problem %q: cannot read id from code field %q", r.Problem, r.Code)
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("problem %q: bad id in %q: %w", r.Problem, r.Code, err)
		}
		slug := strings.Trim(r.Link, "/")
		if slug == "" {
			return nil, fmt.Errorf("problem %q has no link", r.Problem)
		}
		picked = append(picked, ordered{pos: i, p: catalog.Problem{
			ID:          id,
			Slug:        slug,
			Title:       r.Problem,
			Pattern:     r.Pattern,
			PatternSlug: catalog.Slugify(r.Pattern),
			PatternIdx:  catalog.PatternIndex(r.Pattern),
			Difficulty:  r.Difficulty,
			Blind75:     r.Blind75,
			Premium:     r.Premium,
			VideoID:     r.Video,
			LeetCodeURL: "https://leetcode.com/problems/" + slug + "/",
			Dir:         fmt.Sprintf("%04d-%s", id, slug),
		}})
	}
	if len(picked) == 0 {
		return nil, errors.New("upstream list contained no neetcode150 problems")
	}

	sort.SliceStable(picked, func(i, j int) bool {
		return picked[i].p.PatternIdx < picked[j].p.PatternIdx
	})
	problems := make([]catalog.Problem, 0, len(picked))
	for i, o := range picked {
		o.p.Order = i
		problems = append(problems, o.p)
	}
	return catalog.New(problems), nil
}

// statementQuery asks only for what the local files need.
const statementQuery = `query questionContent($titleSlug: String!) {
  question(titleSlug: $titleSlug) {
    questionFrontendId
    title
    difficulty
    isPaidOnly
    content
    topicTags { name }
    codeSnippets { langSlug code }
  }
}`

// Statement is a problem statement as LeetCode serves it.
type Statement struct {
	FrontendID string
	Title      string
	Difficulty string
	PaidOnly   bool
	ContentMD  string
	Topics     []string
	GoStub     string // the official Go function signature, empty if none
}

type graphQLResponse struct {
	Data struct {
		Question *struct {
			QuestionFrontendID string `json:"questionFrontendId"`
			Title              string `json:"title"`
			Difficulty         string `json:"difficulty"`
			IsPaidOnly         bool   `json:"isPaidOnly"`
			Content            string `json:"content"`
			TopicTags          []struct {
				Name string `json:"name"`
			} `json:"topicTags"`
			CodeSnippets []struct {
				LangSlug string `json:"langSlug"`
				Code     string `json:"code"`
			} `json:"codeSnippets"`
		} `json:"question"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// ErrNoStatement means LeetCode served no statement body. In practice this is a
// premium-only problem, whose statement has to be written by hand.
var ErrNoStatement = errors.New("no statement available")

// Statement fetches one problem statement and converts it to Markdown.
func (c *Client) Statement(ctx context.Context, slug string) (*Statement, error) {
	payload, err := json.Marshal(map[string]any{
		"query":     statementQuery,
		"variables": map[string]string{"titleSlug": slug},
	})
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		st, retryable, err := c.statementOnce(ctx, slug, payload)
		if err == nil {
			return st, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%s: %w", slug, lastErr)
}

func (c *Client) statementOnce(ctx context.Context, slug string, payload []byte) (*Statement, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphQLURL, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://leetcode.com/problems/"+slug+"/")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, true, err
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("leetcode %s: %s", resp.Status, snippet(body))
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("leetcode %s: %s", resp.Status, snippet(body))
	}

	var out graphQLResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, true, fmt.Errorf("parse graphql response: %w", err)
	}
	if len(out.Errors) > 0 {
		return nil, false, fmt.Errorf("leetcode graphql: %s", out.Errors[0].Message)
	}
	q := out.Data.Question
	if q == nil {
		return nil, false, fmt.Errorf("%s: %w (unknown slug)", slug, ErrNoStatement)
	}

	st := &Statement{
		FrontendID: q.QuestionFrontendID,
		Title:      q.Title,
		Difficulty: q.Difficulty,
		PaidOnly:   q.IsPaidOnly,
	}
	for _, t := range q.TopicTags {
		st.Topics = append(st.Topics, t.Name)
	}
	for _, sn := range q.CodeSnippets {
		if sn.LangSlug == "golang" {
			st.GoStub = sn.Code
			break
		}
	}
	if strings.TrimSpace(q.Content) == "" {
		return st, false, fmt.Errorf("%s: %w (premium)", slug, ErrNoStatement)
	}
	md, err := HTMLToMarkdown(q.Content)
	if err != nil {
		return nil, false, err
	}
	st.ContentMD = md
	return st, false, nil
}

// Result records what happened to one problem during a build.
type Result struct {
	Problem catalog.Problem
	Action  string // "wrote", "skipped", "needs-hand-written", "failed"
	Err     error
}

// BuildOptions controls a content build.
type BuildOptions struct {
	Root  string
	Force bool // rewrite statements that are already on disk
	Log   func(format string, a ...any)
}

// Build writes content/problems/<dir>/problem.md and signature.txt for every
// problem. Existing files are left alone unless Force is set, so a re-run is
// cheap and never clobbers a hand-written statement.
func (c *Client) Build(ctx context.Context, cat *catalog.Catalog, opts BuildOptions) ([]Result, error) {
	logf := opts.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	results := make([]Result, 0, len(cat.Problems))
	fetched := 0

	for _, p := range cat.Problems {
		dir := filepath.Join(opts.Root, p.ContentDir())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return results, err
		}
		statementPath := filepath.Join(dir, "problem.md")
		signaturePath := filepath.Join(dir, "signature.txt")

		needStatement := opts.Force || !hasContent(statementPath)
		needSignature := opts.Force || !hasContent(signaturePath)
		if !needStatement && !needSignature {
			results = append(results, Result{Problem: p, Action: "skipped"})
			continue
		}
		if p.Premium {
			// LeetCode serves neither the statement nor a stub for these.
			logf("  %-52s premium — write it by hand\n", p.Slug)
			results = append(results, Result{Problem: p, Action: "needs-hand-written"})
			continue
		}

		if fetched > 0 {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(c.Delay):
			}
		}
		fetched++

		st, err := c.Statement(ctx, p.Slug)
		if err != nil {
			if errors.Is(err, ErrNoStatement) {
				logf("  %-52s no statement served — write it by hand\n", p.Slug)
				results = append(results, Result{Problem: p, Action: "needs-hand-written", Err: err})
				continue
			}
			logf("  %-52s FAILED: %v\n", p.Slug, err)
			results = append(results, Result{Problem: p, Action: "failed", Err: err})
			continue
		}
		if len(st.Topics) > 0 {
			p.Topics = st.Topics
		}
		if needStatement {
			if err := os.WriteFile(statementPath, []byte(ProblemFile(p, st.ContentMD)), 0o644); err != nil {
				return results, err
			}
		}
		if needSignature && strings.TrimSpace(st.GoStub) != "" {
			if err := os.WriteFile(signaturePath, []byte(strings.TrimRight(st.GoStub, "\n")+"\n"), 0o644); err != nil {
				return results, err
			}
		}
		logf("  %-52s ok\n", p.Slug)
		results = append(results, Result{Problem: p, Action: "wrote"})
	}
	return results, nil
}

// hasContent reports whether path exists and is not empty.
func hasContent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// ProblemFile renders a problem.md: frontmatter, a heading, then the statement.
func ProblemFile(p catalog.Problem, statement string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", p.Title)
	fmt.Fprintf(&b, "id: %d\n", p.ID)
	fmt.Fprintf(&b, "slug: %s\n", p.Slug)
	fmt.Fprintf(&b, "pattern: %s\n", p.Pattern)
	fmt.Fprintf(&b, "difficulty: %s\n", p.Difficulty)
	fmt.Fprintf(&b, "blind75: %t\n", p.Blind75)
	if len(p.Topics) > 0 {
		fmt.Fprintf(&b, "topics: %s\n", strings.Join(p.Topics, ", "))
	}
	fmt.Fprintf(&b, "leetcode: %s\n", p.LeetCodeURL)
	if u := p.VideoURL(); u != "" {
		fmt.Fprintf(&b, "video: %s\n", u)
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %d. %s\n\n", p.ID, p.Title)
	b.WriteString(strings.TrimRight(statement, "\n"))
	b.WriteString("\n")
	return b.String()
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
