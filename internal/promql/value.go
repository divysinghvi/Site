package promql

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Value is a query result: Vector, Matrix, Scalar or String.
type Value interface {
	Type() ValueType
	String() string
	// MarshalJSON renders the `result` field of the HTTP API envelope.
	MarshalJSON() ([]byte, error)
}

// Point is one sample of a series.
type Point struct {
	T int64 // unix milliseconds
	F float64
}

// String is `<value> @[<ms>]`.
func (p Point) String() string {
	return strconv.FormatFloat(p.F, 'f', -1, 64) + " @[" + strconv.FormatInt(p.T, 10) + "]"
}

// Sample is one element of an instant vector.
type Sample struct {
	Metric Labels
	T      int64
	F      float64
}

// String is `<labels> => <value> @[<ms>]`.
func (s Sample) String() string {
	return s.Metric.String() + " => " + Point{s.T, s.F}.String()
}

// Vector is an instant vector.
type Vector []Sample

// Type is "vector".
func (Vector) Type() ValueType { return ValueTypeVector }

// String prints one sample per line.
func (v Vector) String() string {
	parts := make([]string, len(v))
	for i, s := range v {
		parts[i] = s.String()
	}
	return strings.Join(parts, "\n")
}

// MarshalJSON renders `[{"metric":{…},"value":[t,"v"]},…]`.
func (v Vector) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"metric":`)
		writeLabelsJSON(&b, s.Metric)
		b.WriteString(`,"value":`)
		writePairJSON(&b, s.T, s.F)
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.Bytes(), nil
}

// Series is one series of a matrix.
type Series struct {
	Metric Labels
	Points []Point
}

// String is `<labels> =>\n<point>\n…`.
func (s Series) String() string {
	parts := make([]string, len(s.Points))
	for i, p := range s.Points {
		parts[i] = p.String()
	}
	return s.Metric.String() + " =>\n" + strings.Join(parts, "\n")
}

// Matrix is a range vector / range query result.
type Matrix []Series

// Type is "matrix".
func (Matrix) Type() ValueType { return ValueTypeMatrix }

// String prints one series per block.
func (m Matrix) String() string {
	parts := make([]string, len(m))
	for i, s := range m {
		parts[i] = s.String()
	}
	return strings.Join(parts, "\n")
}

// MarshalJSON renders `[{"metric":{…},"values":[[t,"v"],…]},…]`.
func (m Matrix) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"metric":`)
		writeLabelsJSON(&b, s.Metric)
		b.WriteString(`,"values":[`)
		for j, p := range s.Points {
			if j > 0 {
				b.WriteByte(',')
			}
			writePairJSON(&b, p.T, p.F)
		}
		b.WriteString("]}")
	}
	b.WriteByte(']')
	return b.Bytes(), nil
}

// Scalar is a scalar result.
type Scalar struct {
	T int64
	V float64
}

// Type is "scalar".
func (Scalar) Type() ValueType { return ValueTypeScalar }

// String is `scalar: <v> @[<ms>]`.
func (s Scalar) String() string {
	return "scalar: " + strconv.FormatFloat(s.V, 'f', -1, 64) + " @[" + strconv.FormatInt(s.T, 10) + "]"
}

// MarshalJSON renders `[t,"v"]`.
func (s Scalar) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	writePairJSON(&b, s.T, s.V)
	return b.Bytes(), nil
}

// String is a string result.
type String struct {
	T int64
	V string
}

// Type is "string".
func (String) Type() ValueType { return ValueTypeString }

// String returns the value.
func (s String) String() string { return s.V }

// MarshalJSON renders `[t,"s"]`.
func (s String) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	writeTimestampJSON(&b, s.T)
	b.WriteByte(',')
	b.WriteString(strconv.Quote(s.V))
	b.WriteByte(']')
	return b.Bytes(), nil
}

// writeTimestampJSON writes t/1000 with a 3-digit fraction only when needed
// (the Prometheus API encoder's form).
func writeTimestampJSON(b *bytes.Buffer, t int64) {
	if t < 0 {
		b.WriteByte('-')
		t = -t
	}
	b.WriteString(strconv.FormatInt(t/1000, 10))
	if frac := t % 1000; frac != 0 {
		fmt.Fprintf(b, ".%03d", frac)
	}
}

// FormatValue formats a sample value as the API does: shortest 'f' form,
// exponent form outside [1e-6, 1e21); NaN, +Inf and -Inf spelled out.
func FormatValue(f float64) string {
	format := byte('f')
	if abs := math.Abs(f); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	return strconv.FormatFloat(f, format, -1, 64)
}

func writePairJSON(b *bytes.Buffer, t int64, f float64) {
	b.WriteByte('[')
	writeTimestampJSON(b, t)
	b.WriteString(`,"`)
	b.WriteString(FormatValue(f))
	b.WriteString(`"]`)
}

func writeLabelsJSON(b *bytes.Buffer, ls Labels) {
	b.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(l.Name))
		b.WriteByte(':')
		b.WriteString(jsonString(l.Value))
	}
	b.WriteByte('}')
}

// jsonString quotes s as a JSON string without HTML escaping.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// sortVector orders samples by their label-set string (deterministic output).
func sortVector(v Vector) {
	sort.SliceStable(v, func(i, j int) bool { return v[i].Metric.key() < v[j].Metric.key() })
}

// sortMatrix orders series by their label-set string.
func sortMatrix(m Matrix) {
	sort.SliceStable(m, func(i, j int) bool { return m[i].Metric.key() < m[j].Metric.key() })
}
