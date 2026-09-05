// Package config reads the environment into one Config. Flags override env;
// env overrides defaults. The binary never reads .env itself (the shell,
// compose or Vercel exports it).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is every knob the binary understands (vercel-adaptation.md "Env").
type Config struct {
	Addr       string // DIVY_ADDR (PORT wins: ":"+PORT)
	DBURL      string // DIVY_DB_URL; TURSO_DATABASE_URL (+TURSO_AUTH_TOKEN) wins
	ContentDir string // DIVY_CONTENT_DIR
	SiteOrigin string // SITE_ORIGIN
	LogLevel   string // DIVY_LOG_LEVEL: debug|info|warn|error
	LogFormat  string // DIVY_LOG_FORMAT: text|json

	// Collect endpoint and scheduler.
	CollectTokens []string      // DIVY_COLLECT_TOKEN, CRON_SECRET
	CollectBudget time.Duration // DIVY_COLLECT_BUDGET

	// Collectors (real ones land later; the names are fixed here).
	GitHubToken       string // DIVY_GITHUB_TOKEN — the only token variable the app reads
	GitHubLogin       string // DIVY_GITHUB_LOGIN
	GitHubPrivateOrgs []string
	PyPIPackages      []string
	UptimeSelfURL     string // UPTIME_SELF_URL (default SITE_ORIGIN/readyz)
	ProbeTimeout      time.Duration
	IntervalGitHub    time.Duration
	IntervalPyPI      time.Duration
	IntervalUptime    time.Duration
	IntervalManual    time.Duration
	IntervalRetention time.Duration
	CollectDisabled   []string

	// Server extras used by later packages.
	CORSOrigins         []string
	TrustedProxies      []string
	QueryLookback       time.Duration // QUERY_LOOKBACK_DELTA
	QueryTimeout        time.Duration // QUERY_TIMEOUT
	QueryMaxSamples     int           // QUERY_MAX_SAMPLES
	QueryMaxConcurrency int           // QUERY_MAX_CONCURRENCY
	OTelServiceName     string
}

