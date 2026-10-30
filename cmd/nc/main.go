// Command nc is the local NeetCode 150 study kit: it builds the content tree,
// serves the reading site, and tracks what you have solved.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
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
	"neetcode150/internal/textdiff"
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
                               --rung <0-5> notes the highest hint you needed
  nc resolve <problem>         archive the current solution and start a cold re-solve
  nc diff <problem>            the last archived attempt against the current one
  nc drill [--pattern p]       a problem at random, pattern withheld (--solved to revise)
  nc weak [-n 10]              what is least secure, and why
  nc time <problem> <minutes>  add time spent
  nc review                    what is due for review
  nc stats                     progress by pattern
  nc info <problem>            paths and state for one problem
  nc history <problem>         every recorded rating for one problem

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
	case "history":
		return cmdHistory(root, rest)
	case "resolve":
		return cmdResolve(root, rest)
	case "diff":
		return cmdDiff(root, rest)
	case "drill":
		return cmdDrill(root, rest)
	case "weak":
		return cmdWeak(root, rest)
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

	signature, statement := problemInputs(root, p)
	written, files, err := scaffold.Create(root, p, signature, statement)
	if err != nil {
		return err
	}

	now := time.Now()
	if _, err := prog.SetStatus(p.Slug, progress.Attempted, now); err != nil {
		return err
	}
	if _, err := prog.BeginTimer(p.Slug, now); err != nil {
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

// takeIntFlag pulls `--name N` or `--name=N` out of args from any position.
//
// The flag package stops parsing at the first non-flag argument, which would
// force `nc rate --rung 2 two-sum 4`. The rating belongs at the end, so the
// flag has to be extracted by hand.
func takeIntFlag(args []string, name string) (rest []string, value int, found bool, err error) {
	long, short := "--"+name, "-"+name
	for i := 0; i < len(args); i++ {
		a := args[i]
		var raw string
		switch {
		case a == long || a == short:
			if i+1 >= len(args) {
				return nil, 0, false, fmt.Errorf("--%s needs a value", name)
			}
			raw = args[i+1]
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+2:]...)
		case strings.HasPrefix(a, long+"="):
			raw = strings.TrimPrefix(a, long+"=")
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
		case strings.HasPrefix(a, short+"="):
			raw = strings.TrimPrefix(a, short+"=")
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
		default:
			continue
		}
		v, convErr := strconv.Atoi(raw)
		if convErr != nil {
			return nil, 0, false, fmt.Errorf("--%s must be a number, got %q", name, raw)
		}
		return rest, v, true, nil
	}
	return args, 0, false, nil
}

// maxSitting caps what the clock is allowed to contribute. A session left open
// overnight would otherwise log eleven hours and poison the median that
// `nc weak` reads.
const maxSitting = 6 * time.Hour

func cmdRate(root string, args []string) error {
	args, rung, haveRung, err := takeIntFlag(args, "rung")
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: nc rate <problem> <1-5> [--rung <0-5>]")
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

	now := time.Now()
	// Read the clock before rating, because Rate stops it.
	elapsed, running := prog.Get(p.Slug).Elapsed(now)
	logged := 0
	switch {
	case running && elapsed > maxSitting:
		fmt.Printf("  clock ran %.0f hours — ignoring it; `nc time %s <minutes>` if you know\n",
			elapsed.Hours(), p.Slug)
	case running:
		if logged = int(elapsed.Minutes()); logged > 0 {
			if _, err := prog.AddMinutes(p.Slug, logged, now); err != nil {
				return err
			}
		}
	}

	e, err := prog.RateHinted(p.Slug, n, rung, now)
	if err != nil {
		return err
	}
	fmt.Printf("%d. %s — rated %d, now %s\n", p.ID, p.Title, n, e.Status.Label())
	if logged > 0 {
		fmt.Printf("  %s on the clock, %s in total\n",
			plural(logged, "minute", "minutes"), plural(e.MinutesSpent, "minute", "minutes"))
	}
	if haveRung {
		if rung == 0 {
			fmt.Println("  unaided")
		} else {
			fmt.Printf("  needed hint rung %d\n", rung)
		}
		if n >= 4 && rung >= 3 {
			fmt.Printf("  a %d with rung %d is worth a second look: the rating is the felt\n", n, rung)
			fmt.Println("  measure and the rung is the honest one")
		}
	}
	if e.Slipped() {
		fmt.Println("  this fell since last time — the interval it was given was too long")
	}
	if e.NextReview != nil {
		fmt.Printf("  back in %s (%s)\n", plural(progress.IntervalDays(e.ReviewStage), "day", "days"),
			e.NextReview.Format("2 Jan 2006"))
	}
	return nil
}

