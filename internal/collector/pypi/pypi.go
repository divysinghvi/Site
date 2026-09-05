// Package pypi collects PyPI download counts (pypistats.org, mirrors
// excluded, 180-day daily backfill) and the current published version
// (pypi.org JSON API) of every package the content links to.
package pypi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/store"
)

// Metric names written by this collector.
const (
	MetricDownloads = "pypi_downloads_total"
	MetricInfo      = "pypi_package_info"
)

// Defaults.
const (
	DefaultStatsBase = "https://pypistats.org"
	DefaultPyPIBase  = "https://pypi.org"
)

// Config configures the collector.
type Config struct {
	// Packages to collect (union of content links and PYPI_PACKAGES).
	Packages []string
	// Interval is the scheduler cadence (COLLECT_PYPI_INTERVAL).
	Interval time.Duration
	// StatsBase and PyPIBase override the API hosts (tests).
	StatsBase string
	PyPIBase  string
	UserAgent string
	// HTTPClient overrides the outbound client (tests).
	HTTPClient *http.Client
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
}

// Collector implements collector.Collector.
type Collector struct {
	cfg Config
	st  *store.Store
}

// New builds the collector.
func New(cfg Config, st *store.Store) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Minute
	}
	if cfg.StatsBase == "" {
		cfg.StatsBase = DefaultStatsBase
	}
	if cfg.PyPIBase == "" {
		cfg.PyPIBase = DefaultPyPIBase
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = collector.UserAgent("")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = collector.NewHTTPClient(30 * time.Second)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	cfg.StatsBase = strings.TrimRight(cfg.StatsBase, "/")
	cfg.PyPIBase = strings.TrimRight(cfg.PyPIBase, "/")
	return &Collector{cfg: cfg, st: st}
}

