package promql

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// ValueType is the type of an expression or result.
type ValueType string

// Value types (the JSON resultType strings of the HTTP API).
const (
	ValueTypeNone   ValueType = "none"
	ValueTypeVector ValueType = "vector"
	ValueTypeScalar ValueType = "scalar"
	ValueTypeMatrix ValueType = "matrix"
	ValueTypeString ValueType = "string"
)

// DocumentedType is the type name used in error messages.
func DocumentedType(t ValueType) string {
	switch t {
	case ValueTypeVector:
		return "instant vector"
	case ValueTypeMatrix:
		return "range vector"
	}
	return string(t)
}

// posRange is a byte range in the input.
type posRange struct {
	Start, End int
}

// Expr is a node of the parsed expression.
type Expr interface {
	Type() ValueType
	String() string
	pos() posRange
}

// NumberLiteral is a float literal (unary signs are folded in).
type NumberLiteral struct {
	Val float64
	pr  posRange
}

// StringLiteral is a string literal.
type StringLiteral struct {
	Val string
	pr  posRange
}

// VectorSelector selects series by name and matchers.
type VectorSelector struct {
	Name     string
	Matchers []*Matcher // includes the __name__ matcher when Name is set
	pr       posRange
}

// MatrixSelector is a vector selector with a range.
type MatrixSelector struct {
	VS     *VectorSelector
	Range  time.Duration
	endPos int
}

// UnaryExpr is + or - applied to an expression.
type UnaryExpr struct {
	Op       tokenType
	Expr     Expr
	startPos int
}

// BinaryExpr is a binary operation with one-to-one vector matching.
type BinaryExpr struct {
	Op         tokenType
	LHS, RHS   Expr
	ReturnBool bool
}

// AggregateExpr is sum/avg/min/max/count with an optional by/without clause.
type AggregateExpr struct {
	Op       tokenType
	Expr     Expr
	Grouping []string
	Without  bool
	pr       posRange
}

// Call is a function call.
type Call struct {
	Func *Function
	Args []Expr
	pr   posRange
}

// ParenExpr is a parenthesised expression.
type ParenExpr struct {
	Expr Expr
	pr   posRange
}

// Type implementations.
func (*NumberLiteral) Type() ValueType  { return ValueTypeScalar }
func (*StringLiteral) Type() ValueType  { return ValueTypeString }
func (*VectorSelector) Type() ValueType { return ValueTypeVector }
func (*MatrixSelector) Type() ValueType { return ValueTypeMatrix }
func (e *UnaryExpr) Type() ValueType    { return e.Expr.Type() }
func (e *BinaryExpr) Type() ValueType {
	if e.LHS.Type() == ValueTypeScalar && e.RHS.Type() == ValueTypeScalar {
		return ValueTypeScalar
	}
	return ValueTypeVector
}
func (*AggregateExpr) Type() ValueType { return ValueTypeVector }
func (e *Call) Type() ValueType        { return e.Func.ReturnType }
func (e *ParenExpr) Type() ValueType   { return e.Expr.Type() }

func (e *NumberLiteral) pos() posRange  { return e.pr }
func (e *StringLiteral) pos() posRange  { return e.pr }
func (e *VectorSelector) pos() posRange { return e.pr }
func (e *MatrixSelector) pos() posRange { return posRange{e.VS.pr.Start, e.endPos} }
func (e *UnaryExpr) pos() posRange      { return posRange{e.startPos, e.Expr.pos().End} }
func (e *BinaryExpr) pos() posRange     { return posRange{e.LHS.pos().Start, e.RHS.pos().End} }
func (e *AggregateExpr) pos() posRange  { return e.pr }
func (e *Call) pos() posRange           { return e.pr }
func (e *ParenExpr) pos() posRange      { return e.pr }

// String prints the canonical Prometheus form.
func (e *NumberLiteral) String() string { return strconv.FormatFloat(e.Val, 'f', -1, 64) }
func (e *StringLiteral) String() string { return strconv.Quote(e.Val) }

func (e *VectorSelector) String() string {
	var ms []string
	for _, m := range e.Matchers {
		if m.Name == MetricName && m.Type == MatchEqual && m.Value == e.Name && m.Value != "" {
			continue
		}
		ms = append(ms, m.String())
	}
	var b strings.Builder
	b.WriteString(e.Name)
	if len(ms) > 0 {
		sort.Strings(ms)
		b.WriteByte('{')
		b.WriteString(strings.Join(ms, ","))
		b.WriteByte('}')
	}
	return b.String()
}

func (e *MatrixSelector) String() string {
	return e.VS.String() + "[" + FormatDuration(e.Range) + "]"
}

func (e *UnaryExpr) String() string { return e.Op.String() + e.Expr.String() }

func (e *BinaryExpr) String() string {
	b := ""
	if e.ReturnBool {
		b = " bool"
	}
	return e.LHS.String() + " " + e.Op.String() + b + " " + e.RHS.String()
}

func (e *AggregateExpr) String() string {
	var b strings.Builder
	b.WriteString(e.Op.String())
	switch {
	case e.Without:
		b.WriteString(" without (")
		b.WriteString(strings.Join(e.Grouping, ", "))
		b.WriteString(") ")
	case len(e.Grouping) > 0:
		b.WriteString(" by (")
		b.WriteString(strings.Join(e.Grouping, ", "))
		b.WriteString(") ")
	}
	b.WriteByte('(')
	b.WriteString(e.Expr.String())
	b.WriteByte(')')
	return b.String()
}

func (e *Call) String() string {
	args := make([]string, len(e.Args))
	for i, a := range e.Args {
		args[i] = a.String()
	}
	return e.Func.Name + "(" + strings.Join(args, ", ") + ")"
}

func (e *ParenExpr) String() string { return "(" + e.Expr.String() + ")" }

// Walk calls f for every node in pre-order; a false return stops descending.
func Walk(e Expr, f func(Expr) bool) {
	if e == nil || !f(e) {
		return
	}
	switch n := e.(type) {
	case *MatrixSelector:
		Walk(n.VS, f)
	case *UnaryExpr:
		Walk(n.Expr, f)
	case *BinaryExpr:
		Walk(n.LHS, f)
		Walk(n.RHS, f)
	case *AggregateExpr:
		Walk(n.Expr, f)
	case *Call:
		for _, a := range n.Args {
			Walk(a, f)
		}
	case *ParenExpr:
		Walk(n.Expr, f)
	}
}
