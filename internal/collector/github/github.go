package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/store"
)

// Metric names written by this collector (all in the metrics catalogue).
const (
	MetricCommits       = "github_commits_total"
	MetricContributions = "github_contributions_total"
	MetricMergedPRs     = "github_merged_prs_total"
	MetricMergedByRepo  = "github_merged_prs_by_repo_total"
	MetricStars         = "github_stars"
	MetricFollowers     = "github_followers"
	MetricOpenPRs       = "oss_prs_open"
)

// BackfillDays is the depth of the commits/contributions window.
const BackfillDays = 365

// Config configures the collector.
type Config struct {
	// Token is DIVY_GITHUB_TOKEN; empty disables the collector.
	Token string
	// Login is the GitHub login whose activity is collected (DIVY_GITHUB_LOGIN).
	Login string
	// PrivateOrgs are owners whose repositories are counted but never named (DIVY_GITHUB_PRIVATE_ORGS).
	PrivateOrgs []string
	// Interval is the scheduler cadence (COLLECT_GITHUB_INTERVAL).
	Interval time.Duration
	// Endpoint overrides the GraphQL endpoint (tests).
	Endpoint string
	// UserAgent is sent with every request.
	UserAgent string
	// HTTPClient overrides the outbound client (tests).
	HTTPClient *http.Client
	// MaxPRPages bounds the merged-PR scan per run (default 5, 100 PRs each);
	// the scan resumes from collector_state on the next run.
	MaxPRPages int
	// MinRemaining aborts a run when fewer rate-limit points remain (default 200).
	MinRemaining int
	// RetryDelay is the wait before the single retry (default 2s).
	RetryDelay time.Duration
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
}

// Collector implements collector.Collector.
type Collector struct {
	cfg     Config
	st      *store.Store
	cl      *client
	private map[string]bool
}

// New builds the collector.
func New(cfg Config, st *store.Store) *Collector {
	if cfg.Login == "" {
		cfg.Login = "divysinghvi"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Minute
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = collector.UserAgent("")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = collector.NewHTTPClient(30 * time.Second)
	}
	if cfg.MaxPRPages <= 0 {
		cfg.MaxPRPages = 5
	}
	if cfg.MinRemaining <= 0 {
		cfg.MinRemaining = 200
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 2 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	c := &Collector{cfg: cfg, st: st, private: map[string]bool{}}
	for _, o := range cfg.PrivateOrgs {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			c.private[o] = true
		}
	}
	c.cl = &client{endpoint: cfg.Endpoint, token: cfg.Token, userAgent: cfg.UserAgent, http: cfg.HTTPClient, retryDelay: cfg.RetryDelay}
	return c
}

// Name is "github".
func (c *Collector) Name() string { return "github" }

// Interval is the configured cadence.
func (c *Collector) Interval() time.Duration { return c.cfg.Interval }

// Disabled is true without a token.
func (c *Collector) Disabled() bool { return c.cfg.Token == "" }

// isPrivate applies the privacy rule: a private repository or an owner in
// PrivateOrgs is counted under its owner but never named.
func (c *Collector) isPrivate(owner string, repoPrivate bool) bool {
	return repoPrivate || c.private[strings.ToLower(owner)]
}

// Run performs one collection: Q1 (contributions, 4 windows) → counters;
// Q1 followers + Q3 + Q4 → gauges; Q2 merged PRs → resumable scan. Each
// phase commits on its own, so a timeout in a later phase keeps the earlier
// data (the run is still reported as failed and retried).
func (c *Collector) Run(ctx context.Context) (collector.Result, error) {
	if c.Disabled() {
		return collector.Result{}, fmt.Errorf("%w: DIVY_GITHUB_TOKEN is empty", collector.ErrDisabled)
	}
	now := c.cfg.Now().UTC()
	res := collector.Result{}
	var notes []string

	contrib, err := c.fetchContributions(ctx, now)
	if err != nil {
		return res, err
	}
	notes = append(notes, contrib.notes...)
	n, err := c.writeContributions(ctx, contrib, now)
	res.Items += n
	if err != nil {
		return res, err
	}

	n, rl, err := c.writeGauges(ctx, contrib.followers, now)
	res.Items += n
	if err != nil {
		return res, err
	}

	n, prNote, err := c.scanMergedPRs(ctx, contrib.years, now)
	res.Items += n
	if err != nil {
		return res, err
	}
	notes = append(notes, prNote)
	notes = append(notes, fmt.Sprintf("gh_remaining=%d gh_reset=%s", rl.Remaining, rl.ResetAt))
	res.Note = strings.Join(notes, "; ")
	return res, nil
}

