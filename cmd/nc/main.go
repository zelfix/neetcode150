// Command nc is the local NeetCode 150 study kit: it builds the content tree,
// serves the reading site, and tracks what you have solved.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"neetcode150/internal/catalog"
	"neetcode150/internal/content"
	"neetcode150/internal/fetcher"
	"neetcode150/internal/progress"
	"neetcode150/internal/scaffold"
	"neetcode150/internal/web"
)

const usage = `nc — local NeetCode 150 study kit

Usage:
  nc fetch [--force]           download the problem list, statements and Go signatures
  nc serve [--port 8150]       serve the study site
  nc next                      what to work on now
  nc start <problem>           scaffold the Go solution and notes, mark it in progress
  nc status <problem> <state>  set state: new | attempted | solved | shaky
  nc rate <problem> <1-5>      record how it went and reschedule the review
  nc time <problem> <minutes>  add time spent
  nc review                    what is due for review
  nc stats                     progress by pattern
  nc info <problem>            paths and state for one problem

<problem> is a slug, a LeetCode number, a directory name, or part of a title:
  nc start two-sum        nc start 1        nc start "valid anag"
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nc: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]

	root, err := catalog.FindRoot()
	if err != nil {
		return err
	}

	switch cmd {
	case "fetch":
		return cmdFetch(root, rest)
	case "serve":
		return cmdServe(root, rest)
	case "next":
		return cmdNext(root)
	case "start":
		return cmdStart(root, rest)
	case "status":
		return cmdStatus(root, rest)
	case "rate":
		return cmdRate(root, rest)
	case "time":
		return cmdTime(root, rest)
	case "review":
		return cmdReview(root)
	case "stats":
		return cmdStats(root)
	case "info":
		return cmdInfo(root, rest)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

// ---------- shared loading ----------

func catalogPath(root string) string  { return filepath.Join(root, "content", "problems.json") }
func progressPath(root string) string { return filepath.Join(root, "progress.json") }

func load(root string) (*catalog.Catalog, *progress.Store, error) {
	cat, err := catalog.Load(catalogPath(root))
	if err != nil {
		return nil, nil, err
	}
	prog, err := progress.Load(progressPath(root))
	if err != nil {
		return nil, nil, err
	}
	return cat, prog, nil
}

// resolve looks up one problem from a loose command-line reference.
func resolve(cat *catalog.Catalog, args []string) (catalog.Problem, error) {
	if len(args) == 0 {
		return catalog.Problem{}, fmt.Errorf("which problem? try `nc next`")
	}
	return cat.Resolve(strings.Join(args, " "))
}

// ---------- commands ----------

func cmdFetch(root string, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	force := fs.Bool("force", false, "rewrite statements and signatures that are already on disk")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := fetcher.NewClient()
	fmt.Println("Reading the NeetCode 150 list…")
	cat, err := client.FetchCatalog(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%d problems across %d patterns.\n", len(cat.Problems), len(cat.Groups()))
	if err := cat.Save(catalogPath(root)); err != nil {
		return err
	}

	fmt.Println("Fetching statements and Go signatures (rate limited, this takes a few minutes):")
	results, err := client.Build(ctx, cat, fetcher.BuildOptions{
		Root:  root,
		Force: *force,
		Log:   func(format string, a ...any) { fmt.Printf(format, a...) },
	})
	if err != nil {
		return err
	}

	counts := map[string]int{}
	var handWritten, failed []string
	for _, r := range results {
		counts[r.Action]++
		switch r.Action {
		case "needs-hand-written":
			handWritten = append(handWritten, r.Problem.Slug)
		case "failed":
			failed = append(failed, r.Problem.Slug)
		}
	}
	fmt.Printf("\nwrote %d, already on disk %d, hand-written %d, failed %d\n",
		counts["wrote"], counts["skipped"], counts["needs-hand-written"], counts["failed"])
	if len(handWritten) > 0 {
		fmt.Printf("LeetCode keeps these behind Premium, so their statements are written by hand:\n  %s\n",
			strings.Join(handWritten, "\n  "))
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d problems failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

func cmdServe(root string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8150, "port to listen on")
	host := fs.String("host", "127.0.0.1", "address to bind")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	store := content.NewStore(root, content.NewRenderer())
	srv, err := web.New(root, cat, store, prog)
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("neetcode150 on http://%s  (%d problems, Ctrl-C to stop)\n", addr, len(cat.Problems))
	return web.Listen(addr, srv.Handler())
}

func cmdNext(root string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	now := time.Now()
	p, reason, ok := prog.Next(cat.Problems, now)
	if !ok {
		fmt.Println("Nothing left: all 150 solved and nothing due for review.")
		return nil
	}
	e := prog.Get(p.Slug)
	fmt.Printf("%d. %s\n", p.ID, p.Title)
	fmt.Printf("  %s · %s · %s\n", p.Difficulty, p.Pattern, reason)
	fmt.Printf("  slug      %s\n", p.Slug)
	fmt.Printf("  statement %s/problem.md\n", p.ContentDir())
	fmt.Printf("  solution  %s/solution.go\n", p.SolutionDir())
	fmt.Printf("  state     %s", e.Status.Label())
	if e.Attempts > 0 {
		fmt.Printf(", %s", plural(e.Attempts, "attempt", "attempts"))
	}
	fmt.Println()
	return nil
}

func cmdStart(root string, args []string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}

	signature, _ := os.ReadFile(filepath.Join(root, p.ContentDir(), "signature.txt"))
	statementBytes, _ := os.ReadFile(filepath.Join(root, p.ContentDir(), "problem.md"))
	_, statement := content.SplitFrontmatter(string(statementBytes))
	files := scaffold.Generate(p, string(signature), statement)

	solDir := filepath.Join(root, p.SolutionDir())
	if err := os.MkdirAll(solDir, 0o755); err != nil {
		return err
	}
	written, err := writeNew(map[string]string{
		filepath.Join(solDir, "solution.go"):            files.Solution,
		filepath.Join(solDir, "solution_test.go"):       files.Test,
		filepath.Join(root, p.ContentDir(), "notes.md"): scaffold.NotesTemplate(p),
	})
	if err != nil {
		return err
	}

	if _, err := prog.SetStatus(p.Slug, progress.Attempted, time.Now()); err != nil {
		return err
	}

	fmt.Printf("%d. %s — %s, %s\n", p.ID, p.Title, p.Difficulty, p.Pattern)
	if len(written) == 0 {
		fmt.Println("  files already exist, left untouched")
	}
	for _, f := range written {
		rel, _ := filepath.Rel(root, f)
		fmt.Printf("  created %s\n", rel)
	}
	switch {
	case files.TableDriven:
		fmt.Printf("  entry point %s() — the test is a table, fill in the cases\n", files.FuncName)
	case strings.TrimSpace(string(signature)) == "":
		fmt.Println("  no Go signature on file — write the entry point yourself")
	default:
		fmt.Println("  the test is a plain skeleton — this one is not one function with one result")
	}
	fmt.Printf("  read   %s/problem.md\n", p.ContentDir())
	fmt.Printf("  test   go test ./%s/\n", filepath.ToSlash(p.SolutionDir()))
	return nil
}

// writeNew writes files that do not exist yet, leaving existing ones alone so
// your own work is never overwritten.
func writeNew(files map[string]string) ([]string, error) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	var written []string
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(files[path]), 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

func cmdStatus(root string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: nc status <problem> <new|attempted|solved|shaky>")
	}
	state := progress.Status(strings.ToLower(args[len(args)-1]))
	if !state.Valid() {
		return fmt.Errorf("unknown state %q; use one of new, attempted, solved, shaky", args[len(args)-1])
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args[:len(args)-1])
	if err != nil {
		return err
	}
	e, err := prog.SetStatus(p.Slug, state, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("%d. %s → %s\n", p.ID, p.Title, e.Status.Label())
	if e.NextReview != nil {
		fmt.Printf("  review in %s (%s)\n", plural(progress.IntervalDays(e.ReviewStage), "day", "days"),
			e.NextReview.Format("2 Jan 2006"))
	}
	return nil
}

func cmdRate(root string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: nc rate <problem> <1-5>")
	}
	n, err := strconv.Atoi(args[len(args)-1])
	if err != nil {
		return fmt.Errorf("rating must be a number from 1 to 5, got %q", args[len(args)-1])
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args[:len(args)-1])
	if err != nil {
		return err
	}
	e, err := prog.Rate(p.Slug, n, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("%d. %s — rated %d, now %s\n", p.ID, p.Title, n, e.Status.Label())
	if e.NextReview != nil {
		fmt.Printf("  back in %s (%s)\n", plural(progress.IntervalDays(e.ReviewStage), "day", "days"),
			e.NextReview.Format("2 Jan 2006"))
	}
	return nil
}

func cmdTime(root string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: nc time <problem> <minutes>")
	}
	minutes, err := strconv.Atoi(args[len(args)-1])
	if err != nil {
		return fmt.Errorf("minutes must be a number, got %q", args[len(args)-1])
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args[:len(args)-1])
	if err != nil {
		return err
	}
	e, err := prog.AddMinutes(p.Slug, minutes, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("%d. %s — %s logged in total\n", p.ID, p.Title, plural(e.MinutesSpent, "minute", "minutes"))
	return nil
}

func cmdReview(root string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	now := time.Now()
	due := prog.DueReview(cat.Problems, now)
	if len(due) == 0 {
		fmt.Println("Nothing due for review.")
		return nil
	}
	fmt.Printf("%s due:\n", plural(len(due), "problem", "problems"))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range due {
		e := prog.Get(p.Slug)
		over := int(now.Sub(*e.NextReview).Hours() / 24)
		when := "today"
		if over > 0 {
			when = plural(over, "day", "days") + " over"
		}
		fmt.Fprintf(w, "  %d\t%s\t%s\t%s\t%s\n", p.ID, p.Title, p.Difficulty, when, p.Slug)
	}
	return w.Flush()
}

func cmdStats(root string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	now := time.Now()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, g := range cat.Groups() {
		sum := prog.Summarize(g.Problems, now)
		fmt.Fprintf(w, "  %s\t%d/%d\t%s\n", g.Pattern, sum.Done(), sum.Total, bar(sum.Done(), sum.Total))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	total := prog.Summarize(cat.Problems, now)
	fmt.Printf("\n%d of %d solved (%d%%), %d in progress, %d due for review\n",
		total.Done(), total.Total, total.Percent(), total.Attempted, total.DueNow)
	if total.Minutes > 0 {
		fmt.Printf("%s logged\n", plural(total.Minutes, "minute", "minutes"))
	}
	return nil
}

func cmdInfo(root string, args []string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}
	e := prog.Get(p.Slug)
	fmt.Printf("%d. %s\n", p.ID, p.Title)
	fmt.Printf("  slug       %s\n", p.Slug)
	fmt.Printf("  difficulty %s\n", p.Difficulty)
	fmt.Printf("  pattern    %s (guide: content/patterns/%s.md)\n", p.Pattern, p.PatternSlug)
	if p.Blind75 {
		fmt.Printf("  lists      NeetCode 150, Blind 75\n")
	}
	fmt.Printf("  statement  %s/problem.md\n", p.ContentDir())
	fmt.Printf("  hints      %s/hints.md\n", p.ContentDir())
	fmt.Printf("  notes      %s/notes.md\n", p.ContentDir())
	fmt.Printf("  signature  %s/signature.txt\n", p.ContentDir())
	fmt.Printf("  solution   %s/solution.go\n", p.SolutionDir())
	fmt.Printf("  leetcode   %s\n", p.LeetCodeURL)
	if u := p.VideoURL(); u != "" {
		fmt.Printf("  walkthrough %s\n", u)
	}
	fmt.Printf("  state      %s", e.Status.Label())
	if e.Attempts > 0 {
		fmt.Printf(", %s", plural(e.Attempts, "attempt", "attempts"))
	}
	if e.Confidence > 0 {
		fmt.Printf(", last rated %d/5", e.Confidence)
	}
	fmt.Println()
	if e.NextReview != nil {
		fmt.Printf("  review     %s\n", e.NextReview.Format("2 Jan 2006"))
	}
	return nil
}

// ---------- small helpers ----------

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// bar draws a simple fill meter for the terminal.
func bar(done, total int) string {
	const width = 20
	if total == 0 {
		return ""
	}
	filled := done * width / total
	return strings.Repeat("█", filled) + strings.Repeat("·", width-filled)
}
