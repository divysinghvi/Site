// Command api is the divy binary: `divy serve` (the default when Vercel runs
// the binary with no arguments), `collect`, `validate`, `schemagen`,
// `migrate`, `export-ascii` and `ping`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata" // Asia/Kolkata must load on distroless and Vercel

	"divy.dev/internal/ascii"
	"divy.dev/internal/collector"
	"divy.dev/internal/config"
	"divy.dev/internal/content"
	"divy.dev/internal/schemagen"
	"divy.dev/internal/server"
	"divy.dev/internal/store"
	"divy.dev/internal/version"
)

const usage = `divy — the divy.dev observability binary

Usage:
  divy [serve] [flags]      serve the API and the embedded site (default; Vercel runs this)
  divy collect --once       run one collection round and print the summary
  divy validate             validate content/ (schema + cross-file rules)
  divy schemagen            write schema/*.schema.json from the Go structs
  divy migrate              apply pending database migrations
  divy export-ascii         print the ASCII career trace
  divy ping                 GET a URL, exit 0 on 2xx (healthchecks)
  divy version              print the build identity

Every flag has an env twin (see .env.example); flags win over env.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return cmdServe(args, stdout, stderr)
	case "collect":
		return cmdCollect(args, stdout, stderr)
	case "validate":
		return cmdValidate(args, stdout, stderr)
	case "schemagen":
		return cmdSchemagen(args, stdout, stderr)
	case "migrate":
		return cmdMigrate(args, stdout, stderr)
	case "export-ascii":
		return cmdExportASCII(args, stdout, stderr)
	case "ping":
		return cmdPing(args, stdout, stderr)
	case "version", "--version", "-version":
		fmt.Fprintf(stdout, "divy %s (%s) built %s %s\n", version.Version, version.Commit, version.Date, version.GoVersion())
		return 0
	case "help", "-h", "--help", "-help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "divy: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// common flags shared by the subcommands
type common struct {
	cfg config.Config
}

func newFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *common, error) {
	cfg, err := config.FromEnv(config.Getenv)
	if err != nil {
		return nil, nil, err
	}
	fs := flag.NewFlagSet("divy "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	c := &common{cfg: cfg}
	fs.StringVar(&c.cfg.LogLevel, "log-level", cfg.LogLevel, "log level: debug|info|warn|error (DIVY_LOG_LEVEL)")
	fs.StringVar(&c.cfg.LogFormat, "log-format", cfg.LogFormat, "log format: text|json (DIVY_LOG_FORMAT)")
	return fs, c, nil
}

func (c *common) logger(w io.Writer) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(c.cfg.LogLevel) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if strings.ToLower(c.cfg.LogFormat) == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "divy: %v\n", err)
	return 1
}

// loadContent loads and validates content; on errors it prints the report and returns nil.
func loadContent(dir, selfURL, origin string, strict bool, stderr io.Writer) (*content.Content, *content.Report) {
	c, err := content.Load(dir, content.Options{SelfURL: selfURL, SiteOrigin: origin})
	if err != nil {
		fmt.Fprintf(stderr, "divy: %v\n", err)
		return nil, nil
	}
	if c.Report.HasErrors(strict) {
		c.Report.Write(stderr, strict)
		return nil, c.Report
	}
	return c, c.Report
}

// ---- serve ----

func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs, c, err := newFlagSet("serve", stderr)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := &c.cfg
	var collect bool
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address (PORT wins, then DIVY_ADDR)")
	fs.StringVar(&cfg.DBURL, "db", cfg.DBURL, "database URL: file:PATH or libsql://… (DIVY_DB_URL; TURSO_DATABASE_URL wins)")
	fs.StringVar(&cfg.ContentDir, "content", cfg.ContentDir, "content directory (DIVY_CONTENT_DIR)")
	fs.StringVar(&cfg.SiteOrigin, "site-origin", cfg.SiteOrigin, "absolute site origin for links (SITE_ORIGIN)")
	fs.BoolVar(&collect, "collect", false, "run the in-process collector scheduler")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := c.logger(stderr)
	slog.SetDefault(log)
	if cfg.SiteOrigin == "" {
		cfg.SiteOrigin = "http://localhost" + defaultPortSuffix(cfg.Addr)
	}
	selfURL := cfg.UptimeSelfURL
	if selfURL == "" {
		selfURL = strings.TrimRight(cfg.SiteOrigin, "/") + "/readyz"
	}
	start := time.Now()
	cnt, _ := loadContent(cfg.ContentDir, selfURL, cfg.SiteOrigin, false, stderr)
	if cnt == nil {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBURL)
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = st.Close() }()

	reg := collector.NewRegistry()
	_ = reg.Register(collector.Process{})
	runner := &collector.Runner{Store: st, Registry: reg, Logger: log}

	var shuttingDown atomic.Bool
	if len(cfg.CollectTokens) == 0 {
		log.Warn("DIVY_COLLECT_TOKEN and CRON_SECRET are empty: /api/collect will answer 401 to every request")
	}
	srv, err := server.New(server.Config{
		Content:             cnt,
		Store:               st,
		Runner:              runner,
		Logger:              log,
		Version:             version.Version,
		Commit:              version.Commit,
		StartedAt:           start,
		Branch:              version.Branch,
		BuildUser:           version.BuildUser,
		BuildDate:           version.Date,
		SiteOrigin:          cfg.SiteOrigin,
		CollectTokens:       cfg.CollectTokens,
		CollectBudget:       cfg.CollectBudget,
		OTelServiceName:     cfg.OTelServiceName,
		ShuttingDown:        shuttingDown.Load,
		QueryLookback:       cfg.QueryLookback,
		QueryTimeout:        cfg.QueryTimeout,
		QueryMaxSamples:     cfg.QueryMaxSamples,
		QueryMaxConcurrency: cfg.QueryMaxConcurrency,
		CollectorIntervals: map[string]time.Duration{
			"github": cfg.IntervalGitHub, "pypi": cfg.IntervalPyPI, "uptime": cfg.IntervalUptime, "manual": cfg.IntervalManual, "retention": cfg.IntervalRetention,
		},
	})
	if err != nil {
		return fail(stderr, err)
	}
	var sched *collector.Scheduler
	if collect {
		sched = &collector.Scheduler{Runner: runner}
		sched.Start(ctx)
	}
	hs := &http.Server{Addr: cfg.Addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fail(stderr, err)
	}
	log.Info("divy serve", "addr", ln.Addr().String(), "db", st.Mode, "content", cfg.ContentDir, "spans", len(cnt.Nodes()), "logs", len(cnt.Logs), "todos", len(cnt.Todos), "collect", collect, "startup_ms", time.Since(start).Milliseconds(), "version", version.Version)
	errCh := make(chan error, 1)
	go func() { errCh <- hs.Serve(ln) }()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fail(stderr, err)
		}
	case <-ctx.Done():
	}
	shuttingDown.Store(true)
	log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if sched != nil {
		sched.Stop(shCtx)
	}
	if err := hs.Shutdown(shCtx); err != nil {
		log.Warn("shutdown", "err", err.Error())
	}
	return 0
}

func defaultPortSuffix(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	return ":" + port
}

// ---- collect ----

func cmdCollect(args []string, stdout, stderr io.Writer) int {
	fs, c, err := newFlagSet("collect", stderr)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := &c.cfg
	var once bool
	var only string
	var budget time.Duration
	fs.BoolVar(&once, "once", false, "run one round and exit (otherwise run the scheduler until interrupted)")
	fs.StringVar(&only, "only", "", "comma list of collector names (default all)")
	fs.DurationVar(&budget, "budget", cfg.CollectBudget, "round budget (DIVY_COLLECT_BUDGET)")
	fs.StringVar(&cfg.DBURL, "db", cfg.DBURL, "database URL (DIVY_DB_URL)")
	fs.StringVar(&cfg.ContentDir, "content", cfg.ContentDir, "content directory (DIVY_CONTENT_DIR)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := c.logger(stderr)
	if cnt, _ := loadContent(cfg.ContentDir, cfg.UptimeSelfURL, cfg.SiteOrigin, false, stderr); cnt == nil {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, cfg.DBURL)
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = st.Close() }()
	reg := collector.NewRegistry()
	_ = reg.Register(collector.Process{})
	runner := &collector.Runner{Store: st, Registry: reg, Logger: log}
	var names []string
	if only != "" {
		for _, n := range strings.Split(only, ",") {
			if n = strings.TrimSpace(n); n != "" {
				if _, ok := reg.Get(n); !ok {
					return fail(stderr, fmt.Errorf("unknown collector %q (have: %s)", n, strings.Join(reg.Names(), ", ")))
				}
				names = append(names, n)
			}
		}
	}
	if !once {
		sched := &collector.Scheduler{Runner: runner}
		sched.Start(ctx)
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sched.Stop(shCtx)
		return 0
	}
	sum := runner.RunRound(ctx, budget, names...)
	fmt.Fprintf(stdout, "%-12s %-5s %6s %9s  %s\n", "collector", "ok", "items", "duration", "error")
	rc := 0
	for _, r := range sum.Collectors {
		fmt.Fprintf(stdout, "%-12s %-5v %6d %7dms  %s\n", r.Name, r.OK, r.Items, r.DurationMs, r.Error)
		if !r.OK && !strings.HasPrefix(r.Error, "skipped:") {
			rc = 1
		}
	}
	fmt.Fprintf(stdout, "budget %dms truncated=%v\n", sum.BudgetMs, sum.Truncated)
	return rc
}

// ---- validate ----

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	fs, c, err := newFlagSet("validate", stderr)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := &c.cfg
	var strict, listTodos, asJSON bool
	fs.StringVar(&cfg.ContentDir, "content", cfg.ContentDir, "content directory (DIVY_CONTENT_DIR)")
	fs.BoolVar(&strict, "strict", false, "treat warnings as errors")
	fs.BoolVar(&listTodos, "list-todos", false, "print the TODO(divy) inventory")
	fs.BoolVar(&asJSON, "json", false, "machine-readable report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 1 {
		cfg.ContentDir = fs.Arg(0)
	}
	cnt, err := content.Load(cfg.ContentDir, content.Options{SelfURL: cfg.UptimeSelfURL, SiteOrigin: cfg.SiteOrigin})
	if err != nil {
		return fail(stderr, err)
	}
	rep := cnt.Report
	switch {
	case asJSON:
		_, _ = stdout.Write(rep.JSON(strict))
	case listTodos:
		rep.WriteTodos(stdout)
	default:
		rep.Write(stdout, strict)
	}
	if rep.HasErrors(strict) {
		return 1
	}
	return 0
}

// ---- schemagen ----

func cmdSchemagen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("divy schemagen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var out, comments string
	var check bool
	fs.StringVar(&out, "out", "./schema", "output directory")
	fs.StringVar(&comments, "comments", "internal/model", "path of internal/model (Go doc comments become descriptions); '' to skip")
	fs.BoolVar(&check, "check", false, "write nothing; exit 1 when the files on disk differ")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if comments != "" {
		if st, err := os.Stat(comments); err != nil || !st.IsDir() {
			fmt.Fprintf(stderr, "divy schemagen: %s not found; descriptions omitted (run from the repository root)\n", comments)
			comments = ""
		}
	}
	docs, err := schemagen.Generate(schemagen.Options{CommentsDir: comments})
	if err != nil {
		return fail(stderr, err)
	}
	if check {
		diff, err := schemagen.Check(out, docs)
		if err != nil {
			return fail(stderr, err)
		}
		if len(diff) > 0 {
			fmt.Fprintf(stderr, "schema drift in %s: %s (run: go run ./cmd/api schemagen --out %s)\n", out, strings.Join(diff, ", "), out)
			return 1
		}
		fmt.Fprintf(stdout, "schema/ is up to date (%d files)\n", len(docs))
		return 0
	}
	paths, err := schemagen.Write(out, docs)
	if err != nil {
		return fail(stderr, err)
	}
	for _, p := range paths {
		fmt.Fprintln(stdout, p)
	}
	return 0
}

// ---- migrate ----

func cmdMigrate(args []string, stdout, stderr io.Writer) int {
	fs, c, err := newFlagSet("migrate", stderr)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := &c.cfg
	var status bool
	var to int
	fs.StringVar(&cfg.DBURL, "db", cfg.DBURL, "database URL (DIVY_DB_URL)")
	fs.BoolVar(&status, "status", false, "print applied/pending migrations")
	fs.IntVar(&to, "to", -1, "apply pending migrations up to this version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBURL) // Open already applies every pending migration
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = st.Close() }()
	if to >= 0 {
		if _, err := st.MigrateTo(ctx, to); err != nil {
			return fail(stderr, err)
		}
	}
	rows, err := st.Status(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "%-8s %-20s %s\n", "version", "name", "applied_at")
	for _, r := range rows {
		at := "pending"
		if r.Applied {
			at = r.AppliedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%04d     %-20s %s\n", r.Version, r.Name, at)
	}
	_ = status
	return 0
}

// ---- export-ascii ----

func cmdExportASCII(args []string, stdout, stderr io.Writer) int {
	fs, c, err := newFlagSet("export-ascii", stderr)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := &c.cfg
	var width int
	var color bool
	fs.StringVar(&cfg.ContentDir, "content", cfg.ContentDir, "content directory (DIVY_CONTENT_DIR)")
	fs.StringVar(&cfg.SiteOrigin, "origin", cfg.SiteOrigin, "site origin printed in the footer (SITE_ORIGIN)")
	fs.IntVar(&width, "width", 80, "total width in columns (60..200)")
	fs.BoolVar(&color, "color", false, "ANSI 24-bit colour on service names")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cnt, _ := loadContent(cfg.ContentDir, cfg.UptimeSelfURL, cfg.SiteOrigin, false, stderr)
	if cnt == nil {
		return 1
	}
	fmt.Fprint(stdout, ascii.Render(cnt, ascii.Options{Width: width, Origin: cfg.SiteOrigin, Color: color}))
	return 0
}

// ---- ping ----

func cmdPing(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("divy ping", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var u string
	var timeout time.Duration
	fs.StringVar(&u, "url", "http://127.0.0.1:8080/readyz", "URL to GET")
	fs.DurationVar(&timeout, "timeout", 3*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(u)
	if err != nil {
		fmt.Fprintf(stderr, "ping %s: %v\n", u, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	fmt.Fprintf(stdout, "%s %d\n", u, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 1
	}
	return 0
}
