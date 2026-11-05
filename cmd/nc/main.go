// Command nc is the local NeetCode 150 study kit: it builds the content tree,
// serves the reading site, and tracks what you have solved.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
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
	"neetcode150/internal/studydb"
	"neetcode150/internal/textdiff"
	"neetcode150/internal/tutor"
	"neetcode150/internal/web"
)

const usage = `nc — local NeetCode 150 study kit

Usage:
  nc migrate                   import existing personal data into SQLite once
  nc backup <file.sqlite3>      create a consistent database backup
  nc notes <problem>           read notes (--file path to replace)
  nc hints <problem>           read hint ladder (--file path to replace)
  nc log                       read the study log (--append-file path to append)
  nc fetch [--force]           download the problem list, statements and Go signatures
  nc serve [--port 8150]       serve the study site and the tutor panel
                               --tutor-provider claude|codex (default claude)
                               --tutor-mode full|tutor, --tutor-budget <usd>
  nc next                      what to work on now
  nc start <problem>           scaffold the Go solution and notes, mark it in progress
  nc tests <problem>|--all     add example and corner-case tests to existing workspaces
  nc status <problem> <state>  set state: new | attempted | solved | shaky
  nc rate <problem> <1-5>      record how it went and reschedule the review
                               --rung <0-5> notes the highest hint you needed
                               --amend <1-5> corrects the last rating, --undo drops it
  nc resolve <problem>         archive the current solution and start a cold re-solve
  nc diff <problem>            the last archived attempt against the current one
  nc drill [--pattern p]       a problem at random, pattern withheld (--solved to revise)
  nc weak [-n 10]              what is least secure, and why
  nc time <problem> <minutes>  add time spent
  nc review [--limit 8]        what is due for review, most overdue first
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
	case "migrate":
		return cmdDatabase(root, rest, false)
	case "backup":
		return cmdDatabase(root, rest, true)
	case "notes", "hints", "log":
		return cmdDocument(root, cmd, rest)
	case "fetch":
		return cmdFetch(root, rest)
	case "serve":
		return cmdServe(root, rest)
	case "next":
		return cmdNext(root)
	case "start":
		return cmdStart(root, rest)
	case "tests":
		return cmdTests(root, rest)
	case "status":
		return cmdStatus(root, rest)
	case "rate":
		return cmdRate(root, rest)
	case "time":
		return cmdTime(root, rest)
	case "review":
		return cmdReview(root, rest)
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
func progressPath(root string) string { return filepath.Join(root, studydb.Filename) }

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

// Document commands expose personal Markdown without making legacy files live again.
func cmdDocument(root, kind string, args []string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	defer prog.Close()
	slug := ""
	if kind != "log" {
		if len(args) == 0 {
			return fmt.Errorf("usage: nc %s <problem> [--file path | --append-file path]", kind)
		}
		p, err := cat.Resolve(args[0])
		if err != nil {
			return err
		}
		slug = p.Slug
		args = args[1:]
	}
	if len(args) == 0 {
		text, err := prog.DB().Document(slug, kind)
		if err != nil {
			return err
		}
		fmt.Print(text)
		return nil
	}
	if len(args) != 2 || (args[0] != "--file" && args[0] != "--append-file") {
		return fmt.Errorf("expected --file path or --append-file path")
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	if args[0] == "--append-file" {
		_, err = prog.DB().SQL.Exec("INSERT INTO documents VALUES(?,?,?) ON CONFLICT(slug,kind) DO UPDATE SET text=documents.text || excluded.text", slug, kind, string(data))
	} else {
		err = prog.DB().SaveDocument(slug, kind, string(data))
	}
	return err
}

func cmdDatabase(root string, args []string, backup bool) error {
	db, err := studydb.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	if err = db.SQL.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("SQLite integrity check: %s", check)
	}
	if backup {
		if len(args) != 1 {
			return fmt.Errorf("usage: nc backup <new-file.sqlite3>")
		}
		path, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		if _, err = os.Stat(path); err == nil {
			return fmt.Errorf("backup already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err = db.SQL.Exec("VACUUM INTO ?", path); err != nil {
			return err
		}
		if err = os.Chmod(path, 0600); err != nil {
			return err
		}
		fmt.Println("Backup:", path)
		return nil
	}
	if len(args) > 0 {
		return fmt.Errorf("usage: nc migrate")
	}
	var audit string
	if err = db.SQL.QueryRow("SELECT value FROM metadata WHERE key='legacy_import_v1'").Scan(&audit); err != nil {
		return err
	}
	var counts map[string]int
	if err = json.Unmarshal([]byte(audit), &counts); err != nil {
		return err
	}
	fmt.Printf("Database: %s\nImport verified: %d progress entries, %d documents, %d archived attempts\nIntegrity: %s\nLegacy files are preserved and no longer updated.\n", filepath.Join(root, studydb.Filename), counts["progress"], counts["documents"], counts["attempts"], check)
	return nil
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
	provider := fs.String("tutor-provider", string(tutor.ProviderClaude),
		"agent CLI: claude or codex")
	mode := fs.String("tutor-mode", string(tutor.ModeTutor),
		"tutor permissions: tutor (read-only, default) or full")
	budget := fs.Float64("tutor-budget", 0, "dollar ceiling per tutor session, 0 for none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if p := tutor.Provider(*provider); !p.Valid() {
		return fmt.Errorf("--tutor-provider must be %q or %q, got %q", tutor.ProviderClaude, tutor.ProviderCodex, *provider)
	}
	if m := tutor.Mode(*mode); !m.Valid() {
		return fmt.Errorf("--tutor-mode must be %q or %q, got %q", tutor.ModeFull, tutor.ModeTutor, *mode)
	}
	if *budget < 0 || math.IsNaN(*budget) || math.IsInf(*budget, 0) {
		return fmt.Errorf("--tutor-budget must be a finite number greater than or equal to 0")
	}
	if tutor.Provider(*provider) == tutor.ProviderCodex && *budget > 0 {
		return fmt.Errorf("--tutor-budget only applies to the Claude provider")
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	defer prog.Close()
	store := content.NewStore(root, content.NewRenderer())
	srv, err := web.New(root, cat, store, prog)
	if err != nil {
		return err
	}
	coach := tutor.NewForProvider(root, tutor.Provider(*provider), tutor.Mode(*mode), *budget)
	srv.UseTutor(coach)
	// The server owns claude child processes now, so it has to be stopped in
	// order rather than killed where it stands.
	defer srv.CloseTutor()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("neetcode150 on http://%s  (%d problems, Ctrl-C to stop)\n", addr, len(cat.Problems))
	if ok, why := coach.Available(); ok {
		fmt.Printf("tutor panel: %s, %s mode\n", *provider, *mode)
	} else {
		fmt.Printf("tutor panel off — %s\n", why)
	}
	err = web.Listen(ctx, addr, srv.Handler())
	fmt.Println("stopping tutor sessions…")
	return err
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
	case files.PracticeCases > 0:
		fmt.Printf("  %d practice cases — examples and corner cases\n", files.PracticeCases)
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

// cmdTests only writes tests: it does not open or change personal study data.
func cmdTests(root string, args []string) error {
	cat, err := catalog.Load(catalogPath(root))
	if err != nil {
		return err
	}
	problems := cat.Problems
	if len(args) != 1 || args[0] != "--all" {
		p, err := resolve(cat, args)
		if err != nil {
			return err
		}
		problems = []catalog.Problem{p}
	}
	added := 0
	for _, p := range problems {
		paths, err := scaffold.EnsurePracticeTests(root, p)
		if err != nil {
			return err
		}
		for _, path := range paths {
			rel, _ := filepath.Rel(root, path)
			fmt.Printf("  created %s\n", rel)
			added++
		}
	}
	fmt.Printf("Added %d test suites. Unstarted problems receive their tests on Start.\n", added)
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
// takeBoolFlag pulls a valueless flag out of args, wherever it sits, so that
// the problem reference can stay where it reads naturally.
func takeBoolFlag(args []string, name string) (rest []string, found bool) {
	long, short := "--"+name, "-"+name
	for _, a := range args {
		if a == long || a == short {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

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

// printSchedule reports when a problem comes back, using the interval the
// schedule is actually holding to rather than the nominal ladder rung — ease
// and the per-problem spread both move it.
func printSchedule(e progress.Entry) {
	if e.NextReview == nil {
		return
	}
	fmt.Printf("  back in %s (%s)\n", plural(e.IntervalDays(), "day", "days"),
		e.NextReview.Format("2 Jan 2006"))
}

// cmdRateUndo drops the last rating. Ratings drive the whole schedule, so a
// mistyped one is worth being able to take back.
func cmdRateUndo(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nc rate <problem> --undo")
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}
	e, dropped, ok, err := prog.Undo(p.Slug, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		fmt.Printf("%d. %s — no rating to undo.\n", p.ID, p.Title)
		return nil
	}
	fmt.Printf("%d. %s — dropped the %d from %s, now %s\n", p.ID, p.Title,
		dropped.Confidence, dropped.Date.Format("2 Jan 2006"), e.Status.Label())
	if e.MinutesSpent > 0 {
		fmt.Printf("  %s stays logged: the sitting happened either way\n",
			plural(e.MinutesSpent, "minute", "minutes"))
	}
	printSchedule(e)
	return nil
}

func cmdRate(root string, args []string) error {
	args, undo := takeBoolFlag(args, "undo")
	if undo {
		return cmdRateUndo(root, args)
	}
	args, amend := takeBoolFlag(args, "amend")
	args, rung, haveRung, err := takeIntFlag(args, "rung")
	if err != nil {
		return err
	}
	if len(args) < 2 {
		if amend {
			return fmt.Errorf("usage: nc rate <problem> --amend <1-5> [--rung <0-5>]")
		}
		return fmt.Errorf("usage: nc rate <problem> <1-5> [--rung <0-5>] | --amend <1-5> | --undo")
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
	if amend {
		e, ok, err := prog.Amend(p.Slug, n, rung, now)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Printf("%d. %s — no rating to amend; `nc rate %s %d` records the first one.\n",
				p.ID, p.Title, p.Slug, n)
			return nil
		}
		fmt.Printf("%d. %s — last rating corrected to %d, now %s\n", p.ID, p.Title, n, e.Status.Label())
		printSchedule(e)
		return nil
	}

	// Read the clock and the stage before rating, because Rate stops the one
	// and moves the other.
	prev := prog.Get(p.Slug)
	elapsed, running := prev.Elapsed(now)
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
		// The rating is the felt measure and the rung is the honest one, so say
		// plainly when the rung is what decided the schedule.
		held := max(1, prev.ReviewStage)
		switch {
		case e.ReviewStage < held:
			fmt.Printf("  rung %d is not recall — stage %d, down from %d\n", rung, e.ReviewStage, held)
		case rung >= 3 && n >= 4:
			fmt.Printf("  rung %d held it at stage %d rather than earning the next one\n", rung, e.ReviewStage)
		}
	}
	if e.Slipped() {
		fmt.Println("  this fell since last time — the interval it was given was too long")
	}
	printSchedule(e)
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
	for _, st := range progress.Sittings(e) {
		sitting := "—"
		if st.Minutes > 0 {
			sitting = fmt.Sprintf("%d min", st.Minutes)
		}
		note := ""
		if st.Slipped {
			note = "slipped"
		}
		// A high rating next to a high rung is the signature of an inflated
		// self-assessment, so the two sit side by side.
		if st.Generous {
			note = strings.TrimSpace(note + " rating looks generous")
		}
		rung := "—"
		if st.Rung > 0 {
			rung = strconv.Itoa(st.Rung)
		}
		fmt.Fprintf(w, "  %s\t%d\t%s\t%d\t%s\t%s\t%s\n",
			st.Date.Format("2 Jan 2006"), st.Confidence, rung, st.Stage,
			plural(st.IntervalDays, "day", "days"), sitting, note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if e.NextReview != nil {
		fmt.Printf("\n  next review %s, in %s\n", e.NextReview.Format("2 Jan 2006"),
			plural(e.IntervalDays(), "day", "days"))
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

// reviewLimit is how many due reviews one sitting is asked to carry. Reviews
// arrive in heaps, and a list of thirty is a list nobody starts.
const reviewLimit = 8

func cmdReview(root string, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	limit := fs.Int("limit", reviewLimit, "how many to list; 0 for all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	now := time.Now()
	total := len(prog.DueReview(cat.Problems, now))
	if total == 0 {
		fmt.Println("Nothing due for review.")
		return nil
	}
	due := prog.ReviewQueue(cat.Problems, now, *limit)
	if len(due) < total {
		fmt.Printf("%s due, the %d most overdue first:\n", plural(total, "problem", "problems"), len(due))
	} else {
		fmt.Printf("%s due, most overdue first:\n", plural(total, "problem", "problems"))
	}
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
	reports := prog.Patterns(cat.Problems, now)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range reports {
		// The rung and the unaided share are blank rather than zero where
		// nothing is rated: a pattern not started has not scored 0%.
		held, rung := "", ""
		if r.Rated > 0 {
			held = fmt.Sprintf("%d%% unaided", r.UnaidedShare())
			rung = fmt.Sprintf("rung %.1f", r.Rung)
		}
		fmt.Fprintf(w, "  %s\t%d/%d\t%s\t%s\t%s\n",
			r.Pattern, r.Solved, r.Total, bar(r.Solved, r.Total), held, rung)
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
	if concerns := progress.ByConcern(reports); len(concerns) > 0 {
		fmt.Printf("\nworth going back to: ")
		for i, r := range concerns[:min(len(concerns), 3)] {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Print(r.Pattern)
		}
		fmt.Println("\n  `nc weak` names the problems; the Coach page on the site shows the whole picture")
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
	fmt.Printf("  hints      nc hints %s (SQLite)\n", p.Slug)
	fmt.Printf("  notes      nc notes %s (SQLite)\n", p.Slug)
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
		fmt.Printf("  review     %s, %s after the last sitting\n",
			e.NextReview.Format("2 Jan 2006"), plural(e.IntervalDays(), "day", "days"))
	}
	if e.Ease != 0 {
		fmt.Printf("  ease       %.2f (stage %d of %d)\n", e.Ease, e.ReviewStage, progress.MaxStage)
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

	now := time.Now()
	signature, statement := problemInputs(root, p)
	archive, _, err := scaffold.Reset(root, p, signature, statement, now)
	if err != nil {
		return fmt.Errorf("%w — `nc start %s` starts a first attempt", err, p.Slug)
	}

	if _, err := prog.SetStatus(p.Slug, progress.Attempted, now); err != nil {
		return err
	}
	if _, err := prog.ResetTimer(p.Slug, now); err != nil {
		return err
	}

	rel := archive
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
	attempts, err := scaffold.Attempts(root, p)
	if err != nil {
		return err
	}
	if len(attempts) == 0 {
		return fmt.Errorf("no archived attempts for %s — `nc resolve %s` archives one", p.Slug, p.Slug)
	}
	previous := attempts[len(attempts)-1]

	oldAttempt, err := scaffold.ReadAttempt(root, previous)
	oldSrc := []byte(oldAttempt.Code)
	if err != nil {
		return err
	}
	newSrc, err := os.ReadFile(filepath.Join(root, p.SolutionDir(), "solution.go"))
	if err != nil {
		return err
	}

	rel := previous
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
	weak := prog.Weakest(cat.Problems, *limit)

	if len(weak) == 0 {
		fmt.Println("Nothing is flagged. Either it is all solid, or there is not enough history yet —")
		fmt.Println("`nc rate` after every sitting is what fills this in.")
		return nil
	}
	fmt.Printf("%s to shore up, weakest first:\n\n", plural(len(weak), "problem", "problems"))
	for _, w := range weak {
		fmt.Printf("  %d. %s — %s\n", w.Problem.ID, w.Problem.Title, w.Problem.Pattern)
		fmt.Printf("     %s\n", strings.Join(w.Reasons, "; "))
		fmt.Printf("     nc resolve %s\n", w.Problem.Slug)
	}
	return nil
}
