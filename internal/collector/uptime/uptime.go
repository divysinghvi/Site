// Package uptime probes the targets of content/uptime.yaml and records one
// probe_results row plus the three blackbox_exporter-named gauges per probe.
// Targets whose url is TODO(divy) are never probed and never green.
package uptime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/store"
)

// Metric names written by this collector (blackbox_exporter names).
const (
	MetricSuccess  = "probe_success"
	MetricDuration = "probe_duration_seconds"
	MetricStatus   = "probe_http_status_code"
)

// Error classes recorded in probe_results.error as "<class>: <message>".
const (
	ClassDNS      = "dns"
	ClassTLS      = "tls"
	ClassTimeout  = "timeout"
	ClassConn     = "conn"
	ClassHTTP     = "http"
	ClassRedirect = "redirect"
	ClassRead     = "read"
	ClassOther    = "other"
)

// MaxRedirects is the redirect hop limit when follow_redirects is on.
const MaxRedirects = 5

// Target is one probe target with defaults applied.
type Target struct {
	ID   string
	Name string
	URL  string
	// Configured is false when the URL is a TODO(divy) marker.
	Configured bool
	Method     string
	// Expected lists the accepted final status codes; empty = any 2xx or 3xx.
	Expected        []int
	Timeout         time.Duration
	Interval        time.Duration
	FollowRedirects bool
}

// TargetsFromContent converts the loaded uptime.yaml (self-api url already
// replaced by UPTIME_SELF_URL, $SITE_ORIGIN expanded) into probe targets;
// timeouts are capped at maxTimeout (PROBE_TIMEOUT).
func TargetsFromContent(c *content.Content, maxTimeout time.Duration) []Target {
	var out []Target
	for _, t := range c.Uptime.Targets {
		v := content.TargetView(t)
		timeout, err := content.ParsePromDuration(v.Timeout)
		if err != nil || timeout <= 0 {
			timeout = 10 * time.Second
		}
		if maxTimeout > 0 && timeout > maxTimeout {
			timeout = maxTimeout
		}
		interval, err := content.ParsePromDuration(v.Interval)
		if err != nil || interval <= 0 {
			interval = 5 * time.Minute
		}
		out = append(out, Target{
			ID:              t.ID,
			Name:            t.Name,
			URL:             v.URL,
			Configured:      v.Configured,
			Method:          string(v.Method),
			Expected:        append([]int(nil), t.ExpectedStatus...), // raw: empty means the 2xx/3xx rule
			Timeout:         timeout,
			Interval:        interval,
			FollowRedirects: v.FollowRedirects,
		})
	}
	return out
}

// Config configures the collector.
type Config struct {
	Targets []Target
	// Interval is the scheduler tick (COLLECT_UPTIME_INTERVAL); targets keep their own interval.
	Interval time.Duration
	// UserAgent is "divy-uptime/1.0 (+<site origin>)".
	UserAgent string
	// Concurrency bounds simultaneous probes (default 5).
	Concurrency int
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
	// Transport overrides the probe transport (tests); nil = a fresh transport per probe.
	Transport http.RoundTripper
}

// UserAgent builds the probe identity from the site origin.
func UserAgent(siteOrigin string) string {
	return "divy-uptime/1.0 (+" + strings.TrimRight(siteOrigin, "/") + ")"
}

// Collector implements collector.Collector.
type Collector struct {
	cfg Config
	st  *store.Store
}

// New builds the collector.
func New(cfg Config, st *store.Store) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = UserAgent("")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Collector{cfg: cfg, st: st}
}

// Name is "uptime".
func (c *Collector) Name() string { return "uptime" }

// Interval is the scheduler tick.
func (c *Collector) Interval() time.Duration { return c.cfg.Interval }

// Targets returns the configured targets (the uptime endpoint lists them).
func (c *Collector) Targets() []Target { return c.cfg.Targets }

// Run probes every configured target whose own interval has elapsed and
// writes the results in one transaction. A probe cut short by the run
// budget (not by its own timeout) records nothing: a gap is honest, a red
// bar caused by our own deadline is not.
func (c *Collector) Run(ctx context.Context) (collector.Result, error) {
	now := c.cfg.Now().UTC()
	var due []Target
	unconfigured := 0
	for _, t := range c.cfg.Targets {
		if !t.Configured {
			unconfigured++
			continue
		}
		last, ok, err := c.st.LastProbe(ctx, t.ID)
		if err != nil {
			return collector.Result{}, err
		}
		if ok && now.Sub(time.UnixMilli(last.TsMs)) < t.Interval-10*time.Second {
			continue
		}
		due = append(due, t)
	}
	results := make([]*store.Probe, len(due))
	sem := make(chan struct{}, c.cfg.Concurrency)
	var wg sync.WaitGroup
	for i, t := range due {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = c.probe(ctx, t, now)
		}(i, t)
	}
	wg.Wait()
	var probes []store.Probe
	b := collector.NewBatch(c.st)
	skipped := 0
	for _, p := range results {
		if p == nil {
			skipped++
			continue
		}
		probes = append(probes, *p)
		labels := store.Labels{"target": p.Target}
		up, dur := 0.0, 0.0
		if p.Up {
			up = 1
		}
		if p.LatencyMs != nil {
			dur = *p.LatencyMs / 1000
		}
		for _, m := range []struct {
			metric string
			value  float64
		}{{MetricSuccess, up}, {MetricDuration, dur}, {MetricStatus, float64(p.StatusCode)}} {
			id, err := c.st.EnsureSeries(ctx, m.metric, labels)
			if err != nil {
				return collector.Result{}, err
			}
			b.Upsert(id, store.Sample{TsMs: p.TsMs, Value: m.value})
		}
	}
	wctx := ctx
	if ctx.Err() != nil {
		// the budget expired during the probes: still persist what finished
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	if err := c.st.WriteProbeResults(wctx, probes); err != nil {
		return collector.Result{}, err
	}
	n, err := b.Commit(wctx)
	if err != nil {
		return collector.Result{}, err
	}
	res := collector.Result{Items: n + len(probes)}
	var parts []string
	upCount := 0
	for _, p := range probes {
		if p.Up {
			upCount++
		}
	}
	parts = append(parts, fmt.Sprintf("probed=%d up=%d", len(probes), upCount))
	if len(due) < len(c.cfg.Targets)-unconfigured {
		parts = append(parts, fmt.Sprintf("not_due=%d", len(c.cfg.Targets)-unconfigured-len(due)))
	}
	if unconfigured > 0 {
		parts = append(parts, fmt.Sprintf("unconfigured=%d", unconfigured))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("cut_by_budget=%d", skipped))
	}
	res.Note = strings.Join(parts, " ")
	if ctx.Err() != nil && skipped > 0 {
		return res, fmt.Errorf("%d probe(s) cut short by the run budget: %w", skipped, ctx.Err())
	}
	return res, nil
}

