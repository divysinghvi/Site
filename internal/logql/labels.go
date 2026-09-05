package logql

import (
	"sort"
	"strconv"
	"strings"
)

// Label is one name/value pair.
type Label struct {
	Name  string
	Value string
}

// Labels is a label set kept sorted by name.
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

// Get returns the value of a label and whether it is present.
func (ls Labels) Get(name string) (string, bool) {
	i := sort.Search(len(ls), func(i int) bool { return ls[i].Name >= name })
	if i < len(ls) && ls[i].Name == name {
		return ls[i].Value, true
	}
	return "", false
}

// Has reports whether the label is present.
func (ls Labels) Has(name string) bool {
	_, ok := ls.Get(name)
	return ok
}

// With returns a copy with the label set (added or replaced).
func (ls Labels) With(name, value string) Labels {
	out := make(Labels, 0, len(ls)+1)
	done := false
	for _, l := range ls {
		if !done && l.Name >= name {
			if l.Name != name {
				out = append(out, Label{name, value})
				out = append(out, l)
			} else {
				out = append(out, Label{name, value})
			}
			done = true
			continue
		}
		out = append(out, l)
	}
	if !done {
		out = append(out, Label{name, value})
	}
	return out
}

// Keep returns a copy holding only the named labels.
func (ls Labels) Keep(names []string) Labels {
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

// Without returns a copy without the named labels.
func (ls Labels) Without(names []string) Labels {
	out := make(Labels, 0, len(ls))
outer:
	for _, l := range ls {
		for _, n := range names {
			if l.Name == n {
				continue outer
			}
		}
		out = append(out, l)
	}
	return out
}

// Map converts the set to a map (JSON output).
func (ls Labels) Map() map[string]string {
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}

// String renders the set as a Loki stream selector: {a="1", b="2"}.
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

// MatchAll reports whether every matcher accepts the set (an absent label is "").
func MatchAll(ms []*Matcher, ls Labels) bool {
	for _, m := range ms {
		v, _ := ls.Get(m.Name)
		if !m.Matches(v) {
			return false
		}
	}
	return true
}
