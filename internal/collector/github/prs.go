package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/store"
)

// StateKey is the collector_state row holding the merged-PR scan.
const StateKey = "github.prs"

// searchCap is GitHub's hard limit on search results per query.
const searchCap = 1000

// prEvent is one merged pull request: day, owner and — for public
// repositories outside the private owners only — the repository name.
// Private repository names never reach this struct, so they never reach the
// database, the logs or an error message.
type prEvent struct {
	D string `json:"d"`
	O string `json:"o"`
	R string `json:"r,omitempty"`
}

// prState is the resumable scan persisted in collector_state (JSON).
type prState struct {
	// Windows are "from..to" day ranges ("" = unbounded) still to be scanned, in order.
	Windows []string `json:"windows"`
	// I indexes the window being paginated; After is its cursor.
	I     int    `json:"i"`
	After string `json:"after,omitempty"`
	// Events collected so far in this scan.
	Events    []prEvent `json:"events"`
	StartedMs int64     `json:"started_ms"`
	Pages     int       `json:"pages"`
	// DoneMs is set when the scan completed; the next run starts a fresh scan.
	DoneMs int64 `json:"done_ms,omitempty"`
	Count  int   `json:"count,omitempty"`
}

func (c *Collector) loadState(ctx context.Context) (*prState, error) {
	raw, ok, err := c.st.GetState(ctx, StateKey)
	if err != nil || !ok {
		return nil, err
	}
	var s prState
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, nil // unreadable state: start over
	}
	return &s, nil
}

func (c *Collector) saveState(ctx context.Context, s *prState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return c.st.SetState(ctx, StateKey, string(b))
}

// scanMergedPRs runs Q2 for up to MaxPRPages pages, persisting the cursor
// after every page, and writes the series when the scan completes. A run
// that starts after a completed scan begins a new one, so retroactive
// changes (un-merged PR, renamed repository) are absorbed on the next pass.
func (c *Collector) scanMergedPRs(ctx context.Context, years []int, now time.Time) (int, string, error) {
	s, err := c.loadState(ctx)
	if err != nil {
		return 0, "", err
	}
	if s == nil || s.DoneMs != 0 || len(s.Windows) == 0 {
		s = &prState{Windows: []string{""}, StartedMs: now.UnixMilli()}
	}
	pages := 0
	for {
		if s.I >= len(s.Windows) {
			n, err := c.writeMergedPRs(ctx, s.Events, now)
			if err != nil {
				return 0, "", err
			}
			done := &prState{Windows: s.Windows, I: s.I, StartedMs: s.StartedMs, Pages: s.Pages, DoneMs: now.UnixMilli(), Count: len(s.Events)}
			if err := c.saveState(ctx, done); err != nil {
				return n, "", err
			}
			return n, fmt.Sprintf("prs=%d (scan complete, %d pages)", len(s.Events), s.Pages), nil
		}
		if pages >= c.cfg.MaxPRPages {
			break
		}
		w := s.Windows[s.I]
		q := fmt.Sprintf("is:pr is:merged author:%s", c.cfg.Login)
		if w != "" {
			q += " merged:" + w
		}
		vars := map[string]any{"q": q}
		if s.After != "" {
			vars["after"] = s.After
		}
		var data mergedPRsData
		rl, err := c.cl.query(ctx, queryMergedPRs, vars, &data)
		if err != nil {
			if !errCanceled(err) {
				return 0, "", err
			}
			// timed out: keep the progress made so far
			_ = c.saveState(context.WithoutCancel(ctx), s)
			return 0, "", err
		}
		pages++
		s.Pages++
		if err := c.checkRateLimit(rl); err != nil {
			return 0, "", err
		}
		if s.After == "" && data.Search.IssueCount > searchCap {
			if split := splitWindow(w, years, now); len(split) > 0 {
				s.Windows = append(append(append([]string{}, s.Windows[:s.I]...), split...), s.Windows[s.I+1:]...)
				if err := c.saveState(ctx, s); err != nil {
					return 0, "", err
				}
				continue
			}
			c.cfg.Logger.Warn("github: merged PR window exceeds the search cap; series may be incomplete", "window", w, "count", data.Search.IssueCount)
		}
		for _, n := range data.Search.Nodes {
			t, err := time.Parse(time.RFC3339, n.MergedAt)
			if err != nil {
				continue
			}
			ev := prEvent{D: collector.DayKey(t), O: n.Repository.Owner.Login}
			if !c.isPrivate(n.Repository.Owner.Login, n.Repository.IsPrivate) {
				ev.R = n.Repository.Name
			}
			s.Events = append(s.Events, ev)
		}
		if data.Search.PageInfo.HasNextPage {
			s.After = data.Search.PageInfo.EndCursor
		} else {
			s.I++
			s.After = ""
		}
		if err := c.saveState(ctx, s); err != nil {
			return 0, "", err
		}
	}
	return 0, fmt.Sprintf("prs scan in progress (window %d/%d, %d events so far)", s.I+1, len(s.Windows), len(s.Events)), nil
}

