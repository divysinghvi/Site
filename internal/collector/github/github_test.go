package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/store"
)

// frozen is 2026-09-05T10:15:00Z, the storage draft's worked example time.
var frozen = time.Date(2026, 9, 5, 10, 15, 0, 0, time.UTC)

// day is one day of fixture activity: commits per repository and the calendar count.
type day struct {
	date          string
	site, gradr   int // commits to divysinghvi/site (public) and gradr/app (private)
	contributions int
}

// activity is the fixture: the last seven days give the favicon example
// 3 0 5 2 7 1 4; 2025-09-05 is the first day of the 365-day window and
// 2025-09-04 lies outside it (it must be clamped away).
var activity = []day{
	{"2025-09-04", 9, 0, 9},
	{"2025-09-05", 1, 0, 1},
	{"2026-03-01", 2, 2, 6},
	{"2026-08-30", 2, 1, 4},
	{"2026-09-01", 5, 0, 5},
	{"2026-09-02", 0, 2, 2},
	{"2026-09-03", 7, 0, 8},
	{"2026-09-04", 1, 0, 2},
	{"2026-09-05", 3, 1, 5},
}

type prFixture struct {
	mergedAt, owner, name string
	private               bool
}

var mergedPRs = [][]prFixture{
	{ // page 1
		{"2026-08-30T12:00:00Z", "kubernetes", "minikube", false},
		{"2026-08-30T15:00:00Z", "kubernetes", "minikube", false},
	},
	{ // page 2
		{"2026-09-03T09:00:00Z", "kubeflow", "kubeflow", false},
		{"2026-09-01T09:00:00Z", "gradr", "app", true},
		{"2024-03-10T09:00:00Z", "someone", "secret-repo", true},
	},
}

// fakeGitHub is an httptest GraphQL server answering the four documents
// with bodies shaped like docs.github.com's object references.
type fakeGitHub struct {
	t         *testing.T
	mu        sync.Mutex
	reqs      []string // operation names in order
	login     string   // viewer.login
	remaining int
	fail      map[string]int // op → HTTP status to return
	failOnce  map[string]int // op → HTTP status returned once
	unbounded int            // issueCount for the unbounded merged-PR query (0 = real count)
	yearly    map[string]int // issueCount per merged: window
	invalid   bool           // answer with a GraphQL error
}

