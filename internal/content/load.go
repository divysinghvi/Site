// Package content loads and validates the content/ directory: the career
// spans, log lines, postmortems, panels, alert rules, uptime targets, manual
// metrics and the profile. Everything the site says about Divy comes from
// here; nothing is hard-coded elsewhere.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"divy.dev/internal/model"
	"divy.dev/internal/schemagen"
)

// fileNames is the fixed file order of content/ (used for ordering findings and TODOs).
var fileNames = []string{"spans.yaml", "logs.ndjson", "postmortems", "panels.yaml", "alerts.yaml", "uptime.yaml", "manual_metrics.yaml", "profile.yaml"}

// Options tune loading.
type Options struct {
	// Now is the reference time for open spans and validation; zero = time.Now().
	Now time.Time
	// SelfURL replaces the url of the uptime target whose id is self-api (UPTIME_SELF_URL).
	SelfURL string
	// Schemas are the compiled validators; nil = schemagen.MustCompile().
	Schemas schemagen.Compiled
	// CheckExpr validates a PromQL expression (panels.expr, alerts); nil skips the check.
	CheckExpr func(expr string) error
}

// Content is the validated, in-memory content model.
type Content struct {
	Dir      string
	LoadedAt time.Time
	// Hash is the sha256 of every content file (constant per process).
	Hash string
	// Files is the number of content files read.
	Files int

	Spans       model.SpansFile
	Logs        []LogEntry
	LogsRaw     []byte
	Postmortems []*Postmortem
	Panels      model.PanelsFile
	Alerts      model.AlertsFile
	Uptime      model.UptimeFile
	Manual      model.ManualMetricsFile
	Profile     model.Profile
	Todos       []model.TodoItem

	// Report holds the validation findings; serve refuses to start when it has errors.
	Report *Report

	services map[string]model.Service
	nodes    []*Node          // DFS order
	byID     map[string]*Node // span id → node
	pms      map[string]*Postmortem
	loaded   map[string]bool // which files parsed cleanly
}

// LogEntry is one parsed log line with its ordering timestamp.
type LogEntry struct {
	Line   model.LogLine
	Raw    string // the line verbatim (what Loki serves)
	Index  int    // 0-based index among non-empty lines
	FileLn int    // 1-based line number in the file
	// TSNano is the ordering timestamp in nanoseconds: ts (or the span/root fallback) + Index.
	TSNano int64
	// Labels are the Loki stream labels: service, level and component when present.
	Labels map[string]string
}

// Postmortem is one rendered incident report.
type Postmortem struct {
	File     string
	Front    model.PostmortemFrontmatter
	Markdown string // the whole file, verbatim
	Body     string // markdown after the frontmatter
	HTML     string
	TOC      []model.TOCEntry
	Sections []string // H2 texts in order
	// TodoCount is the number of TODO(divy) markers in the file.
	TodoCount int
}

// Load reads dir, validates it and builds the derived structures. It returns
// an error only for I/O problems; validation findings are in Content.Report.
func Load(dir string, opts Options) (*Content, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	opts.Now = opts.Now.UTC()
	if opts.Schemas == nil {
		opts.Schemas = schemagen.MustCompile()
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("content: %s is not a directory", dir)
	}
	c := &Content{Dir: dir, LoadedAt: opts.Now, Report: &Report{}, services: map[string]model.Service{}, byID: map[string]*Node{}, pms: map[string]*Postmortem{}, loaded: map[string]bool{}}
	h := sha256.New()
	ld := &loader{c: c, opts: opts, hash: h}

	ld.loadSpans()
	ld.loadLogs()
	ld.loadPostmortems()
	ld.loadPanels()
	ld.loadAlerts()
	ld.loadUptime()
	ld.loadManual()
	ld.loadProfile()

	c.Hash = hex.EncodeToString(h.Sum(nil))
	c.Report.Files = c.Files

	if c.loaded["spans.yaml"] {
		c.buildTree(opts.Now)
	}
	c.orderLogs()
	ld.rules()
	ld.sanitizeAll()
	c.Todos = ld.todos
	c.Report.Todos = c.Todos
	c.Report.Sort()
	return c, nil
}

// MustLoad loads and panics on I/O errors or validation errors (tests).
func MustLoad(dir string, opts Options) *Content {
	c, err := Load(dir, opts)
	if err != nil {
		panic(err)
	}
	if c.Report.HasErrors(false) {
		var sb strings.Builder
		c.Report.Write(&sb, false)
		panic("content: invalid: " + sb.String())
	}
	return c
}

