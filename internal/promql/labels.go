package promql

import (
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
)

// MetricName is the label that carries the metric name.
const MetricName = "__name__"

// Label is one name/value pair.
type Label struct {
	Name  string
	Value string
}

// Labels is a label set sorted by name (Prometheus labels.Labels).
type Labels []Label

// NewLabels builds a sorted label set from a map.
func NewLabels(m map[string]string) Labels {
	out := make(Labels, 0, len(m))
	for k, v := range m {
		out = append(out, Label{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the value of a label ("" when absent).
func (ls Labels) Get(name string) string {
	for _, l := range ls {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// Has reports whether the label is present.
func (ls Labels) Has(name string) bool {
	for _, l := range ls {
		if l.Name == name {
			return true
		}
	}
	return false
}

// Map converts the label set to a map (for JSON output).
func (ls Labels) Map() map[string]string {
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}

// Without returns a copy without the named labels (and never mutates ls).
func (ls Labels) Without(names ...string) Labels {
	out := make(Labels, 0, len(ls))
	for _, l := range ls {
		drop := false
		for _, n := range names {
			if l.Name == n {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, l)
		}
	}
	return out
}

// Keep returns a copy holding only the named labels.
func (ls Labels) Keep(names ...string) Labels {
	out := make(Labels, 0, len(names))
	for _, l := range ls {
		for _, n := range names {
			if l.Name == n {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// WithName returns a copy with __name__ set (or added).
func (ls Labels) WithName(name string) Labels {
	out := ls.Without(MetricName)
	out = append(out, Label{MetricName, name})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// String renders the set as Prometheus does: {a="1", b="2"}.
func (ls Labels) String() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// key is a cheap unique string of the set (used for signatures and grouping).
func (ls Labels) key() string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte(0)
		b.WriteString(l.Value)
		b.WriteByte(0)
	}
	return b.String()
}

// MatchType is a label matcher operator.
type MatchType int

// Matcher operators.
const (
	MatchEqual MatchType = iota
	MatchNotEqual
	MatchRegexp
	MatchNotRegexp
)

func (t MatchType) String() string {
	return [...]string{"=", "!=", "=~", "!~"}[t]
}

// Matcher is one label matcher. Regular expressions are RE2 (Go regexp)
// and fully anchored, as in Prometheus.
type Matcher struct {
	Type  MatchType
	Name  string
	Value string
	re    *regexp.Regexp
}

// NewMatcher builds a matcher; the error for a bad regex is Go's own
// (`error parsing regexp: …`), which is what Prometheus reports too.
func NewMatcher(t MatchType, name, value string) (*Matcher, error) {
	m := &Matcher{Type: t, Name: name, Value: value}
	if t == MatchRegexp || t == MatchNotRegexp {
		if _, err := syntax.Parse(value, syntax.Perl|syntax.DotNL); err != nil {
			return nil, err
		}
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
	switch m.Type {
	case MatchEqual:
		return v == m.Value
	case MatchNotEqual:
		return v != m.Value
	case MatchRegexp:
		return m.re.MatchString(v)
	case MatchNotRegexp:
		return !m.re.MatchString(v)
	}
	return false
}

// String renders name, operator and the quoted value (Prometheus form).
func (m *Matcher) String() string {
	return m.Name + m.Type.String() + strconv.Quote(m.Value)
}

// MatchLabels reports whether every matcher accepts the label set (a label
// the set lacks is matched against "").
func MatchLabels(ms []*Matcher, ls Labels) bool {
	for _, m := range ms {
		if !m.Matches(ls.Get(m.Name)) {
			return false
		}
	}
	return true
}