// checkRateLimit aborts a run that is close to the hourly budget.
func (c *Collector) checkRateLimit(rl rateLimit) error {
	if rl.Cost == 0 && rl.Remaining == 0 && rl.ResetAt == "" {
		return nil // query did not select rateLimit (should not happen)
	}
	if rl.Remaining < c.cfg.MinRemaining {
		return fmt.Errorf("%w: %d points remaining, resets at %s", ErrRateLimited, rl.Remaining, rl.ResetAt)
	}
	return nil
}

// ---- Q1: contributions ----

type contributions struct {
	w0            time.Time
	commits       collector.DailyCounts
	contributions collector.DailyCounts
	followers     int
	years         []int
	notes         []string
}

type window struct {
	from, to time.Time
}

// windows splits [today − 365d, now] into four consecutive ranges of at most
// 92 days so that contributions(first: 100) never paginates.
func windows(now time.Time) []window {
	today := collector.DayOf(now)
	w0 := today.AddDate(0, 0, -BackfillDays)
	total := int(today.Sub(w0).Hours()/24) + 1 // days including today
	per := (total + 3) / 4
	var out []window
	start := w0
	for i := 0; i < 4 && !start.After(today); i++ {
		end := start.AddDate(0, 0, per-1)
		if end.After(today) || i == 3 {
			end = today
		}
		to := end.AddDate(0, 0, 1).Add(-time.Second)
		if !end.Before(today) {
			to = now
		}
		out = append(out, window{from: start, to: to})
		start = end.AddDate(0, 0, 1)
	}
	return out
}