// FromEnv builds the config from getenv (os.Getenv in production).
func FromEnv(getenv func(string) string) (Config, error) {
	get := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Addr:            get("DIVY_ADDR", ":8080"),
		ContentDir:      get("DIVY_CONTENT_DIR", "./content"),
		SiteOrigin:      get("SITE_ORIGIN", ""),
		LogLevel:        get("DIVY_LOG_LEVEL", "info"),
		LogFormat:       get("DIVY_LOG_FORMAT", "text"),
		GitHubToken:     strings.TrimSpace(getenv("DIVY_GITHUB_TOKEN")),
		GitHubLogin:     get("DIVY_GITHUB_LOGIN", "divysinghvi"),
		UptimeSelfURL:   get("UPTIME_SELF_URL", ""),
		OTelServiceName: get("OTEL_SERVICE_NAME", "divy-api"),
	}
	// On Vercel the production URL is a system env var; use it when SITE_ORIGIN is unset.
	if c.SiteOrigin == "" {
		for _, k := range []string{"VERCEL_PROJECT_PRODUCTION_URL", "VERCEL_URL"} {
			if h := strings.TrimSpace(getenv(k)); h != "" {
				c.SiteOrigin = "https://" + strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
				break
			}
		}
	}
	if p := strings.TrimSpace(getenv("PORT")); p != "" {
		c.Addr = ":" + strings.TrimPrefix(p, ":")
	}
	c.DBURL = resolveDBURL(getenv)
	for _, t := range []string{getenv("DIVY_COLLECT_TOKEN"), getenv("CRON_SECRET")} {
		if t = strings.TrimSpace(t); t != "" {
			c.CollectTokens = append(c.CollectTokens, t)
		}
	}
	c.GitHubPrivateOrgs = splitList(get("DIVY_GITHUB_PRIVATE_ORGS", "gradr"))
	c.PyPIPackages = splitList(get("PYPI_PACKAGES", "codemind-ci"))
	c.CollectDisabled = splitList(getenv("COLLECT_DISABLED"))
	c.CORSOrigins = splitList(getenv("CORS_ORIGINS"))
	c.TrustedProxies = splitList(getenv("TRUSTED_PROXIES"))
	var err error
	durs := []struct {
		dst  *time.Duration
		key  string
		def  string
		min  time.Duration
		name string
	}{
		{&c.CollectBudget, "DIVY_COLLECT_BUDGET", "8s", time.Second, "collect budget"},
		{&c.ProbeTimeout, "PROBE_TIMEOUT", "10s", time.Second, "probe timeout"},
		{&c.IntervalGitHub, "COLLECT_GITHUB_INTERVAL", "15m", time.Minute, "github interval"},
		{&c.IntervalPyPI, "COLLECT_PYPI_INTERVAL", "60m", time.Minute, "pypi interval"},
		{&c.IntervalUptime, "COLLECT_UPTIME_INTERVAL", "5m", time.Minute, "uptime interval"},
		{&c.IntervalManual, "COLLECT_MANUAL_INTERVAL", "15m", time.Minute, "manual interval"},
		{&c.IntervalRetention, "COLLECT_RETENTION_INTERVAL", "60m", time.Minute, "retention interval"},
		{&c.QueryLookback, "QUERY_LOOKBACK_DELTA", "26h", time.Second, "query lookback delta"},
		{&c.QueryTimeout, "QUERY_TIMEOUT", "30s", time.Second, "query timeout"},
	}
	for _, d := range durs {
		if *d.dst, err = ParseDuration(get(d.key, d.def)); err != nil {
			return c, fmt.Errorf("%s: %w", d.key, err)
		}
		if *d.dst < d.min {
			return c, fmt.Errorf("%s: %s must be at least %s", d.key, d.name, d.min)
		}
	}
	ints := []struct {
		dst  *int
		key  string
		def  int
		name string
	}{
		{&c.QueryMaxSamples, "QUERY_MAX_SAMPLES", 2000000, "query max samples"},
		{&c.QueryMaxConcurrency, "QUERY_MAX_CONCURRENCY", 20, "query max concurrency"},
	}
	for _, d := range ints {
		*d.dst = d.def
		if v := strings.TrimSpace(getenv(d.key)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return c, fmt.Errorf("%s: %s must be a positive integer", d.key, d.name)
			}
			*d.dst = n
		}
	}
	if c.UptimeSelfURL == "" && c.SiteOrigin != "" {
		c.UptimeSelfURL = strings.TrimRight(c.SiteOrigin, "/") + "/readyz"
	}
	return c, nil
}

func resolveDBURL(getenv func(string) string) string {
	if t := strings.TrimSpace(getenv("TURSO_DATABASE_URL")); t != "" {
		if tok := strings.TrimSpace(getenv("TURSO_AUTH_TOKEN")); tok != "" {
			sep := "?"
			if strings.Contains(t, "?") {
				sep = "&"
			}
			return t + sep + "authToken=" + tok
		}
		return t
	}
	if u := strings.TrimSpace(getenv("DIVY_DB_URL")); u != "" {
		return u
	}
	// Vercel functions have a read-only bundle and a writable /tmp; without a
	// database URL the store is ephemeral there (readyz/logs say so).
	if strings.TrimSpace(getenv("VERCEL")) != "" {
		return "file:/tmp/divy.db"
	}
	return "file:./data/divy.db"
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseDuration accepts Go durations (8s, 1h30m) and Prometheus-style d/w/y units.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	unit := s[len(s)-1]
	mult := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour, 'y': 365 * 24 * time.Hour}[unit]
	if mult == 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return time.Duration(n) * mult, nil
}

// Getenv is os.Getenv; a variable for tests.
var Getenv = os.Getenv