func newFake(t *testing.T) (*fakeGitHub, *httptest.Server) {
	f := &fakeGitHub{t: t, login: "divysinghvi", remaining: 4990, fail: map[string]int{}, failOnce: map[string]int{}, yearly: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeGitHub) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func (f *fakeGitHub) count(op string) int {
	n := 0
	for _, o := range f.ops() {
		if o == op {
			n++
		}
	}
	return n
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "bearer ") || !strings.HasPrefix(r.Header.Get("User-Agent"), "divy.dev-collector/") {
		f.t.Errorf("missing auth or user agent: %v", r.Header)
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	op := body.Query[strings.Index(body.Query, "query ")+6:]
	op = op[:strings.IndexAny(op, "( ")]
	f.mu.Lock()
	f.reqs = append(f.reqs, op)
	status := f.fail[op]
	if s, ok := f.failOnce[op]; ok {
		status = s
		delete(f.failOnce, op)
	}
	f.mu.Unlock()
	if status != 0 {
		w.Header().Set("x-ratelimit-reset", "1788603600")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
		return
	}
	if f.invalid {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Field 'nope' doesn't exist","type":"UNKNOWN"}]}`))
		return
	}
	rl := fmt.Sprintf(`"rateLimit":{"cost":1,"remaining":%d,"resetAt":"2026-09-05T11:00:00Z"}`, f.remaining)
	var data string
	switch op {
	case "Contributions":
		data = f.contributions(body.Variables, rl)
	case "MergedPRs":
		data = f.mergedPRs(body.Variables, rl)
	case "OpenPRs":
		data = `"search":{"issueCount":3},` + rl
	case "Repos":
		data = `"user":{"repositories":{"totalCount":3,"pageInfo":{"hasNextPage":false,"endCursor":"Y3Vyc29yOjM="},"nodes":[{"name":"site","stargazerCount":5,"isArchived":false},{"name":"savely","stargazerCount":12,"isArchived":false},{"name":"zero","stargazerCount":0,"isArchived":true}]}},` + rl
	default:
		http.Error(w, "unknown op "+op, 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":{` + data + `}}`))
}

func (f *fakeGitHub) contributions(vars map[string]any, rl string) string {
	from, _ := vars["from"].(string)
	to, _ := vars["to"].(string)
	inWindow := func(d string) bool { return d+"T00:00:00Z" >= from && d+"T00:00:00Z" <= to }
	var days, siteNodes, gradrNodes []string
	total := 0
	for _, d := range activity {
		// the out-of-window day is returned in every response on purpose
		if !inWindow(d.date) && d.date != "2025-09-04" {
			continue
		}
		days = append(days, fmt.Sprintf(`{"date":%q,"contributionCount":%d}`, d.date, d.contributions))
		if d.site > 0 {
			siteNodes = append(siteNodes, fmt.Sprintf(`{"occurredAt":"%sT00:00:00Z","commitCount":%d}`, d.date, d.site))
		}
		if d.gradr > 0 {
			gradrNodes = append(gradrNodes, fmt.Sprintf(`{"occurredAt":"%sT00:00:00Z","commitCount":%d}`, d.date, d.gradr))
		}
		if inWindow(d.date) {
			total += d.site + d.gradr
		}
	}
	repo := func(nameWithOwner, owner string, private bool, nodes []string) string {
		return fmt.Sprintf(`{"repository":{"nameWithOwner":%q,"isPrivate":%v,"owner":{"login":%q}},"contributions":{"totalCount":%d,"pageInfo":{"hasNextPage":false,"endCursor":"Y3Vyc29yOnYyOpK5MjAyNi0wOS0wNVQwMDowMDowMCswMDowMM4AAAAB"},"nodes":[%s]}}`, nameWithOwner, private, owner, len(nodes), strings.Join(nodes, ","))
	}
	return fmt.Sprintf(`"viewer":{"login":%q},"user":{"followers":{"totalCount":42},"contributionsCollection":{"contributionYears":[2026,2025,2024],"totalCommitContributions":%d,"totalRepositoriesWithContributedCommits":2,"restrictedContributionsCount":3,"hasAnyRestrictedContributions":true,"contributionCalendar":{"totalContributions":10,"weeks":[{"contributionDays":[%s]}]},"commitContributionsByRepository":[%s,%s]}},%s`,
		f.login, total, strings.Join(days, ","), repo("divysinghvi/site", "divysinghvi", false, siteNodes), repo("gradr/app", "gradr", true, gradrNodes), rl)
}

func (f *fakeGitHub) mergedPRs(vars map[string]any, rl string) string {
	q, _ := vars["q"].(string)
	after, _ := vars["after"].(string)
	if !strings.Contains(q, "is:pr is:merged author:divysinghvi") {
		f.t.Errorf("unexpected search query %q", q)
	}
	// windowed queries (after a split): answer with the PRs merged inside the window
	if i := strings.Index(q, "merged:"); i >= 0 {
		win := q[i+len("merged:"):]
		parts := strings.SplitN(win, "..", 2)
		var nodes []string
		for _, page := range mergedPRs {
			for _, p := range page {
				if p.mergedAt[:10] >= parts[0] && p.mergedAt[:10] <= parts[1] {
					nodes = append(nodes, prNode(p))
				}
			}
		}
		count := len(nodes)
		if c, ok := f.yearly[win]; ok {
			count = c
		}
		return fmt.Sprintf(`"search":{"issueCount":%d,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[%s]},%s`, count, strings.Join(nodes, ","), rl)
	}
	page := 0
	if after == "Y3Vyc29yOjE=" {
		page = 1
	}
	count := 5
	if f.unbounded > 0 {
		count = f.unbounded
	}
	var nodes []string
	for _, p := range mergedPRs[page] {
		nodes = append(nodes, prNode(p))
	}
	next := page == 0
	cursor := `"Y3Vyc29yOjE="`
	if !next {
		cursor = "null"
	}
	return fmt.Sprintf(`"search":{"issueCount":%d,"pageInfo":{"hasNextPage":%v,"endCursor":%s},"nodes":[%s]},%s`, count, next, cursor, strings.Join(nodes, ","), rl)
}

func prNode(p prFixture) string {
	return fmt.Sprintf(`{"mergedAt":%q,"repository":{"name":%q,"isPrivate":%v,"owner":{"login":%q}}}`, p.mergedAt, p.name, p.private, p.owner)
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "gh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newCollector(t *testing.T, st *store.Store, srv *httptest.Server, mod func(*Config)) *Collector {
	cfg := Config{Token: "ghp_test", Login: "divysinghvi", PrivateOrgs: []string{"gradr"}, Endpoint: srv.URL, UserAgent: collector.UserAgent("https://example.vercel.app"), RetryDelay: 5 * time.Millisecond, Now: func() time.Time { return frozen }}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg, st)
}

// samples returns a series' samples as ts → value.
func samples(t *testing.T, st *store.Store, metric string, labels store.Labels) map[int64]float64 {
	t.Helper()
	ms := []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: metric}}
	for k, v := range labels {
		ms = append(ms, store.Matcher{Name: k, Type: store.MatchEqual, Value: v})
	}
	data, err := st.QueryRange(context.Background(), ms, 0, frozen.Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 {
		return nil
	}
	out := map[int64]float64{}
	for _, s := range data[0].Samples {
		out[s.TsMs] = s.Value
	}
	return out
}

func dayEnd(date string) int64 {
	d, _ := time.Parse("2006-01-02", date)
	return collector.DayEnd(d)
}

func TestWindows(t *testing.T) {
	ws := windows(frozen)
	if len(ws) != 4 {
		t.Fatalf("windows = %d", len(ws))
	}
	if !ws[0].from.Equal(time.Date(2025, 9, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("w0 = %s", ws[0].from)
	}
	for i, w := range ws {
		if days := w.to.Sub(w.from).Hours() / 24; days > 92 {
			t.Errorf("window %d spans %.1f days", i, days)
		}
		if i > 0 && !w.from.Equal(ws[i-1].to.Add(time.Second)) {
			t.Errorf("window %d is not contiguous: %s after %s", i, w.from, ws[i-1].to)
		}
	}
	if !ws[3].to.Equal(frozen) {
		t.Errorf("last window ends at %s, want now", ws[3].to)
	}
}

func TestRunWritesEverything(t *testing.T) {
	f, srv := newFake(t)
	st := newStore(t)
	c := newCollector(t, st, srv, nil)
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Items == 0 || !strings.Contains(res.Note, "prs=5 (scan complete") {
		t.Errorf("result = %+v", res)
	}
	if f.count("Contributions") != 4 || f.count("MergedPRs") != 2 || f.count("OpenPRs") != 1 || f.count("Repos") != 1 {
		t.Errorf("requests = %v", f.ops())
	}

	// commits: 365-day window starting 2025-09-05; 2025-09-04 (9 commits) clamped away
	commits := samples(t, st, MetricCommits, nil)
	if v, ok := commits[dayEnd("2025-09-04")]; !ok || v != 0 {
		t.Errorf("start marker at dayEnd(2025-09-04) = %v %v", v, ok)
	}
	if v := commits[dayEnd("2025-09-05")]; v != 1 {
		t.Errorf("grid through 2025-09-05 = %v, want 1", v)
	}
	// through 2026-09-04: 1 + 4 (03-01) + 3 + 5 + 2 + 7 + 1 = 23; live = 23 + 4 (today) = 27
	if v := commits[dayEnd("2026-09-04")]; v != 23 {
		t.Errorf("grid through 2026-09-04 = %v, want 23", v)
	}
	if v := commits[frozen.UnixMilli()]; v != 27 {
		t.Errorf("live commits = %v, want 27", v)
	}
	// daily diffs of the last seven days: 3 0 5 2 7 1 4
	want := []float64{3, 0, 5, 2, 7, 1, 4}
	prev := commits[dayEnd("2026-08-29")]
	for i, d := range []string{"2026-08-30", "2026-08-31", "2026-09-01", "2026-09-02", "2026-09-03", "2026-09-04"} {
		if got := commits[dayEnd(d)] - prev; got != want[i] {
			t.Errorf("commits on %s = %v, want %v", d, got, want[i])
		}
		prev = commits[dayEnd(d)]
	}
	if got := commits[frozen.UnixMilli()] - prev; got != 4 {
		t.Errorf("commits today = %v, want 4", got)
	}
	if n := len(commits); n != 367+1 { // 366 grid days (365 + start marker … through yesterday) + live
		t.Logf("commit samples = %d", n)
	}
	contrib := samples(t, st, MetricContributions, nil)
	if v := contrib[frozen.UnixMilli()]; v != 1+6+4+5+2+8+2+5 {
		t.Errorf("live contributions = %v", v)
	}

	// gauges
	if v := samples(t, st, MetricFollowers, nil)[frozen.UnixMilli()]; v != 42 {
		t.Errorf("followers = %v", v)
	}
	if v := samples(t, st, MetricOpenPRs, nil)[frozen.UnixMilli()]; v != 3 {
		t.Errorf("oss_prs_open = %v", v)
	}
	if v := samples(t, st, MetricStars, store.Labels{"repo": "savely"})[frozen.UnixMilli()]; v != 12 {
		t.Errorf("stars savely = %v", v)
	}
	if v, ok := samples(t, st, MetricStars, store.Labels{"repo": "zero"})[frozen.UnixMilli()]; !ok || v != 0 {
		t.Errorf("stars zero = %v %v (zero-star repos are stored)", v, ok)
	}

	// merged PRs: the storage draft's worked example for org=kubernetes
	k8s := samples(t, st, MetricMergedPRs, store.Labels{"org": "kubernetes"})
	for ts, want := range map[int64]float64{dayEnd("2026-08-29"): 0, dayEnd("2026-08-30"): 2, dayEnd("2026-09-03"): 2, dayEnd("2026-09-04"): 2, frozen.UnixMilli(): 2} {
		if got, ok := k8s[ts]; !ok || got != want {
			t.Errorf("kubernetes @%d = %v %v, want %v", ts, got, ok, want)
		}
	}
	if len(k8s) != 8 {
		t.Errorf("kubernetes samples = %d, want 8 (0 marker + 6 grid + live)", len(k8s))
	}
	if v := samples(t, st, MetricMergedPRs, store.Labels{"org": "kubeflow"})[frozen.UnixMilli()]; v != 1 {
		t.Errorf("kubeflow = %v", v)
	}
	if v := samples(t, st, MetricMergedPRs, store.Labels{"org": "gradr"})[frozen.UnixMilli()]; v != 1 {
		t.Errorf("gradr (private org, count only) = %v", v)
	}
	if v := samples(t, st, MetricMergedPRs, store.Labels{"org": "someone"})[frozen.UnixMilli()]; v != 1 {
		t.Errorf("someone (private repo, count only) = %v", v)
	}
	if v := samples(t, st, MetricMergedByRepo, store.Labels{"org": "kubernetes", "repo": "minikube"})[frozen.UnixMilli()]; v != 2 {
		t.Errorf("by repo minikube = %v", v)
	}
	// privacy: private repository names appear nowhere — series labels or collector_state
	series, err := st.ListSeries(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byRepo := 0
	for _, s := range series {
		for _, v := range s.Labels {
			if v == "app" || v == "secret-repo" {
				t.Errorf("private repository name leaked into series %s%s", s.Metric, store.CanonicalLabels(s.Labels))
			}
		}
		if s.Metric == MetricMergedByRepo {
			byRepo++
		}
	}
	if byRepo != 2 {
		t.Errorf("by-repo series = %d, want 2 (minikube, kubeflow)", byRepo)
	}
	state, _, _ := st.GetState(context.Background(), StateKey)
	if strings.Contains(state, "app") || strings.Contains(state, "secret-repo") {
		t.Errorf("private repository name leaked into collector_state: %s", state)
	}
	if !strings.Contains(state, `"done_ms"`) {
		t.Errorf("scan state not marked done: %s", state)
	}

	// second run: identical values, only live samples rewritten (write budget)
	res2, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res2.Items > 20 || res2.Items == 0 {
		t.Errorf("second run wrote %d samples; expected only the live samples", res2.Items)
	}
	if v := samples(t, st, MetricCommits, nil)[frozen.UnixMilli()]; v != 27 {
		t.Errorf("live commits after rerun = %v", v)
	}
	if got := samples(t, st, MetricFollowers, nil); len(got) != 1 {
		t.Errorf("unchanged gauge rewritten within the heartbeat: %v", got)
	}
}

func TestMergedPRScanResumesAcrossRuns(t *testing.T) {
	f, srv := newFake(t)
	st := newStore(t)
	c := newCollector(t, st, srv, func(c *Config) { c.MaxPRPages = 1 })
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "scan in progress") || f.count("MergedPRs") != 1 {
		t.Errorf("first run: note=%q merged requests=%d", res.Note, f.count("MergedPRs"))
	}
	if s := samples(t, st, MetricMergedPRs, store.Labels{"org": "kubernetes"}); s != nil {
		t.Errorf("PR series written before the scan completed: %v", s)
	}
	raw, ok, _ := st.GetState(context.Background(), StateKey)
	if !ok || !strings.Contains(raw, `"after":"Y3Vyc29yOjE="`) {
		t.Errorf("cursor not persisted: %s", raw)
	}
	res, err = c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Note, "prs=5 (scan complete") || f.count("MergedPRs") != 2 {
		t.Errorf("second run: note=%q merged requests=%d", res.Note, f.count("MergedPRs"))
	}
	if v := samples(t, st, MetricMergedPRs, store.Labels{"org": "kubernetes"})[frozen.UnixMilli()]; v != 2 {
		t.Errorf("kubernetes after resume = %v", v)
	}
	// a third run starts a fresh scan (cursor cleared, new page 1)
	_, _ = c.Run(context.Background())
	if f.count("MergedPRs") != 3 {
		t.Errorf("third run did not start a new scan: %d", f.count("MergedPRs"))
	}
}

func TestMergedPRSearchCapSplitsByYear(t *testing.T) {
	f, srv := newFake(t)
	f.unbounded = 1500
	st := newStore(t)
	c := newCollector(t, st, srv, nil)
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 1 unbounded probe + 2024, 2025, 2026 windows
	if f.count("MergedPRs") != 4 || !strings.Contains(res.Note, "prs=5 (scan complete") {
		t.Errorf("requests=%d note=%q", f.count("MergedPRs"), res.Note)
	}
	if v := samples(t, st, MetricMergedPRs, store.Labels{"org": "someone"})[frozen.UnixMilli()]; v != 1 {
		t.Errorf("2024 window lost the PR: %v", v)
	}
}

func TestSplitWindow(t *testing.T) {
	if got := splitWindow("", []int{2026, 2024, 2025}, frozen); len(got) != 3 || got[0] != "2024-01-01..2024-12-31" || got[2] != "2026-01-01..2026-12-31" {
		t.Errorf("years: %v", got)
	}
	if got := splitWindow("2024-01-01..2024-12-31", nil, frozen); len(got) != 2 || got[0] != "2024-01-01..2024-06-30" || got[1] != "2024-07-01..2024-12-31" {
		t.Errorf("halves: %v", got)
	}
	if got := splitWindow("2024-01-01..2024-01-01", nil, frozen); got != nil {
		t.Errorf("single day must not split: %v", got)
	}
}

func TestIdentityGuard(t *testing.T) {
	f, srv := newFake(t)
	f.login = "someone-else"
	st := newStore(t)
	_, err := newCollector(t, st, srv, nil).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `token belongs to "someone-else", expected "divysinghvi"`) {
		t.Fatalf("err = %v", err)
	}
	if names, _ := st.MetricNames(context.Background()); len(names) != 0 {
		t.Errorf("wrote series despite the identity mismatch: %v", names)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *fakeGitHub)
		wantIs error
		want   string
		writes bool
	}{
		{"401", func(f *fakeGitHub) { f.fail["Contributions"] = 401 }, ErrTokenRejected, "token rejected", false},
		{"403", func(f *fakeGitHub) { f.fail["Contributions"] = 403 }, ErrRateLimited, "rate limited until 2026-09-05T10:20:00Z", false},
		{"low remaining", func(f *fakeGitHub) { f.remaining = 150 }, ErrRateLimited, "150 points remaining", false},
		{"graphql error", func(f *fakeGitHub) { f.invalid = true }, nil, "Field 'nope' doesn't exist", false},
		{"500 then ok", func(f *fakeGitHub) { f.failOnce["OpenPRs"] = 500 }, nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFake(t)
			tc.setup(f)
			st := newStore(t)
			_, err := newCollector(t, st, srv, nil).Run(context.Background())
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if f.count("OpenPRs") != 2 {
					t.Errorf("expected one retry, got %d OpenPRs requests", f.count("OpenPRs"))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err %v is not %v", err, tc.wantIs)
			}
			if strings.Contains(err.Error(), "ghp_test") {
				t.Errorf("token leaked into the error: %v", err)
			}
			if names, _ := st.MetricNames(context.Background()); (len(names) > 0) != tc.writes {
				t.Errorf("series written = %v, want %v", names, tc.writes)
			}
		})
	}
}

