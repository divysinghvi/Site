package content

import (
	"strings"
	"time"

	"divy.dev/internal/model"
)

// Healthz renders the liveness body from profile.yaml.
func (c *Content) Healthz() model.Healthz {
	openTo := c.Profile.OpenTo
	if openTo == nil {
		openTo = []string{}
	}
	return model.Healthz{Status: "ok", OpenTo: openTo, TZ: c.Profile.TZ}
}

// ServicesView is /api/content/services in file order.
func (c *Content) ServicesView() model.ContentServices {
	s := c.Spans.Services
	if s == nil {
		s = []model.Service{}
	}
	return model.ContentServices{Services: s}
}

// OGImageURL returns the absolute OG image URL of a postmortem.
func OGImageURL(origin, id string) string {
	return strings.TrimRight(origin, "/") + "/og/postmortems/" + id + ".png"
}

func (c *Content) pmSummary(p *Postmortem, origin string) model.PostmortemSummary {
	return model.PostmortemSummary{PostmortemFrontmatter: p.Front, TodoCount: p.TodoCount, OgImage: OGImageURL(origin, p.Front.ID)}
}

// PostmortemList is /api/content/postmortems, sorted by id.
func (c *Content) PostmortemList(origin string) model.ContentPostmortemList {
	items := make([]model.PostmortemSummary, 0, len(c.Postmortems))
	for _, p := range c.Postmortems {
		items = append(items, c.pmSummary(p, origin))
	}
	return model.ContentPostmortemList{Items: items}
}

// PostmortemView is /api/content/postmortems/{id}.
func (c *Content) PostmortemView(id, origin string) (model.ContentPostmortem, bool) {
	p, ok := c.pms[id]
	if !ok {
		return model.ContentPostmortem{}, false
	}
	return model.ContentPostmortem{
		PostmortemSummary: c.pmSummary(p, origin),
		HTML:              p.HTML,
		TOC:               p.TOC,
		Markdown:          p.Markdown,
		SpanURL:           "/#trace?span=" + p.Front.Span,
	}, true
}

// Uptime defaults (Content §C.8).
const (
	DefaultProbeMethod   = "GET"
	DefaultProbeTimeout  = "10s"
	DefaultProbeInterval = "5m"
)

// UptimeView is /api/content/uptime with defaults applied.
func (c *Content) UptimeView() model.ContentUptime {
	out := model.ContentUptime{Targets: []model.UptimeTargetView{}}
	for _, t := range c.Uptime.Targets {
		out.Targets = append(out.Targets, TargetView(t))
	}
	return out
}

// TargetView applies the defaults to one uptime target.
func TargetView(t model.UptimeTarget) model.UptimeTargetView {
	v := model.UptimeTargetView{ID: t.ID, Name: t.Name, URL: t.URL, Method: t.Method, Timeout: t.Timeout, Interval: t.Interval, FollowRedirects: true, Configured: reHTTPURL.MatchString(t.URL)}
	if v.Method == "" {
		v.Method = DefaultProbeMethod
	}
	if v.Timeout == "" {
		v.Timeout = DefaultProbeTimeout
	}
	if v.Interval == "" {
		v.Interval = DefaultProbeInterval
	}
	if t.FollowRedirects != nil {
		v.FollowRedirects = *t.FollowRedirects
	}
	v.ExpectedStatus = []int(t.ExpectedStatus)
	if len(v.ExpectedStatus) == 0 {
		v.ExpectedStatus = []int{200}
	}
	if t.Span != "" {
		s := t.Span
		v.Span = &s
	}
	if t.Note != "" {
		n := t.Note
		v.Note = &n
	}
	return v
}

// ManualView is /api/content/manual-metrics.
func (c *Content) ManualView() model.ContentManualMetrics {
	m := c.Manual.Metrics
	if m == nil {
		m = []model.ManualMetric{}
	}
	return model.ContentManualMetrics{Metrics: m}
}

// ProfileView is /api/content/profile with the computed pod columns.
func (c *Content) ProfileView(now time.Time) model.ContentProfile {
	p := c.Profile
	out := model.ContentProfile{Name: p.Name, Handle: p.Handle, Location: p.Location, TZ: p.TZ, OpenToWork: p.OpenToWork, OpenTo: p.OpenTo, Tagline: p.Tagline, Links: p.Links, Escalation: p.Escalation, Pods: []model.PodView{}}
	if out.OpenTo == nil {
		out.OpenTo = []string{}
	}
	if out.Escalation == nil {
		out.Escalation = []model.Escalation{}
	}
	for _, pod := range p.Pods {
		pv := model.PodView{Pod: pod}
		if pod.RestartsFrom == "postmortems" {
			pv.Restarts = len(c.PostmortemsFor(pod.Span))
		}
		if n, ok := c.byID[pod.Span]; ok && !n.Start.IsZero() && now.After(n.Start) {
			pv.AgeS = int64(now.Sub(n.Start).Seconds())
		}
		out.Pods = append(out.Pods, pv)
	}
	return out
}

// ExperienceYears is divy_experience_years at t (0, false when no dated experience span exists).
func (c *Content) ExperienceYears(t time.Time) (float64, bool) {
	start, ok := c.ExperienceStart()
	if !ok || t.Before(start) {
		return 0, false
	}
	return t.Sub(start).Hours() / (24 * 365.25), true
}
