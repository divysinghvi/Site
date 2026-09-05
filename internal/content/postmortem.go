package content

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"

	"divy.dev/internal/model"
)

// RequiredSections are the eight H2 headings every postmortem must have, in order.
var RequiredSections = []string{"Summary", "Impact", "Timeline (UTC)", "Root cause", "Detection", "Resolution", "Action items", "Lessons"}

var pmFileRe = regexp.MustCompile(`^INC-[0-9]{3}\.md$`)

func (l *loader) loadPostmortems() {
	dir := filepath.Join(l.c.Dir, "postmortems")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			l.c.Report.errorf(l.rel("postmortems"), 0, 0, "file.missing", "", "directory is missing")
		} else {
			l.c.Report.errorf(l.rel("postmortems"), 0, 0, "file.read", "", "%v", err)
		}
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && pmFileRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		l.loadPostmortem("postmortems/" + name)
	}
	l.c.loaded["postmortems"] = true
}

func (l *loader) loadPostmortem(name string) {
	raw, ok := l.read(name)
	if !ok {
		return
	}
	rel := l.rel(name)
	fm, body, fmLine, err := splitFrontmatter(raw)
	if err != nil {
		l.c.Report.errorf(rel, 1, 1, "pm.frontmatter", "", "%v", err)
		return
	}
	doc, err := parseYAML(rel, fm)
	if err != nil {
		l.c.Report.errorf(rel, fmLine, 1, "pm.frontmatter", "", "%v", err)
		return
	}
	// shift node lines to file lines
	shiftLines(doc.root, fmLine-1)
	l.checkDatesQuoted(doc)
	errs, err := validateJSON(l.opts.Schemas["postmortem"], doc.json)
	if err != nil {
		l.c.Report.errorf(rel, fmLine, 1, "pm.frontmatter", "", "%v", err)
		return
	}
	for _, e := range errs {
		line, col := fmLine, 1
		if n := locate(doc.root, e.ptr); n != nil {
			line, col = n.Line, n.Column
		}
		l.c.Report.errorf(rel, line, col, "pm.frontmatter", jsonPath(e.ptr), "%s", e.msg)
	}
	if len(errs) > 0 {
		return
	}
	var front model.PostmortemFrontmatter
	if err := decodeStrict(doc.json, &front); err != nil {
		l.c.Report.errorf(rel, fmLine, 1, "pm.frontmatter", "", "decode: %v", err)
		return
	}
	stem := strings.TrimSuffix(filepath.Base(name), ".md")
	if front.ID != stem {
		l.c.Report.errorf(rel, fmLine, 1, "pm.frontmatter", "$.id", "id %q must equal the file name stem %q", front.ID, stem)
	}
	pm := &Postmortem{File: rel, Front: front, Markdown: string(raw), Body: body}
	pm.HTML, pm.TOC, pm.Sections = renderMarkdown([]byte(body))
	pm.TodoCount = strings.Count(string(raw), "TODO(divy)")
	l.collectYAMLTodos(doc)
	l.collectMarkdownTodos(rel, front.ID, body, fmLine+strings.Count(string(fm), "\n")+2)
	l.c.Postmortems = append(l.c.Postmortems, pm)
	if _, dup := l.c.pms[front.ID]; dup {
		l.c.Report.errorf(rel, fmLine, 1, "pm.frontmatter", "$.id", "duplicate postmortem id %s", front.ID)
	} else {
		l.c.pms[front.ID] = pm
	}
}

// splitFrontmatter separates the leading --- block. It returns the YAML
// bytes, the body, and the 1-based line of the first YAML line.
func splitFrontmatter(raw []byte) (fm []byte, body string, fmLine int, err error) {
	s := string(raw)
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return nil, "", 0, fmt.Errorf("file must start with a --- frontmatter block")
	}
	rest := s[strings.Index(s, "\n")+1:]
	end := -1
	lines := strings.Split(rest, "\n")
	var yamlLines []string
	for i, ln := range lines {
		if strings.TrimRight(ln, "\r") == "---" {
			end = i
			break
		}
		yamlLines = append(yamlLines, ln)
	}
	if end < 0 {
		return nil, "", 0, fmt.Errorf("frontmatter block is not closed with ---")
	}
	body = strings.Join(lines[end+1:], "\n")
	return []byte(strings.Join(yamlLines, "\n") + "\n"), body, 2, nil
}

func shiftLines(n *yaml.Node, delta int) {
	if n == nil {
		return
	}
	n.Line += delta
	for _, c := range n.Content {
		shiftLines(c, delta)
	}
}

// fixedSlugger implements the fixed heading-id rule: lowercase, runs of
// [^a-z0-9] → "-", trimmed; duplicates get -2, -3, …
type fixedSlugger struct{ used map[string]int }

// Slug applies the fixed rule without de-duplication.
func Slug(s string) string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			dash = false
		} else if !dash && sb.Len() > 0 {
			sb.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(sb.String(), "-")
}

func (f *fixedSlugger) Generate(value []byte, kind ast.NodeKind) []byte {
	base := Slug(string(value))
	if base == "" {
		base = "heading"
	}
	id := base
	if n := f.used[base]; n > 0 {
		id = fmt.Sprintf("%s-%d", base, n+1)
	}
	f.used[base]++
	return []byte(id)
}

func (f *fixedSlugger) Put(value []byte) { f.used[string(value)]++ }

var (
	pmPolicy = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-z0-9-]+$`)).OnElements("h2", "h3", "h4")
		p.AllowElements("input")
		p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
		p.AllowAttrs("checked", "disabled").Matching(regexp.MustCompile(`^(checked|disabled|)$`)).OnElements("input")
		p.AllowAttrs("class").Matching(regexp.MustCompile(`^(task-list-item|language-[a-z0-9-]+)$`)).OnElements("li", "code", "pre")
		p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|center|right)$`)).OnElements("td", "th")
		return p
	}()
)

// renderMarkdown renders CommonMark+GFM to sanitized HTML with fixed heading
// ids, and returns the TOC (h2/h3) and the H2 texts in order.
func renderMarkdown(src []byte) (string, []model.TOCEntry, []string) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithXHTML()),
	)
	ctx := parser.NewContext(parser.WithIDs(&fixedSlugger{used: map[string]int{}}))
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(ctx))
	var toc []model.TOCEntry
	var h2 []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			txt := headingText(h, src)
			id := ""
			if v, ok := h.AttributeString("id"); ok {
				if b, ok := v.([]byte); ok {
					id = string(b)
				}
			}
			if h.Level == 2 {
				h2 = append(h2, txt)
			}
			if h.Level == 2 || h.Level == 3 {
				toc = append(toc, model.TOCEntry{Level: h.Level, ID: id, Text: txt})
			}
		}
		return ast.WalkContinue, nil
	})
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, nil
	}
	out := pmPolicy.SanitizeBytes(buf.Bytes())
	if toc == nil {
		toc = []model.TOCEntry{}
	}
	return string(out), toc, h2
}

func headingText(h *ast.Heading, src []byte) string {
	var sb strings.Builder
	_ = ast.Walk(h, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(src))
		case *ast.String:
			sb.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(sb.String())
}