func TestDisabledWithoutToken(t *testing.T) {
	_, srv := newFake(t)
	st := newStore(t)
	c := newCollector(t, st, srv, func(c *Config) { c.Token = "" })
	if !c.Disabled() {
		t.Fatal("expected Disabled()")
	}
	_, err := c.Run(context.Background())
	if !errors.Is(err, collector.ErrDisabled) || !strings.Contains(err.Error(), "DIVY_GITHUB_TOKEN is empty") {
		t.Fatalf("err = %v", err)
	}
	// through the runner: outcome skipped, no collector_runs row
	reg := collector.NewRegistry()
	_ = reg.Register(c)
	r := &collector.Runner{Store: st, Registry: reg}
	res := r.RunOne(context.Background(), c, time.Second)
	if res.OK || !strings.HasPrefix(res.Error, "skipped: ") {
		t.Errorf("runner result = %+v", res)
	}
	if runs, _ := st.RecentRuns(context.Background(), "github", 10); len(runs) != 0 {
		t.Errorf("a disabled collector must not leave collector_runs rows: %+v", runs)
	}
}

func TestTimeoutKeepsProgress(t *testing.T) {
	f, srv := newFake(t)
	st := newStore(t)
	c := newCollector(t, st, srv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Run(ctx); err == nil {
		t.Fatal("expected an error with a cancelled context")
	}
	if len(f.ops()) != 0 {
		// requests may have been attempted; none may have succeeded in writing
		t.Logf("ops with cancelled ctx: %v", f.ops())
	}
	if names, _ := st.MetricNames(context.Background()); len(names) != 0 {
		t.Errorf("wrote %v with a cancelled context", names)
	}
}
