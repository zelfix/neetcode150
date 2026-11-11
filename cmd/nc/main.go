// Command nc is the local NeetCode 150 study kit: it builds the content tree,
// serves the reading site, and tracks what you have solved.
package main

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
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
	"neetcode150/internal/lang"
	"neetcode150/internal/progress"
	"neetcode150/internal/scaffold"
	"neetcode150/internal/studydb"
	"neetcode150/internal/textdiff"
	"neetcode150/internal/tutor"
	"neetcode150/internal/web"
	"neetcode150/internal/workspace"
)

const usage = `nc — local NeetCode 150 study kit

Usage:
  nc migrate                   import existing personal data into SQLite once
  nc backup <file.sqlite3>      create a consistent database backup
  nc export <name> <file.json>  one person's progress, notes, attempts and tutor transcripts
  nc user list|add|link|revoke|rm   who can reach the site (--base-url, --budget)
                               link --budget <usd> also changes their monthly ceiling
  nc invite new|list|revoke    one link several people may join through
                               new --count <n> --expires 48h [--budget <usd>] [--note …]
  nc notes <problem>           read notes (--file path to replace)
  nc hints <problem>           read hint ladder (--file path to replace)
  nc log                       read the study log (--append-file path to append)
  nc fetch [--force]           download the problem list, statements and signatures
  nc serve [--port 8150]       serve the study site and the tutor panel
                               --tutor-provider claude|codex|deepseek
                               --tutor-mode full|tutor, --tutor-model <name>
                               --tutor-rates <file.json> replaces the DeepSeek price list
                               --sandbox auto|docker|local, --max-runs <n>
                               --sandbox-image <go image>, --sandbox-image-python <py image>
  nc next                      what to work on now
  nc start <problem>           scaffold the solution and notes, mark it in progress
                               --lang go|python chooses the language for this problem
  nc tests <problem>|--all     add example and corner-case tests to existing workspaces
                               --lang go|python picks which workspace (default go)
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

nc <command> --help shows that command's line and its flags.
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
	if len(rest) > 0 && (rest[0] == "-h" || rest[0] == "--help") {
		return commandHelp(cmd)
	}

	root, err := catalog.FindRoot()
	if err != nil {
		return err
	}

	return commandHelpOnErr(cmd, dispatch(cmd, root, rest))
}

// commandHelpOnErr finishes `nc rate two-sum --help`: the command's own flag
// set has printed its flags and returned ErrHelp, so add the usage line rather
// than report a failure.
func commandHelpOnErr(cmd string, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return commandHelp(cmd)
	}
	return err
}

// commandHelp prints one command's lines out of usage, so the flags on rate,
// drill or serve can be found without reading the whole block.
func commandHelp(cmd string) error {
	var out []string
	capturing := false
	for _, line := range strings.Split(usage, "\n") {
		switch {
		case strings.HasPrefix(line, "  nc "+cmd+" ") || line == "  nc "+cmd:
			capturing = true
			out = append(out, line)
		case capturing && strings.HasPrefix(line, "    "):
			out = append(out, line)
		default:
			capturing = false
		}
	}
	if len(out) == 0 {
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	fmt.Println(strings.Join(out, "\n"))
	return nil
}

func dispatch(cmd, root string, rest []string) error {
	switch cmd {
	case "migrate":
		return cmdDatabase(root, rest, false)
	case "backup":
		return cmdDatabase(root, rest, true)
	case "export":
		return cmdExport(root, rest)
	case "user":
		return cmdUser(root, rest)
	case "invite":
		return cmdInvite(root, rest)
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

// cmdExport writes one person's data as JSON. `nc backup` is the whole
// database, token hashes included, which is not something to hand to a guest
// who asks for their own work back.
func cmdExport(root string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: nc export <name> <new-file.json>")
	}
	db, err := studydb.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()
	u, err := findUser(db, args[0])
	if err != nil {
		return err
	}
	path, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); err == nil {
		return fmt.Errorf("export already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	ex, err := db.ExportUser(u.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	rows := 0
	for _, t := range ex.Tables {
		rows += len(t)
	}
	fmt.Printf("Exported %s: %s across %d tables to %s\n", u.Name, plural(rows, "row", "rows"), len(ex.Tables), path)
	return nil
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
		text, err := prog.Scope().Document(slug, kind)
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
		_, err = prog.DB().SQL.Exec("INSERT INTO documents(user_id,slug,kind,text) VALUES(?,?,?,?) ON CONFLICT(user_id,slug,kind) DO UPDATE SET text=documents.text || excluded.text", prog.UserID(), slug, kind, string(data))
	} else {
		err = prog.Scope().SaveDocument(slug, kind, string(data))
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
	model := fs.String("tutor-model", "", "model for an API provider, e.g. deepseek-pro")
	ratesPath := fs.String("tutor-rates", "", "JSON price list for the deepseek provider, see DEPLOY.md")
	sandbox := fs.String("sandbox", string(web.SandboxAuto), "test isolation: auto, docker or local")
	image := fs.String("sandbox-image", "", "container image for Go test runs")
	pyImage := fs.String("sandbox-image-python", "", "container image for Python test runs")
	maxRuns := fs.Int("max-runs", 0, "concurrent test runs, 0 to size from the CPU count")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if p := tutor.Provider(*provider); !p.Valid() {
		return fmt.Errorf("--tutor-provider must be %q, %q or %q, got %q",
			tutor.ProviderClaude, tutor.ProviderCodex, tutor.ProviderDeepSeek, *provider)
	}
	if m := tutor.Mode(*mode); !m.Valid() {
		return fmt.Errorf("--tutor-mode must be %q or %q, got %q", tutor.ModeFull, tutor.ModeTutor, *mode)
	}
	if *budget < 0 || math.IsNaN(*budget) || math.IsInf(*budget, 0) {
		return fmt.Errorf("--tutor-budget must be a finite number greater than or equal to 0")
	}
	if tutor.Provider(*provider) != tutor.ProviderClaude && *budget > 0 {
		return fmt.Errorf("--tutor-budget is a Claude CLI flag and caps one session; " +
			"for a per-person monthly ceiling use `nc user add --budget`")
	}
	if *model != "" && tutor.Provider(*provider) != tutor.ProviderDeepSeek {
		return fmt.Errorf("--tutor-model applies to the deepseek provider; the CLI providers choose their own")
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
	// Resolved before the listener opens: a server that cannot isolate guest
	// code must fail to start rather than start and quietly run it unisolated.
	chosen, err := srv.UseSandbox(web.Sandbox(*sandbox), web.SandboxImages{Go: *image, Python: *pyImage}, *maxRuns)
	if err != nil {
		return err
	}

	coach := tutor.NewForProvider(root, tutor.Provider(*provider), tutor.Mode(*mode), *budget)
	coach.UseModel(*model)
	if *ratesPath != "" {
		if tutor.Provider(*provider) != tutor.ProviderDeepSeek {
			return fmt.Errorf("--tutor-rates prices DeepSeek turns and means nothing for %s", *provider)
		}
		rates, err := tutor.LoadDeepSeekRates(*ratesPath)
		if err != nil {
			return fmt.Errorf("--tutor-rates: %w", err)
		}
		coach.UseRates(rates)
	}
	srv.UseTutor(coach)
	// The server owns claude child processes now, so it has to be stopped in
	// order rather than killed where it stands.
	defer srv.CloseTutor()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("neetcode150 on http://%s  (%d problems, Ctrl-C to stop)\n", addr, len(cat.Problems))
	fmt.Println(chosen)
	// The site serves nobody without a link, so say so rather than let the
	// first visit look like a broken server.
	if users, err := prog.DB().Users(); err == nil {
		linked, guests := 0, 0
		for _, u := range users {
			if u.HasToken {
				linked++
			}
			if u.ID != studydb.OwnerID {
				guests++
			}
		}
		switch {
		case linked == 0:
			fmt.Println("No one has a link yet, so every page will refuse. `nc user link owner` issues yours.")
		case guests > 0:
			fmt.Printf("%s, %s with a link.\n",
				plural(len(users), "person", "people"), plural(linked, "one", "of them"))
		}
	}
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
	defer prog.Close()
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
	fmt.Printf("  solution  %s\n", workspace.Owner(root).Rel(p, languageFor(prog, p.Slug).SolutionFile))
	fmt.Printf("  state     %s", e.Status.Label())
	if e.Attempts > 0 {
		fmt.Printf(", %s", plural(e.Attempts, "attempt", "attempts"))
	}
	fmt.Println()
	return nil
}

func cmdStart(root string, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	langFlag := fs.String("lang", "", "go or python; the default is the owner's setting")
	refArgs, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	defer prog.Close()
	p, err := resolve(cat, refArgs)
	if err != nil {
		return err
	}
	if *langFlag != "" {
		chosen, err := lang.Parse(*langFlag)
		if err != nil {
			return err
		}
		// Recorded before anything is written, so the site and later
		// commands agree on which workspace this problem means.
		if _, err := prog.SetLanguage(p.Slug, string(chosen.ID)); err != nil {
			return err
		}
	}
	l := languageFor(prog, p.Slug)

	signature, statement := problemInputs(root, p, l)
	written, files, err := scaffold.Create(prog.Scope(), workspace.Owner(root), p, l, signature, statement)
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

	fmt.Printf("%d. %s — %s, %s (%s)\n", p.ID, p.Title, p.Difficulty, p.Pattern, l.Label)
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
		fmt.Printf("  no %s signature on file — write the entry point yourself\n", l.Label)
	default:
		fmt.Println("  the test is a plain skeleton — this one is not one function with one result")
	}
	fmt.Printf("  read   %s/problem.md\n", p.ContentDir())
	if l.ID == lang.Go {
		fmt.Printf("  test   go test %s\n", workspace.Owner(root).Pattern(p))
	} else {
		fmt.Printf("  test   %s %s\n", l.RunHint, workspace.Owner(root).PkgDir(p))
	}
	return nil
}

// cmdTests only writes tests: it does not open or change personal study data.
func cmdTests(root string, args []string) error {
	fs := flag.NewFlagSet("tests", flag.ContinueOnError)
	all := fs.Bool("all", false, "every workspace that exists")
	langFlag := fs.String("lang", "go", "which language's workspaces: go or python")
	rest, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}
	l, err := lang.Parse(*langFlag)
	if err != nil {
		return err
	}
	cat, err := catalog.Load(catalogPath(root))
	if err != nil {
		return err
	}
	problems := cat.Problems
	if !*all {
		p, err := resolve(cat, rest)
		if err != nil {
			return err
		}
		problems = []catalog.Problem{p}
	}
	added := 0
	for _, p := range problems {
		paths, err := scaffold.EnsurePracticeTests(workspace.Owner(root), p, l)
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
	defer prog.Close()
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
	defer prog.Close()
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
	defer prog.Close()
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
	if running {
		minutes, capped := progress.ClockMinutes(elapsed)
		switch {
		case capped:
			fmt.Printf("  clock ran %.0f hours — ignoring it; `nc time %s <minutes>` if you know\n",
				elapsed.Hours(), p.Slug)
		case minutes > 0:
			if _, err := prog.AddMinutes(p.Slug, minutes, now); err != nil {
				return err
			}
			logged = minutes
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
	defer prog.Close()
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
	defer prog.Close()
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
	defer prog.Close()
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
	defer prog.Close()
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
	defer prog.Close()
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
	l := languageFor(prog, p.Slug)
	fmt.Printf("  language   %s", l.Label)
	if e.Lang == "" {
		fmt.Print(" (your default)")
	}
	fmt.Println()
	fmt.Printf("  signature  %s/%s\n", p.ContentDir(), l.SignatureFile)
	fmt.Printf("  solution   %s\n", workspace.Owner(root).Rel(p, l.SolutionFile))
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
func problemInputs(root string, p catalog.Problem, l lang.Lang) (signature, statement string) {
	sig, _ := os.ReadFile(filepath.Join(root, p.ContentDir(), l.SignatureFile))
	raw, _ := os.ReadFile(filepath.Join(root, p.ContentDir(), "problem.md"))
	_, body := content.SplitFrontmatter(string(raw))
	return string(sig), body
}

// languageFor is the CLI's one call to the resolution rule.
func languageFor(prog *progress.Store, slug string) lang.Lang {
	return lang.Resolve(prog.Get(slug).Lang)
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
	defer prog.Close()
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}

	now := time.Now()
	l := languageFor(prog, p.Slug)
	signature, statement := problemInputs(root, p, l)
	archive, _, err := scaffold.Reset(prog.Scope(), workspace.Owner(root), p, l, signature, statement, now)
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
	fmt.Printf("  reset    %s\n", workspace.Owner(root).Rel(p, l.SolutionFile))
	fmt.Println("  the test file is left alone — it holds the cases, not the approach")
	fmt.Println()
	fmt.Println("  Solve it cold. Do not open the archive until the test passes;")
	fmt.Printf("  then `nc diff %s` shows what changed between attempts.\n", p.Slug)
	fmt.Printf("  clock started — `nc rate %s <1-5>` logs the time automatically.\n", p.Slug)
	return nil
}

// cmdDiff compares the current solution against the most recent archived one.
func cmdDiff(root string, args []string) error {
	cat, prog, err := load(root)
	if err != nil {
		return err
	}
	defer prog.Close()
	p, err := resolve(cat, args)
	if err != nil {
		return err
	}
	l := languageFor(prog, p.Slug)
	archived, err := prog.LastArchived(p.Slug, l.ID)
	if err != nil {
		return err
	}
	if !archived.Found {
		if archived.Skipped > 0 {
			return fmt.Errorf("no archived %s attempts for %s (%s in another language) — `nc resolve %s` archives one",
				l.Label, p.Slug, plural(archived.Skipped, "attempt", "attempts"), p.Slug)
		}
		return fmt.Errorf("no archived attempts for %s — `nc resolve %s` archives one", p.Slug, p.Slug)
	}
	previous := archived.Attempt.Ref()
	oldSrc := []byte(archived.Attempt.Code)
	newSrc, err := os.ReadFile(workspace.Owner(root).Path(p, l.SolutionFile))
	if err != nil {
		return err
	}

	rel := previous
	fmt.Printf("%d. %s — %s against the current solution\n", p.ID, p.Title, filepath.ToSlash(rel))
	if archived.Matching > 1 {
		fmt.Printf("  (%s archived in total)\n", plural(archived.Matching, "attempt", "attempts"))
	}
	if archived.Skipped > 0 {
		fmt.Printf("  (%s in another language skipped)\n", plural(archived.Skipped, "attempt", "attempts"))
	}
	fmt.Println()

	report := textdiff.Compare(string(oldSrc), string(newSrc))
	if report.TooBig {
		return fmt.Errorf("these files are too large to diff")
	}
	if report.Identical {
		fmt.Println("  Identical, line for line. Worth asking whether that is memory or understanding.")
		return nil
	}
	for _, l := range report.Lines {
		switch l.Op {
		case textdiff.Delete:
			fmt.Printf("  - %s\n", l.Text)
		case textdiff.Insert:
			fmt.Printf("  + %s\n", l.Text)
		default:
			fmt.Printf("    %s\n", l.Text)
		}
	}
	fmt.Printf("\n  %s added, %s removed\n",
		plural(report.Added, "line", "lines"), plural(report.Removed, "line", "lines"))
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
	defer prog.Close()

	pool := catalog.MatchPattern(cat.Problems, *pattern)
	if len(pool) == 0 && *pattern != "" {
		return fmt.Errorf("no pattern matches %q — `nc stats` lists them", *pattern)
	}

	candidates := prog.DrillPool(pool, *solved)
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
	defer prog.Close()
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

// ---------- invites ----------

// cmdInvite mints the authority to create an identity, bounded and delegated
// once.
//
// The site still has no admin surface: it cannot invent a person. What it can
// do is spend a use of a row minted here, before that row's deadline, with the
// ceiling written on it — so the damage a leaked invite can do is three numbers
// the owner chose, rather than open-ended. A link that reaches the wrong chat
// is revoked with `nc invite revoke`, and anybody who already came in through
// it stays revocable on their own, like everybody else.
func cmdInvite(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected: new, list or revoke")
	}
	db, err := studydb.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()

	sub, rest := args[0], args[1:]
	switch sub {
	case "new":
		return inviteNew(db, rest)
	case "list":
		return inviteList(db)
	case "revoke":
		return inviteRevoke(db, rest)
	default:
		return fmt.Errorf("unknown `nc invite` command %q", sub)
	}
}

// maxInviteLife bounds how long an invite may stand. An open-ended one is a
// shared password with extra steps.
const maxInviteLife = 30 * 24 * time.Hour

func inviteNew(db *studydb.DB, args []string) error {
	fs := flag.NewFlagSet("invite new", flag.ContinueOnError)
	base := fs.String("base-url", "http://127.0.0.1:8150", "site address the invite link points at")
	count := fs.Int("count", 1, "how many people may use it")
	budget := fs.Float64("budget", 5, "monthly tutor ceiling in dollars for each person who joins")
	note := fs.String("note", "", "what this invite was for, shown by `nc invite list`")
	life := fs.Duration("expires", 0, "how long it stands, e.g. 48h (required, at most 720h)")
	if _, err := parseWithPositional(fs, args); err != nil {
		return err
	}
	if *life <= 0 {
		return fmt.Errorf("--expires is required: an invite with no deadline is a standing password")
	}
	if *life > maxInviteLife {
		return fmt.Errorf("--expires is at most %s, got %s", maxInviteLife, *life)
	}
	if *budget <= 0 {
		// Zero is not "no money" anywhere else in this program; it is "no
		// ceiling". An invite is the last place that should hand one out.
		return fmt.Errorf("--budget must be above zero: zero means no ceiling at all")
	}
	if *count < 1 {
		return fmt.Errorf("--count must be at least 1, got %d", *count)
	}

	now := time.Now()
	token := crand.Text()
	v, err := db.CreateInvite(studydb.HashToken(token), *count, *budget, *note, now, now.Add(*life))
	if err != nil {
		return err
	}
	fmt.Printf("Invite %d — %s, $%.2f each, good until %s\n\n",
		v.ID, plural(v.UsesLeft, "use", "uses"), v.BudgetUSD, v.ExpiresAt.Format("2 Jan 15:04"))
	fmt.Printf("  %s/invite/%s\n\n", strings.TrimRight(*base, "/"), token)
	fmt.Println("Only its hash is stored, so this is the one time it can be shown.")
	fmt.Println("Whoever opens it picks a name and gets their own link from then on.")
	return nil
}

func inviteList(db *studydb.DB) error {
	invites, err := db.Invites()
	if err != nil {
		return err
	}
	if len(invites) == 0 {
		fmt.Println("No invites. `nc invite new --count 3 --expires 48h` makes one.")
		return nil
	}
	now := time.Now()
	fmt.Printf("%-4s %-6s %-9s %-20s %s\n", "id", "left", "each", "until", "note")
	for _, v := range invites {
		until := v.ExpiresAt.Format("2 Jan 15:04")
		switch {
		case v.UsesLeft < 1:
			until = "spent"
		case !v.ExpiresAt.After(now):
			until = "expired"
		}
		fmt.Printf("%-4d %-6d $%-8.2f %-20s %s\n", v.ID, v.UsesLeft, v.BudgetUSD, until, v.Note)
	}
	return nil
}

func inviteRevoke(db *studydb.DB, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: nc invite revoke <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invite id must be a number, got %q", args[0])
	}
	if err := db.RevokeInvite(id); err != nil {
		return err
	}
	fmt.Printf("Invite %d has no uses left. Anybody who already joined through it keeps their account;\n", id)
	fmt.Println("`nc user revoke <name>` is how you remove one of them.")
	return nil
}

// ---------- users ----------

// cmdUser manages who can reach the site.
//
// The authority to mint lives in the CLI: the site has no admin surface, so an
// attacker who reaches it cannot invent a person. It can only spend a use of an
// invite minted here (see cmdInvite), bounded by that invite's deadline, count
// and ceiling. Only the hash of a token is stored, which is why `link` has to
// issue a new one rather than print the old — a link that can be reprinted is a
// link the database could leak.
func cmdUser(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected: list, add, link, revoke or rm")
	}
	db, err := studydb.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return userList(db)
	case "add":
		return userAdd(db, rest)
	case "link":
		return userLink(db, rest)
	case "revoke":
		return userRevoke(db, rest)
	case "rm":
		return userRemove(db, rest)
	default:
		return fmt.Errorf("unknown `nc user` command %q", sub)
	}
}

func userList(db *studydb.DB) error {
	users, err := db.Users()
	if err != nil {
		return err
	}
	fmt.Printf("%-4s %-16s %-6s %-9s %-9s %s\n", "id", "name", "link", "budget", "spent", "state")
	for _, u := range users {
		link := "no"
		if u.HasToken {
			link = "yes"
		}
		state := "active"
		if u.Disabled {
			state = "disabled"
		}
		budget := "none"
		if u.BudgetUSD > 0 {
			budget = fmt.Sprintf("$%.2f", u.BudgetUSD)
		}
		fmt.Printf("%-4d %-16s %-6s %-9s $%-8.2f %s\n", u.ID, u.Name, link, budget, u.SpendUSD, state)
	}
	if len(users) == 1 {
		fmt.Println("\nOnly the owner exists. `nc user add <name>` invites someone.")
	}
	return nil
}

// parseWithPositional lets flags appear on either side of the name. Go's flag
// package stops at the first non-flag argument, so `nc user add dima --budget 5`
// would silently ignore the budget — which is exactly how people type it.
func parseWithPositional(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// userFlags parses the flags add and link share. budget is nil unless --budget
// was given, so link can tell "leave it" from "set it to the default".
func userFlags(name string, args []string, defaultBudget float64) (names []string, base *string, budget *float64, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	base = fs.String("base-url", "http://127.0.0.1:8150", "site address the join link points at")
	b := fs.Float64("budget", defaultBudget, "monthly tutor ceiling in dollars, 0 for none")
	names, err = parseWithPositional(fs, args)
	if err != nil {
		return nil, nil, nil, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "budget" {
			budget = b
		}
	})
	if budget == nil && name == "user add" {
		budget = b
	}
	if budget != nil && (*budget < 0 || math.IsNaN(*budget) || math.IsInf(*budget, 0)) {
		return nil, nil, nil, fmt.Errorf("--budget must be a finite number greater than or equal to 0")
	}
	return names, base, budget, nil
}

func userAdd(db *studydb.DB, args []string) error {
	names, base, budget, err := userFlags("user add", args, 5)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return fmt.Errorf("expected one name")
	}
	name := names[0]
	token := crand.Text()
	u, err := db.CreateUser(name, studydb.HashToken(token), *budget, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("%s is user %d.\n\n", u.Name, u.ID)
	printJoinLink(*base, token)
	return nil
}

// userLink issues a new link, which also invalidates the previous one. It is
// also where a budget gets changed, since reissuing is the one time an operator
// has the person's row in front of them.
func userLink(db *studydb.DB, args []string) error {
	names, base, budget, err := userFlags("user link", args, 0)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return fmt.Errorf("expected one name")
	}
	u, err := findUser(db, names[0])
	if err != nil {
		return err
	}
	if budget != nil {
		if err := db.SetUserBudget(u.ID, *budget); err != nil {
			return err
		}
		fmt.Printf("%s's monthly tutor ceiling is now $%.2f.\n", u.Name, *budget)
	}
	token := crand.Text()
	if err := db.SetUserToken(u.ID, token); err != nil {
		return err
	}
	fmt.Printf("A new link for %s. Any earlier link has stopped working.\n\n", u.Name)
	printJoinLink(*base, token)
	return nil
}

func printJoinLink(base, token string) {
	fmt.Printf("  %s/join/%s\n\n", strings.TrimRight(base, "/"), token)
	fmt.Println("Opening it once in a browser is the whole sign-in. Only its hash is")
	fmt.Println("stored, so this is the one time it can be shown — send it now or")
	fmt.Println("issue another with `nc user link`.")
}

func userRevoke(db *studydb.DB, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("expected one name")
	}
	u, err := findUser(db, args[0])
	if err != nil {
		return err
	}
	if err := db.SetUserToken(u.ID, ""); err != nil {
		return err
	}
	fmt.Printf("%s can no longer sign in. Their work is untouched; `nc user link %s` lets them back.\n", u.Name, u.Name)
	return nil
}

func userRemove(db *studydb.DB, args []string) error {
	fs := flag.NewFlagSet("user rm", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm that everything of theirs is deleted")
	names, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return fmt.Errorf("expected one name")
	}
	u, err := findUser(db, names[0])
	if err != nil {
		return err
	}
	if !*yes {
		return fmt.Errorf("this deletes %s's progress, notes, attempts and sessions for good — pass --yes to confirm", u.Name)
	}
	if err := db.DeleteUser(u.ID); err != nil {
		return err
	}
	fmt.Printf("%s and everything of theirs is gone.\n", u.Name)
	return nil
}

func findUser(db *studydb.DB, name string) (studydb.User, error) {
	users, err := db.Users()
	if err != nil {
		return studydb.User{}, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) {
			return u, nil
		}
	}
	return studydb.User{}, fmt.Errorf("no user named %q — `nc user list` shows them all", name)
}
