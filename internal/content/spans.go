package content

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"divy.dev/internal/model"
)

// RootSpanID is the id of the career trace's root span.
const RootSpanID = "divy.career"

// TraceID is hex(sha256("divy.career")[0:16]) — the fixed id of the career trace.
var TraceID = hex.EncodeToString(sum256(RootSpanID)[:16])

func sum256(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// SpanHexID derives a Jaeger span id from a content span id: hex(sha256(id)[0:8]).
func SpanHexID(id string) string { return hex.EncodeToString(sum256(id)[:8]) }

// Node is a span with its resolved interval and tree position.
type Node struct {
	Span     *model.Span
	Parent   *Node
	Depth    int
	Children []*Node // sorted by (resolved start, id)
	Path     string  // JSONPath in spans.yaml

	// Start is the resolved start; StartPrecision is year|month|day|todo.
	Start          time.Time
	StartPrecision Precision
	// End is the resolved end for closed spans, or the planned end for open
	// spans that have one; zero for open spans without a planned end.
	End          time.Time
	EndPrecision Precision // year|month|day|todo|open
	// startErr/endErr record calendar problems for the rules pass.
	startErr, endErr error
}

// Open reports whether the span is still running.
func (n *Node) Open() bool { return n.Span.Open }

// EffectiveEnd is the end used for durations: closed → End; open → now.
func (n *Node) EffectiveEnd(now time.Time) time.Time {
	if n.Span.Open {
		return now
	}
	return n.End
}

// AxisEnd is the right edge on a time axis: planned end for open spans when
// it lies after now, else EffectiveEnd.
func (n *Node) AxisEnd(now time.Time) time.Time {
	if n.Span.Open && !n.End.IsZero() && n.End.After(now) {
		return n.End
	}
	return n.EffectiveEnd(now)
}

// TodoDerived reports whether either edge came from a TODO(divy) fallback.
func (n *Node) TodoDerived() bool {
	return n.StartPrecision == PrecisionTodo || n.EndPrecision == PrecisionTodo
}

// Title returns the span title or its id.
func (n *Node) Title() string {
	if n.Span.Title != "" {
		return n.Span.Title
	}
	return n.Span.ID
}

// Nodes returns every span in DFS order (children sorted by start, id).
func (c *Content) Nodes() []*Node { return c.nodes }

// Node returns the node of a span id.
func (c *Content) Node(id string) (*Node, bool) {
	n, ok := c.byID[id]
	return n, ok
}

// Root returns the root node (nil when spans.yaml did not load).
func (c *Content) Root() *Node {
	if len(c.nodes) == 0 {
		return nil
	}
	return c.nodes[0]
}

// buildTree resolves dates with the TODO fallbacks and orders children.
func (c *Content) buildTree(now time.Time) {
	root := &Node{Span: &c.Spans.Trace, Path: "$.trace"}
	c.resolve(root, now)
	c.nodes = nil
	c.byID = map[string]*Node{}
	var walk func(n *Node)
	walk = func(n *Node) {
		c.nodes = append(c.nodes, n)
		if _, dup := c.byID[n.Span.ID]; !dup {
			c.byID[n.Span.ID] = n
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(root)
}

func (c *Content) resolve(n *Node, now time.Time) {
	sp := n.Span
	// start
	t, p, err := ParseDate(string(sp.Start))
	n.startErr = err
	if p == PrecisionTodo || err != nil {
		n.StartPrecision = PrecisionTodo
		if n.Parent != nil {
			n.Start = n.Parent.Start
		} else {
			n.Start = time.Time{}
		}
	} else {
		n.Start, n.StartPrecision = t, p
	}
	// end
	switch {
	case sp.Open && sp.End == "":
		n.EndPrecision = PrecisionOpen
	case sp.End == "":
		// missing end on a closed span: schema error; fall back like a TODO
		n.EndPrecision = PrecisionTodo
		n.End = c.parentEnd(n, now)
	default:
		t, p, err := ParseDate(string(sp.End))
		n.endErr = err
		if p == PrecisionTodo || err != nil {
			n.EndPrecision = PrecisionTodo
			n.End = c.parentEnd(n, now)
		} else {
			n.End, n.EndPrecision = EndOf(t, p), p
		}
	}
	for i := range sp.Children {
		// Paths follow the file order, not the sorted order.
		ch := &Node{Span: &sp.Children[i], Parent: n, Depth: n.Depth + 1, Path: n.Path + ".children[" + itoa(i) + "]"}
		c.resolve(ch, now)
		n.Children = append(n.Children, ch)
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if !a.Start.Equal(b.Start) {
			return a.Start.Before(b.Start)
		}
		return a.Span.ID < b.Span.ID
	})
}

// parentEnd is the fallback end for a TODO end: the parent's resolved end (open parent → now).
func (c *Content) parentEnd(n *Node, now time.Time) time.Time {
	p := n.Parent
	if p == nil {
		return now
	}
	if p.Span.Open {
		return now
	}
	return p.End
}

// ExperienceStart is the earliest non-TODO start among spans whose service
// counts as experience; ok is false when there is none.
func (c *Content) ExperienceStart() (time.Time, bool) {
	var best time.Time
	found := false
	for _, n := range c.nodes {
		if n.StartPrecision == PrecisionTodo {
			continue
		}
		svc, ok := c.services[n.Span.Service]
		if !ok || !svc.CountsAsExperience {
			continue
		}
		if !found || n.Start.Before(best) {
			best, found = n.Start, true
		}
	}
	return best, found
}

// ServicesWithSpans returns the service ids that own at least one span, sorted.
func (c *Content) ServicesWithSpans() []string {
	seen := map[string]bool{}
	for _, n := range c.nodes {
		seen[n.Span.Service] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