// cmdHistory prints every rating a problem has had. The trajectory is the point:
// a rating that fell since last time means the interval it was given was too
// long, and the single stored confidence cannot show that.
func cmdHistory(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nc history <problem>")
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}
	e := prog.Get(p.Slug)
	if len(e.History) == 0 {
		fmt.Printf("%d. %s — no ratings recorded yet.\n", p.ID, p.Title)
		fmt.Println("  `nc rate <problem> <1-5>` records one, every sitting.")
		return nil
	}

	fmt.Printf("%d. %s — %s, %s total\n\n", p.ID, p.Title,
		plural(len(e.History), "rating", "ratings"),
		plural(e.MinutesSpent, "minute", "minutes"))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  date\trating\trung\tstage\tinterval\tsitting\t")
	prevMinutes, prevRating := 0, 0
	for i, r := range e.History {
		sitting := "—"
		if d := r.MinutesTotal - prevMinutes; d > 0 {
			sitting = fmt.Sprintf("%d min", d)
		}
		note := ""
		if i > 0 && r.Confidence < prevRating {
			note = "slipped"
		}
		// A high rating next to a high rung is the signature of an inflated
		// self-assessment, so the two sit side by side.
		if r.Confidence >= 4 && r.Rung >= 3 {
			note = strings.TrimSpace(note + " rating looks generous")
		}
		rung := "—"
		if r.Rung > 0 {
			rung = strconv.Itoa(r.Rung)
		}
		fmt.Fprintf(w, "  %s\t%d\t%s\t%d\t%s\t%s\t%s\n",
			r.Date.Format("2 Jan 2006"), r.Confidence, rung, r.Stage,
			plural(progress.IntervalDays(r.Stage), "day", "days"), sitting, note)
		prevMinutes, prevRating = r.MinutesTotal, r.Confidence
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if e.NextReview != nil {
		fmt.Printf("\n  next review %s\n", e.NextReview.Format("2 Jan 2006"))
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

// problemInputs reads the two files a scaffold is generated from. A missing
// file is not an error: a scaffold without a signature still gives you
// somewhere to write, and `nc fetch` fills the gap later.
func problemInputs(root string, p catalog.Problem) (signature, statement string) {
	sig, _ := os.ReadFile(filepath.Join(root, p.ContentDir(), "signature.txt"))
	raw, _ := os.ReadFile(filepath.Join(root, p.ContentDir(), "problem.md"))
	_, body := content.SplitFrontmatter(string(raw))
	return string(sig), body
}

// ---------- re-solving ----------

func attemptsDir(root string, p catalog.Problem) string {
	return filepath.Join(root, p.SolutionDir(), "attempts")
}

// Archives are deliberately not named *.go: they sit inside the solution's own
// package directory, and a second file declaring the same functions would stop
// the package compiling.
const attemptExt = ".go.txt"

// attemptPath is today's archive slot, suffixed if the day already has one.
func attemptPath(dir string, now time.Time) string {
	stamp := now.Format("2006-01-02")
	path := filepath.Join(dir, stamp+attemptExt)
	for n := 2; ; n++ {
		if _, err := os.Stat(path); err != nil {
			return path
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stamp, n, attemptExt))
	}
}

// listAttempts returns the archived attempts, oldest first.
func listAttempts(root string, p catalog.Problem) ([]string, error) {
	dir := attemptsDir(root, p)
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, de := range des {
		if !de.IsDir() && strings.HasSuffix(de.Name(), attemptExt) {
			out = append(out, filepath.Join(dir, de.Name()))
		}
	}
	slices.Sort(out)
	return out, nil
}

// cmdResolve sets up a cold re-solve: the current solution is archived and the
// file is reset to a blank scaffold.
//
// Without this the review flow in tutor/TUTOR.md cannot be followed — it asks
// for a solve from scratch and then a comparison against the old attempt, and
// those two needs conflict as long as there is only one file.
func cmdResolve(root string, args []string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}

	solPath := filepath.Join(root, p.SolutionDir(), "solution.go")
	current, err := os.ReadFile(solPath)
	if err != nil {
		return fmt.Errorf("no solution to archive at %s — `nc start %s` starts a first attempt",
			filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution.go")), p.Slug)
	}

	dir := attemptsDir(root, p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	archive := attemptPath(dir, time.Now())
	if err := os.WriteFile(archive, current, 0o644); err != nil {
		return err
	}

	signature, statement := problemInputs(root, p)
	files := scaffold.Generate(p, signature, statement)
	if err := os.WriteFile(solPath, []byte(files.Solution), 0o644); err != nil {
		return err
	}

	now := time.Now()
	if _, err := prog.SetStatus(p.Slug, progress.Attempted, now); err != nil {
		return err
	}
	if _, err := prog.ResetTimer(p.Slug, now); err != nil {
		return err
	}

	rel, _ := filepath.Rel(root, archive)
	fmt.Printf("%d. %s — %s, %s\n", p.ID, p.Title, p.Difficulty, p.Pattern)
	fmt.Printf("  archived %s\n", filepath.ToSlash(rel))
	fmt.Printf("  reset    %s\n", filepath.ToSlash(filepath.Join(p.SolutionDir(), "solution.go")))
	fmt.Println("  the test file is left alone — it holds the cases, not the approach")
	fmt.Println()
	fmt.Println("  Solve it cold. Do not open the archive until the test passes;")
	fmt.Printf("  then `nc diff %s` shows what changed between attempts.\n", p.Slug)
	fmt.Printf("  clock started — `nc rate %s <1-5>` logs the time automatically.\n", p.Slug)
	return nil
}

// cmdDiff compares the current solution against the most recent archived one.
func cmdDiff(root string, args []string) error {
	cat, _, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}
	attempts, err := listAttempts(root, p)
	if err != nil {
		return err
	}
	if len(attempts) == 0 {
		return fmt.Errorf("no archived attempts for %s — `nc resolve %s` archives one", p.Slug, p.Slug)
	}
	previous := attempts[len(attempts)-1]

	oldSrc, err := os.ReadFile(previous)
	if err != nil {
		return err
	}
	newSrc, err := os.ReadFile(filepath.Join(root, p.SolutionDir(), "solution.go"))
	if err != nil {
		return err
	}

	rel, _ := filepath.Rel(root, previous)
	fmt.Printf("%d. %s — %s against the current solution\n", p.ID, p.Title, filepath.ToSlash(rel))
	if len(attempts) > 1 {
		fmt.Printf("  (%s archived in total)\n", plural(len(attempts), "attempt", "attempts"))
	}
	fmt.Println()

	if textdiff.TooBig(string(oldSrc), string(newSrc)) {
		return fmt.Errorf("these files are too large to diff")
	}
	lines := textdiff.Lines(string(oldSrc), string(newSrc))
	if textdiff.Identical(lines) {
		fmt.Println("  Identical, line for line. Worth asking whether that is memory or understanding.")
		return nil
	}
	for _, l := range lines {
		switch l.Op {
		case textdiff.Delete:
			fmt.Printf("  - %s\n", l.Text)
		case textdiff.Insert:
			fmt.Printf("  + %s\n", l.Text)
		default:
			fmt.Printf("    %s\n", l.Text)
		}
	}
	added, removed := textdiff.Stat(lines)
	fmt.Printf("\n  %s added, %s removed\n",
		plural(added, "line", "lines"), plural(removed, "line", "lines"))
	return nil
}

// ---------- drilling ----------

// cmdDrill picks a problem at random and does not say which pattern it is.
//
// Working the roadmap in order means the pattern is known before the statement
// is read, which is most of the work. Interleaved practice feels worse during a
// session and holds up better weeks later, so it needs its own entry point.
func cmdDrill(root string, args []string) error {
	fs := flag.NewFlagSet("drill", flag.ContinueOnError)
	pattern := fs.String("pattern", "", "draw only from this pattern (name or slug)")
	solved := fs.Bool("solved", false, "draw from problems already solved, for retrieval practice")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}

	pool := cat.Problems
	if *pattern != "" {
		want := strings.ToLower(*pattern)
		var filtered []catalog.Problem
		for _, p := range pool {
			if strings.Contains(strings.ToLower(p.Pattern), want) || strings.Contains(p.PatternSlug, want) {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("no pattern matches %q — `nc stats` lists them", *pattern)
		}
		pool = filtered
	}

	var candidates []catalog.Problem
	for _, p := range pool {
		switch st := prog.Get(p.Slug).Status; {
		case *solved && (st == progress.Solved || st == progress.Shaky):
			candidates = append(candidates, p)
		case !*solved && st == progress.New:
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		if *solved {
			return fmt.Errorf("nothing solved yet to drill")
		}
		return fmt.Errorf("nothing unsolved left in that pool — try --solved")
	}

	p := candidates[rand.IntN(len(candidates))]
	fmt.Printf("%d. %s — %s\n", p.ID, p.Title, p.Difficulty)
	fmt.Printf("  drawn from %s\n", plural(len(candidates), "candidate", "candidates"))
	fmt.Printf("  statement %s/problem.md\n", p.ContentDir())
	fmt.Printf("  start     nc start %s\n", p.Slug)
	fmt.Println()
	fmt.Println("  The pattern is withheld on purpose: name it yourself before you read on.")
	fmt.Println("  It is in the statement's frontmatter, so do not scroll past the title.")
	return nil
}

// ---------- weak spots ----------

type weakness struct {
	problem catalog.Problem
	score   int
	reasons []string
}

// cmdWeak ranks what is least secure, and says why for each one.
//
// The score is only a sort key; the reasons are the output. A number without
// them says "revise this" without saying what went wrong.
func cmdWeak(root string, args []string) error {
	fs := flag.NewFlagSet("weak", flag.ContinueOnError)
	limit := fs.Int("n", 10, "how many to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}

	var minutes []int
	for _, p := range cat.Problems {
		if e := prog.Get(p.Slug); e.MinutesSpent > 0 {
			minutes = append(minutes, e.MinutesSpent)
		}
	}
	slices.Sort(minutes)
	median := 0
	if n := len(minutes); n > 0 {
		median = minutes[n/2]
	}

	var weak []weakness
	for _, p := range cat.Problems {
		e := prog.Get(p.Slug)
		if e.Status != progress.Solved && e.Status != progress.Shaky {
			continue
		}
		w := weakness{problem: p}
		if e.Slipped() {
			w.score += 3
			w.reasons = append(w.reasons, "rating fell since last time")
		}
		if e.Status == progress.Shaky {
			w.score += 2
			w.reasons = append(w.reasons, "flagged shaky")
		}
		switch e.Confidence {
		case 1, 2:
			w.score += 3
			w.reasons = append(w.reasons, fmt.Sprintf("last rated %d", e.Confidence))
		case 3:
			w.score++
			w.reasons = append(w.reasons, "last rated 3")
		}
		if r, ok := e.LastReview(); ok && r.Rung >= 4 {
			w.score += 3
			w.reasons = append(w.reasons, fmt.Sprintf("needed hint rung %d", r.Rung))
		} else if ok && r.Rung >= 2 {
			w.score++
			w.reasons = append(w.reasons, fmt.Sprintf("needed hint rung %d", r.Rung))
		}
		if e.Attempts >= 2 {
			w.score++
			w.reasons = append(w.reasons, plural(e.Attempts, "attempt", "attempts"))
		}
		if median > 0 && e.MinutesSpent >= 2*median {
			w.score++
			w.reasons = append(w.reasons, fmt.Sprintf("%s, well over the %d-minute median", plural(e.MinutesSpent, "minute", "minutes"), median))
		}
		if w.score > 0 {
			weak = append(weak, w)
		}
	}

	if len(weak) == 0 {
		fmt.Println("Nothing is flagged. Either it is all solid, or there is not enough history yet —")
		fmt.Println("`nc rate` after every sitting is what fills this in.")
		return nil
	}
	slices.SortStableFunc(weak, func(a, b weakness) int { return b.score - a.score })
	if len(weak) > *limit {
		weak = weak[:*limit]
	}

	fmt.Printf("%s to shore up, weakest first:\n\n", plural(len(weak), "problem", "problems"))
	for _, w := range weak {
		fmt.Printf("  %d. %s — %s\n", w.problem.ID, w.problem.Title, w.problem.Pattern)
		fmt.Printf("     %s\n", strings.Join(w.reasons, "; "))
		fmt.Printf("     nc resolve %s\n", w.problem.Slug)
	}
	return nil
}