// errTooManyRedirects is returned by CheckRedirect past MaxRedirects hops.
var errTooManyRedirects = errors.New("stopped after 5 redirects")

func (c *Collector) client(t Target) *http.Client {
	transport := c.cfg.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: t.Timeout,
			ForceAttemptHTTP2:   true,
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   t.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !t.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= MaxRedirects {
				return errTooManyRedirects
			}
			return nil
		},
	}
}

// probe performs one HTTP probe. It returns nil when the parent context
// ended before the probe's own timeout (cut short by the run budget).
func (c *Collector) probe(parent context.Context, t Target, now time.Time) *store.Probe {
	ctx, cancel := context.WithTimeout(parent, t.Timeout)
	defer cancel()
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	p := &store.Probe{Target: t.ID, TsMs: now.UnixMilli()}
	req, err := http.NewRequestWithContext(ctx, method, t.URL, nil)
	if err != nil {
		msg := ClassOther + ": " + sanitize(err.Error(), t.URL)
		p.Error = &msg
		return p
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "*/*")
	start := time.Now()
	resp, err := c.client(t).Do(req)
	if err != nil {
		if parent.Err() != nil {
			return nil // the run budget, not the probe's own timeout, ended the request
		}
		lat := float64(time.Since(start).Microseconds()) / 1000
		p.LatencyMs = &lat
		msg := Classify(err) + ": " + sanitize(err.Error(), t.URL)
		p.Error = &msg
		return p
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	lat := float64(time.Since(start).Microseconds()) / 1000
	p.LatencyMs = &lat
	p.StatusCode = resp.StatusCode
	if readErr != nil {
		if parent.Err() != nil {
			return nil
		}
		msg := ClassRead + ": " + sanitize(readErr.Error(), t.URL)
		p.Error = &msg
		return p
	}
	if !accepted(resp.StatusCode, t.Expected) {
		want := "2xx or 3xx"
		if len(t.Expected) > 0 {
			want = fmt.Sprint(t.Expected)
		}
		msg := fmt.Sprintf("%s: got %d, want %s", ClassHTTP, resp.StatusCode, want)
		p.Error = &msg
		return p
	}
	p.Up = true
	return p
}

// accepted applies the up rule: the explicit expected_status list, else any 2xx/3xx.
func accepted(code int, expected []int) bool {
	if len(expected) == 0 {
		return code >= 200 && code < 400
	}
	for _, e := range expected {
		if e == code {
			return true
		}
	}
	return false
}

// Classify maps a probe error to its class.
func Classify(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ClassDNS
	}
	if errors.Is(err, errTooManyRedirects) {
		return ClassRedirect
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return ClassTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ClassTimeout
	}
	var certErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	var alertErr tls.AlertError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &certErr) || errors.As(err, &recErr) || errors.As(err, &alertErr) || errors.As(err, &unknownAuth) || errors.As(err, &hostErr) || errors.As(err, &certInvalid) {
		return ClassTLS
	}
	if s := err.Error(); strings.Contains(s, "tls:") || strings.Contains(s, "x509:") || strings.Contains(s, "certificate") {
		return ClassTLS
	}
	for _, errno := range []syscall.Errno{syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.EPIPE} {
		if errors.Is(err, errno) {
			return ClassConn
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			return ClassConn
		}
		return ClassRead
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return ClassRead
	}
	return ClassOther
}

// sanitize strips the target URL's query string and userinfo from a message
// and bounds it to 200 characters.
func sanitize(msg, target string) string {
	if u, err := url.Parse(target); err == nil {
		if u.User != nil || u.RawQuery != "" {
			clean := *u
			clean.User = nil
			clean.RawQuery = ""
			msg = strings.ReplaceAll(msg, target, clean.String())
		}
	}
	// url.Error reads `<Method> "<URL>": <cause>`; keep only the cause
	for _, prefix := range []string{"Get \"", "Head \"", "Post \""} {
		if strings.HasPrefix(msg, prefix) {
			if j := strings.LastIndex(msg, "\": "); j >= 0 {
				msg = msg[j+3:]
			}
			break
		}
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// SortedIDs returns the target ids in file order (helper for handlers/tests).
func SortedIDs(ts []Target) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.ID)
	}
	sort.Strings(out)
	return out
}
