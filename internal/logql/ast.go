package logql

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Query is a parsed LogQL query: a log query, a metric query or a scalar.
type Query interface {
	isQuery()
	// String renders the canonical form of the query.
	String() string
}

// LogQuery is a stream selector followed by pipeline stages.
type LogQuery struct {
	Selector []*Matcher
	Stages   []Stage
}

// MetricQuery is a range aggregation, optionally wrapped in a vector aggregation.
type MetricQuery struct {
	Agg   *Aggregation // nil = bare range aggregation
	Range *RangeAgg
}

// ScalarQuery is a constant-folded vector() expression (Grafana's health check).
type ScalarQuery struct {
	Value float64
}

func (*LogQuery) isQuery()    {}
func (*MetricQuery) isQuery() {}
func (*ScalarQuery) isQuery() {}

// MatchOp is a string matcher operator.
type MatchOp int

// Matcher operators.
const (
	OpEq  MatchOp = iota // =
	OpNeq                // !=
	OpRe                 // =~
	OpNre                // !~
)

func (o MatchOp) String() string { return [...]string{"=", "!=", "=~", "!~"}[o] }

// Matcher is one label matcher of a stream selector (regexes anchored).
type Matcher struct {
	Name  string
	Op    MatchOp
	Value string
	re    *regexp.Regexp
}

// NewMatcher builds a matcher; regexes are RE2 and anchored ^(?s:v)$ like
// Prometheus'. A bad regex returns Go's `error parsing regexp: …`.
func NewMatcher(name string, op MatchOp, value string) (*Matcher, error) {
	m := &Matcher{Name: name, Op: op, Value: value}
	if op == OpRe || op == OpNre {
		re, err := regexp.Compile("^(?s:" + value + ")$")
		if err != nil {
			return nil, err
		}
		m.re = re
	}
	return m, nil
}

// Matches reports whether the matcher accepts v.
func (m *Matcher) Matches(v string) bool {
	switch m.Op {
	case OpEq:
		return v == m.Value
	case OpNeq:
		return v != m.Value
	case OpRe:
		return m.re.MatchString(v)
	case OpNre:
		return !m.re.MatchString(v)
	}
	return false
}

// String renders name, operator and quoted value.
func (m *Matcher) String() string { return m.Name + m.Op.String() + strconv.Quote(m.Value) }

// Stage is one pipeline stage.
type Stage interface {
	isStage()
	String() string
}

// LineOp is a line filter operator.
type LineOp int

// Line filter operators.
const (
	LineContains    LineOp = iota // |=
	LineNotContains               // !=
	LineMatches                   // |~
	LineNotMatches                // !~
)

func (o LineOp) String() string { return [...]string{"|=", "!=", "|~", "!~"}[o] }

// LineFilter tests the raw line text.
type LineFilter struct {
	Op   LineOp
	Text string
	re   *regexp.Regexp // unanchored, |~ and !~ only
}

// Matches reports whether the raw line passes the filter.
func (f *LineFilter) Matches(line string) bool {
	switch f.Op {
	case LineContains:
		return strings.Contains(line, f.Text)
	case LineNotContains:
		return !strings.Contains(line, f.Text)
	case LineMatches:
		return f.re.MatchString(line)
	case LineNotMatches:
		return !f.re.MatchString(line)
	}
	return false
}

// JSONParser is the `| json` stage.
type JSONParser struct{}

// LabelFilter is a `| <expr>` stage over the entry's label set.
type LabelFilter struct {
	Expr LFExpr
}

func (*LineFilter) isStage()  {}
func (*JSONParser) isStage()  {}
func (*LabelFilter) isStage() {}

// LFExpr is a label filter expression.
type LFExpr interface {
	isLF()
	String() string
}

// LFOr is `a or b`.
type LFOr struct{ L, R LFExpr }

// LFAnd is `a and b` (also `a, b`).
type LFAnd struct{ L, R LFExpr }

// LFString compares a label against a string (regexes anchored).
type LFString struct {
	Name  string
	Op    MatchOp
	Value string
	re    *regexp.Regexp
}

// CmpOp is a numeric comparison operator.
type CmpOp int

// Numeric operators.
const (
	CmpEq  CmpOp = iota // ==
	CmpNeq              // !=
	CmpGt               // >
	CmpGte              // >=
	CmpLt               // <
	CmpLte              // <=
)

func (o CmpOp) String() string { return [...]string{"==", "!=", ">", ">=", "<", "<="}[o] }

// NumKind says how a numeric label filter parses the label value.
type NumKind int

// Numeric literal kinds.
const (
	NumPlain    NumKind = iota // strconv.ParseFloat
	NumDuration                // seconds; label parsed as a duration
	NumBytes                   // bytes; label parsed as a bytes literal
)

// LFNumber compares a label numerically. Value is seconds for durations and
// bytes for bytes literals; Text keeps the literal as written.
type LFNumber struct {
	Name  string
	Op    CmpOp
	Value float64
	Kind  NumKind
	Text  string
}

func (*LFOr) isLF()     {}
func (*LFAnd) isLF()    {}
func (*LFString) isLF() {}
func (*LFNumber) isLF() {}

// RangeAgg is count_over_time or rate over a log query and a range.
type RangeAgg struct {
	Fn        string // count_over_time | rate
	Log       *LogQuery
	Range     time.Duration
	RangeText string
}

// Aggregation is sum/count/min/max/avg with an optional grouping.
type Aggregation struct {
	Op       string
	Grouping bool // by or without present
	Without  bool
	Labels   []string
}

// ---- printing ----

func selectorString(ms []*Matcher) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(m.String())
	}
	b.WriteByte('}')
	return b.String()
}

func (q *LogQuery) String() string {
	var b strings.Builder
	b.WriteString(selectorString(q.Selector))
	for _, s := range q.Stages {
		b.WriteByte(' ')
		b.WriteString(s.String())
	}
	return b.String()
}

func (f *LineFilter) String() string  { return f.Op.String() + " " + strconv.Quote(f.Text) }
func (*JSONParser) String() string    { return "| json" }
func (f *LabelFilter) String() string { return "| " + f.Expr.String() }

func (e *LFOr) String() string { return e.L.String() + " or " + e.R.String() }

func (e *LFAnd) String() string {
	l, r := e.L.String(), e.R.String()
	if _, ok := e.L.(*LFOr); ok {
		l = "(" + l + ")"
	}
	if _, ok := e.R.(*LFOr); ok {
		r = "(" + r + ")"
	}
	return l + " and " + r
}

func (e *LFString) String() string { return e.Name + e.Op.String() + strconv.Quote(e.Value) }
func (e *LFNumber) String() string { return e.Name + " " + e.Op.String() + " " + e.Text }

func (r *RangeAgg) String() string {
	inner := r.Log.String()
	if len(r.Log.Stages) > 0 {
		inner += " "
	}
	return r.Fn + "(" + inner + "[" + r.RangeText + "])"
}

func (q *MetricQuery) String() string {
	if q.Agg == nil {
		return q.Range.String()
	}
	a := q.Agg
	if !a.Grouping {
		return a.Op + "(" + q.Range.String() + ")"
	}
	kw := "by"
	if a.Without {
		kw = "without"
	}
	return a.Op + " " + kw + " (" + strings.Join(a.Labels, ", ") + ") (" + q.Range.String() + ")"
}

func (q *ScalarQuery) String() string {
	return "vector(" + strconv.FormatFloat(q.Value, 'f', -1, 64) + ")"
}