type loader struct {
	c     *Content
	opts  Options
	hash  interface{ Write([]byte) (int, error) }
	todos []model.TodoItem
	raws  map[string][]byte   // rel file → bytes (for sanitizer)
	docs  map[string]*yamlDoc // file name → parsed YAML (for line numbers)
}

func (l *loader) rel(name string) string { return "content/" + name }

func (l *loader) read(name string) ([]byte, bool) {
	p := filepath.Join(l.c.Dir, name)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			l.c.Report.errorf(l.rel(name), 0, 0, "file.missing", "", "required file is missing")
		} else {
			l.c.Report.errorf(l.rel(name), 0, 0, "file.read", "", "%v", err)
		}
		return nil, false
	}
	l.c.Files++
	_, _ = l.hash.Write([]byte(name))
	_, _ = l.hash.Write(b)
	if l.raws == nil {
		l.raws = map[string][]byte{}
	}
	l.raws[l.rel(name)] = b
	return b, true
}

// loadYAMLInto parses, schema-validates and decodes one YAML content file.
func (l *loader) loadYAMLInto(name, schema string, out any) *yamlDoc {
	raw, ok := l.read(name)
	if !ok {
		return nil
	}
	rel := l.rel(name)
	doc, err := parseYAML(rel, raw)
	if err != nil {
		l.c.Report.errorf(rel, 0, 0, "yaml.parse", "", "%v", err)
		return nil
	}
	l.checkDatesQuoted(doc)
	errs, err := validateJSON(l.opts.Schemas[schema], doc.json)
	if err != nil {
		l.c.Report.errorf(rel, 0, 0, "schema", "", "%v", err)
		return nil
	}
	for _, e := range errs {
		line, col := 0, 0
		if n := locate(doc.root, e.ptr); n != nil {
			line, col = n.Line, n.Column
		}
		l.c.Report.errorf(rel, line, col, "schema", jsonPath(e.ptr), "%s", e.msg)
	}
	if len(errs) > 0 {
		return nil
	}
	if err := decodeStrict(doc.json, out); err != nil {
		l.c.Report.errorf(rel, 0, 0, "schema", "", "decode: %v", err)
		return nil
	}
	l.c.loaded[name] = true
	if l.docs == nil {
		l.docs = map[string]*yamlDoc{}
	}
	l.docs[name] = doc
	l.collectYAMLTodos(doc)
	return doc
}

func (l *loader) loadSpans() {
	l.loadYAMLInto("spans.yaml", "spans", &l.c.Spans)
	for _, s := range l.c.Spans.Services {
		l.c.services[s.ID] = s
	}
}

func (l *loader) loadPanels()  { l.loadYAMLInto("panels.yaml", "panels", &l.c.Panels) }
func (l *loader) loadAlerts()  { l.loadYAMLInto("alerts.yaml", "alerts", &l.c.Alerts) }
func (l *loader) loadManual()  { l.loadYAMLInto("manual_metrics.yaml", "manual_metrics", &l.c.Manual) }
func (l *loader) loadProfile() { l.loadYAMLInto("profile.yaml", "profile", &l.c.Profile) }

func (l *loader) loadUptime() {
	if l.loadYAMLInto("uptime.yaml", "uptime", &l.c.Uptime) == nil {
		return
	}
	if l.opts.SelfURL != "" {
		for i := range l.c.Uptime.Targets {
			if l.c.Uptime.Targets[i].ID == "self-api" {
				l.c.Uptime.Targets[i].URL = l.opts.SelfURL
			}
		}
	}
}

// Service returns a service by id.
func (c *Content) Service(id string) (model.Service, bool) {
	s, ok := c.services[id]
	return s, ok
}

// ServiceIDs returns the service ids in file order.
func (c *Content) ServiceIDs() []string {
	out := make([]string, 0, len(c.Spans.Services))
	for _, s := range c.Spans.Services {
		out = append(out, s.ID)
	}
	return out
}

// Postmortem returns a postmortem by id.
func (c *Content) Postmortem(id string) (*Postmortem, bool) {
	p, ok := c.pms[id]
	return p, ok
}

// PostmortemsFor returns the postmortem ids whose span is id or a descendant of it, sorted.
func (c *Content) PostmortemsFor(spanID string) []string {
	var out []string
	for _, p := range c.Postmortems {
		n := c.byID[p.Front.Span]
		for n != nil {
			if n.Span.ID == spanID {
				out = append(out, p.Front.ID)
				break
			}
			n = n.Parent
		}
	}
	sort.Strings(out)
	return out
}