// PackagesFromContent returns the package names of every span link of kind
// pypi (https://pypi.org/project/<name>/), deduplicated and sorted.
func PackagesFromContent(c *content.Content) []string {
	seen := map[string]bool{}
	for _, n := range c.Nodes() {
		for _, l := range n.Span.Links {
			if l.Kind != "pypi" {
				continue
			}
			if name := packageFromURL(l.URL); name != "" {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func packageFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, "pypi.org") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "project" {
		return strings.ToLower(parts[1])
	}
	return ""
}

// MergePackages unions two package lists (lower-cased, sorted).
func MergePackages(lists ...[]string) []string {
	seen := map[string]bool{}
	for _, l := range lists {
		for _, p := range l {
			if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
				seen[p] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Name is "pypi".
func (c *Collector) Name() string { return "pypi" }

// Interval is the configured cadence.
func (c *Collector) Interval() time.Duration { return c.cfg.Interval }

// Disabled is true when no package is configured.
func (c *Collector) Disabled() bool { return len(c.cfg.Packages) == 0 }

// Run collects every package. The two sources are independent: a pypistats
// failure (rate limit, blocked network) does not stop the version lookup and
// vice versa; the run is reported as failed when any source failed, with
// every error listed.
func (c *Collector) Run(ctx context.Context) (collector.Result, error) {
	if c.Disabled() {
		return collector.Result{}, fmt.Errorf("%w: no PyPI package configured (PYPI_PACKAGES or a pypi link in content/spans.yaml)", collector.ErrDisabled)
	}
	now := c.cfg.Now().UTC()
	res := collector.Result{}
	var errs []string
	var notes []string
	for _, pkg := range c.cfg.Packages {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		n, note, err := c.downloads(ctx, pkg, now)
		res.Items += n
		if err != nil {
			errs = append(errs, pkg+" downloads: "+err.Error())
		} else {
			notes = append(notes, note)
		}
		n, note, err = c.info(ctx, pkg, now)
		res.Items += n
		if err != nil {
			errs = append(errs, pkg+" info: "+err.Error())
		} else {
			notes = append(notes, note)
		}
	}
	res.Note = strings.Join(notes, "; ")
	if len(errs) > 0 {
		return res, errors.New(strings.Join(errs, "; "))
	}
	return res, nil
}

func (c *Collector) get(ctx context.Context, u string, hdr map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp, nil, err
	}
	return resp, body, nil
}

// ---- pypistats: daily downloads ----

type overallResponse struct {
	Data []struct {
		Category  string `json:"category"`
		Date      string `json:"date"`
		Downloads *int64 `json:"downloads"`
	} `json:"data"`
	Package string `json:"package"`
	Type    string `json:"type"`
}

func (c *Collector) downloads(ctx context.Context, pkg string, now time.Time) (int, string, error) {
	u := c.cfg.StatsBase + "/api/packages/" + url.PathEscape(pkg) + "/overall?mirrors=false"
	resp, body, err := c.get(ctx, u, nil)
	if err != nil {
		return 0, "", fmt.Errorf("pypistats: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return 0, "", fmt.Errorf("pypistats: package %q unknown (404)", pkg)
	case http.StatusTooManyRequests:
		return 0, "", errors.New("pypistats: rate limited (429)")
	default:
		return 0, "", fmt.Errorf("pypistats: HTTP %d", resp.StatusCode)
	}
	var out overallResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, "", fmt.Errorf("pypistats: decode: %w", err)
	}
	if out.Data == nil {
		return 0, "", fmt.Errorf("pypistats: no data for %q", pkg)
	}
	counts := collector.DailyCounts{}
	for _, d := range out.Data {
		if d.Downloads == nil || d.Category != "without_mirrors" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.Date); err != nil {
			continue
		}
		counts[d.Date] += float64(*d.Downloads)
	}
	w0, ok := counts.First()
	if !ok {
		return 0, fmt.Sprintf("%s: no download rows", pkg), nil
	}
	labels := store.Labels{"package": pkg}
	id, err := c.st.EnsureSeries(ctx, MetricDownloads, labels)
	if err != nil {
		return 0, "", err
	}
	baseTs := collector.DayEnd(w0.AddDate(0, 0, -1))
	ex, err := collector.LoadExisting(ctx, c.st, MetricDownloads, baseTs, now.UnixMilli())
	if err != nil {
		return 0, "", err
	}
	base, haveBase := ex.GridValue(id, baseTs)
	grid, live := collector.CounterSamples(counts, w0, now, base, haveBase)
	b := collector.NewBatch(c.st)
	b.Counter(id, ex, grid, live)
	n, err := b.Commit(ctx)
	if err != nil {
		return 0, "", err
	}
	return n, fmt.Sprintf("%s: %d downloads over %d days (first %s)", pkg, int64(counts.Total()), len(counts), collector.DayKey(w0)), nil
}

// ---- pypi.org: current version ----

type infoResponse struct {
	Info struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"info"`
}

func stateKey(kind, pkg string) string { return "pypi." + kind + "." + pkg }

func (c *Collector) info(ctx context.Context, pkg string, now time.Time) (int, string, error) {
	u := c.cfg.PyPIBase + "/pypi/" + url.PathEscape(pkg) + "/json"
	hdr := map[string]string{}
	etag, _, _ := c.st.GetState(ctx, stateKey("etag", pkg))
	cached, haveCached, _ := c.st.GetState(ctx, stateKey("version", pkg))
	if etag != "" && haveCached {
		hdr["If-None-Match"] = etag
	}
	resp, body, err := c.get(ctx, u, hdr)
	if err != nil {
		return 0, "", fmt.Errorf("pypi.org: %w", err)
	}
	var version string
	switch resp.StatusCode {
	case http.StatusNotModified:
		version = cached
	case http.StatusOK:
		var out infoResponse
		if err := json.Unmarshal(body, &out); err != nil {
			return 0, "", fmt.Errorf("pypi.org: decode: %w", err)
		}
		version = out.Info.Version
		if version == "" {
			return 0, "", errors.New("pypi.org: info.version missing")
		}
		if et := resp.Header.Get("ETag"); et != "" {
			_ = c.st.SetState(ctx, stateKey("etag", pkg), et)
		}
		_ = c.st.SetState(ctx, stateKey("version", pkg), version)
	case http.StatusNotFound:
		return 0, "", fmt.Errorf("pypi.org: package %q unknown (404)", pkg)
	default:
		return 0, "", fmt.Errorf("pypi.org: HTTP %d", resp.StatusCode)
	}
	// exactly one pypi_package_info series per package: drop the other versions
	if _, err := c.st.DeleteSeriesWhere(ctx, MetricInfo, func(l store.Labels) bool {
		return l["package"] == pkg && l["version"] != version
	}); err != nil {
		return 0, "", err
	}
	idx, err := collector.LoadLatest(ctx, c.st)
	if err != nil {
		return 0, "", err
	}
	b := collector.NewBatch(c.st)
	if _, err := b.Gauge(ctx, idx, MetricInfo, store.Labels{"package": pkg, "version": version}, 1, now); err != nil {
		return 0, "", err
	}
	n, err := b.Commit(ctx)
	if err != nil {
		return 0, "", err
	}
	return n, fmt.Sprintf("%s: version %s", pkg, version), nil
}
