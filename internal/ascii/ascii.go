// Package ascii renders the career trace as a plain-text waterfall — the body
// of `curl -H 'Accept: text/plain' <site>/`, `GET /ascii` and `divy export-ascii`.
package ascii

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"divy.dev/internal/content"
)

// Options tune the renderer.
type Options struct {
	// Width is the total column count (runes); clamped to 60..200, default 80.
	Width int
	// Now freezes the clock (tests); zero = time.Now().
	Now time.Time
	// Origin is the absolute site origin printed in the footer.
	Origin string
	// Color wraps service names in ANSI 24-bit colour (CLI only).
	Color bool
}

const (
	serviceCol = 10
	nameCol    = 30
	minWidth   = 60
	maxWidth   = 200
	glyphDated = '▓'
	glyphTodo  = '░'
	glyphOpen  = '┄'
)

// ClampWidth applies the 60..200 rule with the default of 80.
func ClampWidth(w int) int {
	if w <= 0 {
		return 80
	}
	if w < minWidth {
		return minWidth
	}
	if w > maxWidth {
		return maxWidth
	}
	return w
}

// Render draws the waterfall.
func Render(c *content.Content, opts Options) string {
	w := ClampWidth(opts.Width)
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	origin := strings.TrimRight(opts.Origin, "/")
	if origin == "" {
		origin = "http://localhost:8080"
	}
	barCols := w - (serviceCol + 1 + nameCol + 1)
	root := c.Root()
	nodes := c.Nodes()
	if root == nil {
		return "no spans loaded\n"
	}
	t0 := root.Start
	t1 := now
	for _, n := range nodes {
		if n.Span.Open && !n.End.IsZero() && n.End.After(t1) {
			t1 = n.End
		}
	}
	if !t1.After(t0) {
		t1 = t0.Add(24 * time.Hour)
	}
	span := t1.Sub(t0).Seconds()
	col := func(t time.Time) int {
		c := int(float64(barCols) * (t.Sub(t0).Seconds() / span))
		if c < 0 {
			return 0
		}
		if c > barCols-1 {
			return barCols - 1
		}
		return c
	}
	colEnd := func(t time.Time) int { // exclusive end column, may equal barCols
		c := int(float64(barCols) * (t.Sub(t0).Seconds() / span))
		if c < 0 {
			return 0
		}
		if c > barCols {
			return barCols
		}
		return c
	}

	var sb strings.Builder
	rule := strings.Repeat("─", w)
	monthsPerCol := (span / float64(barCols)) / (30.44 * 86400)
	fmt.Fprintf(&sb, "%s · %s · %s → now · %d spans\n", content.RootSpanID, content.TraceID, t0.Format("2006-01-02"), len(nodes))
	fmt.Fprintf(&sb, "rendered %s · 1 col ≈ %.1f mo · %c dated  %c TODO(divy)  %c open\n", now.Format(time.RFC3339), monthsPerCol, glyphDated, glyphTodo, glyphOpen)
	sb.WriteString(rule + "\n")

	// header row with year labels
	header := []rune(strings.Repeat(" ", barCols))
	lastEnd := 0
	for y := t0.Year(); y <= t1.Year(); y++ {
		jan := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		if jan.Before(t0) || !jan.Before(t1) {
			continue
		}
		pos := col(jan)
		label := fmt.Sprintf("%d", y)
		if pos < lastEnd || pos+len(label) > barCols {
			continue
		}
		// skip when the next year's label would collide
		next := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
		if next.Before(t1) && col(next) < pos+len(label) {
			continue
		}
		copy(header[pos:], []rune(label))
		lastEnd = pos + len(label)
	}
	sb.WriteString(strings.TrimRight(pad("service", serviceCol)+" "+pad("span", nameCol)+" "+string(header), " ") + "\n")

	nowCol := colEnd(now)
	for _, n := range nodes {
		bar := []rune(strings.Repeat(" ", barCols))
		start := col(n.Start)
		var end int
		if n.Span.Open {
			end = nowCol
		} else {
			end = colEnd(n.End)
		}
		if end <= start {
			end = start + 1
		}
		if end > barCols {
			end = barCols
		}
		g := glyphDated
		if n.TodoDerived() {
			g = glyphTodo
		}
		for i := start; i < end; i++ {
			bar[i] = g
		}
		if n.Span.Open {
			for i := nowCol; i < barCols; i++ {
				if i >= 0 {
					bar[i] = glyphOpen
				}
			}
		}
		name := n.Span.ID
		if n.Depth >= 1 {
			name = strings.Repeat("  ", n.Depth-1) + "└ " + name
		}
		if n.Span.Status == "error" {
			name += " [ERR]"
		}
		name = cut(name, nameCol)
		svc := cut(n.Span.Service, serviceCol)
		svcCell := pad(svc, serviceCol)
		if opts.Color {
			if s, ok := c.Service(n.Span.Service); ok {
				svcCell = colorize(svcCell, s.Color)
			}
		}
		line := svcCell + " " + pad(name, nameCol) + " " + string(bar)
		sb.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	sb.WriteString(rule + "\n")
	fmt.Fprintf(&sb, "JSON: curl -s %s/api/traces/career | jq .data[0].spans\n", origin)
	sb.WriteString("logs: /loki/api/v1/query_range?query={service=\"gradr\"}   metrics: /metrics\n")
	return sb.String()
}

func pad(s string, n int) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	return s + strings.Repeat(" ", n-l)
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func colorize(s, hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return s
	}
	var r, g, b int
	if _, err := fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b); err != nil {
		return s
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, s)
}