func (c *Collector) fetchContributions(ctx context.Context, now time.Time) (*contributions, error) {
	ws := windows(now)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		i    int
		data contributionsData
		rl   rateLimit
		err  error
	}
	results := make([]result, len(ws))
	var wg sync.WaitGroup
	for i, w := range ws {
		wg.Add(1)
		go func(i int, w window) {
			defer wg.Done()
			var data contributionsData
			rl, err := c.cl.query(ctx, queryContributions, map[string]any{
				"login": c.cfg.Login,
				"from":  w.from.Format(time.RFC3339),
				"to":    w.to.Format(time.RFC3339),
			}, &data)
			results[i] = result{i: i, data: data, rl: rl, err: err}
			if err != nil {
				cancel()
			}
		}(i, w)
	}
	wg.Wait()
	out := &contributions{w0: ws[0].from, commits: collector.DailyCounts{}, contributions: collector.DailyCounts{}}
	today := collector.DayKey(now)
	firstKey := collector.DayKey(out.w0)
	sumCommits, wantCommits := 0, 0
	incomplete := false
	minRL := rateLimit{Remaining: -1}
	// prefer the error that caused the cancellation over the cancellations it triggered
	var firstErr error
	for _, r := range results {
		if r.err != nil && (firstErr == nil || (errCanceled(firstErr) && !errCanceled(r.err))) {
			firstErr = r.err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	for _, r := range results {
		if !strings.EqualFold(r.data.Viewer.Login, c.cfg.Login) {
			return nil, fmt.Errorf("github: token belongs to %q, expected %q", r.data.Viewer.Login, c.cfg.Login)
		}
		if r.data.User == nil {
			return nil, fmt.Errorf("github: user %q not found", c.cfg.Login)
		}
		if minRL.Remaining < 0 || r.rl.Remaining < minRL.Remaining {
			minRL = r.rl
		}
		cc := r.data.User.ContributionsCollection
		if r.i == 0 {
			out.followers = r.data.User.Followers.TotalCount
			out.years = cc.ContributionYears
		}
		for _, wk := range cc.ContributionCalendar.Weeks {
			for _, d := range wk.ContributionDays {
				if d.Date < firstKey || d.Date > today {
					continue
				}
				out.contributions[d.Date] = float64(d.ContributionCount)
			}
		}
		wantCommits += cc.TotalCommitContributions
		for _, repo := range cc.CommitContributionsByRepository {
			if repo.Contributions.PageInfo.HasNextPage {
				incomplete = true
			}
			for _, n := range repo.Contributions.Nodes {
				t, err := time.Parse(time.RFC3339, n.OccurredAt)
				if err != nil {
					continue
				}
				key := collector.DayKey(t)
				if key < firstKey || key > today {
					continue
				}
				out.commits[key] += float64(n.CommitCount)
				sumCommits += n.CommitCount
			}
		}
		if cc.TotalRepositoriesWithContributedCommits > 100 {
			incomplete = true
		}
		// The restricted numbers settle the private-visibility question: logged, never stored.
		c.cfg.Logger.Debug("github contributions window", "from", ws[r.i].from.Format("2006-01-02"), "calendar_total", cc.ContributionCalendar.TotalContributions, "commit_total", cc.TotalCommitContributions, "repos", cc.TotalRepositoriesWithContributedCommits, "restricted", cc.RestrictedContributionsCount, "has_restricted", cc.HasAnyRestrictedContributions)
	}
	if err := c.checkRateLimit(minRL); err != nil {
		return nil, err
	}
	if incomplete || sumCommits != wantCommits {
		out.notes = append(out.notes, fmt.Sprintf("commit series may be incomplete (summed %d, api total %d)", sumCommits, wantCommits))
	}
	out.notes = append(out.notes, fmt.Sprintf("commits_365d=%d contributions_365d=%d", int(out.commits.Total()), int(out.contributions.Total())))
	return out, nil
}

// writeContributions backfills the two daily counters (storage §S.2.3 rule 4:
// base = the stored grid value before the window, so history older than the
// window is frozen).
func (c *Collector) writeContributions(ctx context.Context, ct *contributions, now time.Time) (int, error) {
	b := collector.NewBatch(c.st)
	baseTs := collector.DayEnd(ct.w0.AddDate(0, 0, -1))
	for _, m := range []struct {
		metric string
		counts collector.DailyCounts
	}{{MetricCommits, ct.commits}, {MetricContributions, ct.contributions}} {
		id, err := c.st.EnsureSeries(ctx, m.metric, nil)
		if err != nil {
			return 0, err
		}
		ex, err := collector.LoadExisting(ctx, c.st, m.metric, baseTs, now.UnixMilli())
		if err != nil {
			return 0, err
		}
		base, haveBase := ex.GridValue(id, baseTs)
		grid, live := collector.CounterSamples(m.counts, ct.w0, now, base, haveBase)
		b.Counter(id, ex, grid, live)
	}
	return b.Commit(ctx)
}

// ---- Q1 followers, Q3 open PRs, Q4 stars ----

func (c *Collector) writeGauges(ctx context.Context, followers int, now time.Time) (int, rateLimit, error) {
	idx, err := collector.LoadLatest(ctx, c.st)
	if err != nil {
		return 0, rateLimit{}, err
	}
	b := collector.NewBatch(c.st)
	if _, err := b.Gauge(ctx, idx, MetricFollowers, nil, float64(followers), now); err != nil {
		return 0, rateLimit{}, err
	}
	var open openPRsData
	rl, err := c.cl.query(ctx, queryOpenPRs, map[string]any{"q": fmt.Sprintf("is:pr is:open author:%s -user:%s", c.cfg.Login, c.cfg.Login)}, &open)
	if err != nil {
		return 0, rl, err
	}
	if err := c.checkRateLimit(rl); err != nil {
		return 0, rl, err
	}
	if _, err := b.Gauge(ctx, idx, MetricOpenPRs, nil, float64(open.Search.IssueCount), now); err != nil {
		return 0, rl, err
	}
	after := ""
	for page := 0; page < 20; page++ {
		vars := map[string]any{"login": c.cfg.Login}
		if after != "" {
			vars["after"] = after
		}
		var repos reposData
		if rl, err = c.cl.query(ctx, queryRepos, vars, &repos); err != nil {
			return 0, rl, err
		}
		if err := c.checkRateLimit(rl); err != nil {
			return 0, rl, err
		}
		if repos.User == nil {
			return 0, rl, fmt.Errorf("github: user %q not found", c.cfg.Login)
		}
		for _, r := range repos.User.Repositories.Nodes {
			if _, err := b.Gauge(ctx, idx, MetricStars, store.Labels{"repo": r.Name}, float64(r.StargazerCount), now); err != nil {
				return 0, rl, err
			}
		}
		if !repos.User.Repositories.PageInfo.HasNextPage {
			break
		}
		after = repos.User.Repositories.PageInfo.EndCursor
	}
	n, err := b.Commit(ctx)
	return n, rl, err
}

// errCanceled reports whether err is a context error (the run timed out).
func errCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