// splitWindow bisects a search window whose result count exceeds the cap:
// the unbounded window becomes one window per contribution year; a bounded
// window is halved down to single days.
func splitWindow(w string, years []int, now time.Time) []string {
	if w == "" {
		first := now.Year()
		for _, y := range years {
			if y < first && y > 2000 {
				first = y
			}
		}
		var out []string
		for y := first; y <= now.Year(); y++ {
			out = append(out, fmt.Sprintf("%04d-01-01..%04d-12-31", y, y))
		}
		return out
	}
	parts := strings.SplitN(w, "..", 2)
	if len(parts) != 2 {
		return nil
	}
	from, err1 := time.Parse("2006-01-02", parts[0])
	to, err2 := time.Parse("2006-01-02", parts[1])
	if err1 != nil || err2 != nil || !to.After(from) {
		return nil
	}
	mid := from.Add(to.Sub(from) / 2)
	mid = collector.DayOf(mid)
	if !mid.After(from) {
		return nil
	}
	return []string{
		from.Format("2006-01-02") + ".." + mid.AddDate(0, 0, -1).Format("2006-01-02"),
		mid.Format("2006-01-02") + ".." + to.Format("2006-01-02"),
	}
}

// writeMergedPRs lays out github_merged_prs_total{org} and
// github_merged_prs_by_repo_total{org,repo} from the full event list: grid
// from the day before the first merge (0) through yesterday, live at now.
func (c *Collector) writeMergedPRs(ctx context.Context, events []prEvent, now time.Time) (int, error) {
	byOrg := map[string]collector.DailyCounts{}
	byRepo := map[[2]string]collector.DailyCounts{}
	for _, e := range events {
		if e.O == "" || e.D == "" {
			continue
		}
		if byOrg[e.O] == nil {
			byOrg[e.O] = collector.DailyCounts{}
		}
		byOrg[e.O][e.D]++
		if e.R != "" {
			k := [2]string{e.O, e.R}
			if byRepo[k] == nil {
				byRepo[k] = collector.DailyCounts{}
			}
			byRepo[k][e.D]++
		}
	}
	b := collector.NewBatch(c.st)
	type series struct {
		metric string
		labels store.Labels
		counts collector.DailyCounts
	}
	var all []series
	for org, counts := range byOrg {
		all = append(all, series{MetricMergedPRs, store.Labels{"org": org}, counts})
	}
	for k, counts := range byRepo {
		all = append(all, series{MetricMergedByRepo, store.Labels{"org": k[0], "repo": k[1]}, counts})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].metric != all[j].metric {
			return all[i].metric < all[j].metric
		}
		return store.CanonicalLabels(all[i].labels) < store.CanonicalLabels(all[j].labels)
	})
	existing := map[string]collector.Existing{}
	for _, s := range all {
		first, _ := s.counts.First()
		id, err := c.st.EnsureSeries(ctx, s.metric, s.labels)
		if err != nil {
			return 0, err
		}
		ex, ok := existing[s.metric]
		if !ok {
			// one read per metric: every series, the whole history
			if ex, err = collector.LoadExisting(ctx, c.st, s.metric, 1, now.UnixMilli()); err != nil {
				return 0, err
			}
			existing[s.metric] = ex
		}
		grid, live := collector.CounterSamples(s.counts, first, now, 0, false)
		b.Counter(id, ex, grid, live)
	}
	return b.Commit(ctx)
}
