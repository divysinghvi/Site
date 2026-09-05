package promql

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseError is a parse error rendered like Prometheus:
// `<line>:<col>: parse error: <message>` (`unknown position` for empty input).
type ParseError struct {
	Pos   int // 0-based byte offset of the offending token
	Query string
	Err   error
}

// Error implements error.
func (e *ParseError) Error() string {
	return e.position() + ": parse error: " + e.Err.Error()
}

// Unwrap returns the underlying message error.
func (e *ParseError) Unwrap() error { return e.Err }

func (e *ParseError) position() string {
	if e.Query == "" {
		return "unknown position"
	}
	if e.Pos < 0 || e.Pos > len(e.Query) {
		return "invalid position"
	}
	lastLineBreak := -1
	line := 1
	for i, c := range e.Query[:e.Pos] {
		if c == '\n' {
			lastLineBreak = i
			line++
		}
	}
	return fmt.Sprintf("%d:%d", line, e.Pos-lastLineBreak)
}

// ParseExpr parses and type-checks an expression.
func ParseExpr(input string) (Expr, error) {
	p := newParser(input)
	expr := p.parseTop()
	if p.err != nil {
		return nil, p.err
	}
	p.checkAST(expr)
	if p.err != nil {
		return nil, p.err
	}
	if len(p.unsupported) > 0 {
		return nil, p.unsupported[0]
	}
	return expr, nil
}

// Check reports the first parse error of an expression (nil when it parses).
func Check(input string) error {
	_, err := ParseExpr(input)
	return err
}

// ParseMetricSelector parses a `match[]` selector into matchers (no type
// check; the API layer rejects selectors without a non-empty matcher).
func ParseMetricSelector(input string) ([]*Matcher, error) {
	p := newParser(input)
	var vs *VectorSelector
	tok := p.peek()
	switch {
	case tok.typ == tError:
		p.lexError(tok)
	case tok.typ == tLeftBrace:
		vs = p.parseSelector("", 0)
	case tok.typ == tIdentifier || tok.typ == tMetricIdentifier || tok.typ.isAggregator() || isMetricKeyword(tok.typ):
		p.next()
		vs = p.parseSelector(tok.val, tok.pos)
	default:
		p.unexpected(tok, "", "")
	}
	if p.err == nil {
		if t := p.peek(); t.typ == tError {
			p.lexError(t)
		} else if t.typ != tEOF {
			p.unexpected(t, "", "")
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return vs.Matchers, nil
}

// precedence of binary operators (Prometheus: LOR < LAND/LUNLESS < comparisons < ADD/SUB < MUL/DIV/MOD/ATAN2 < POW).
func precedence(t tokenType) int {
	switch t {
	case tLor:
		return 1
	case tLand, tLunless:
		return 2
	case tEqlc, tNeq, tLss, tLte, tGtr, tGte:
		return 3
	case tAdd, tSub:
		return 4
	case tMul, tDiv, tMod, tAtan2:
		return 5
	case tPow:
		return 6
	}
	return 0
}

const precPow = 6

type parser struct {
	input       string
	toks        []token
	i           int
	lastClosing int
	err         *ParseError
	// unsupported holds errors for constructs Prometheus accepts but this
	// subset does not; they are reported after the Prometheus-style checks.
	unsupported []*ParseError
}

func newParser(input string) *parser {
	toks := lex(input)
	kept := toks[:0]
	for _, t := range toks {
		if t.typ != tComment {
			kept = append(kept, t)
		}
	}
	return &parser{input: input, toks: kept}
}

func (p *parser) peek() token {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) next() token {
	t := p.peek()
	if p.i < len(p.toks) {
		p.i++
	}
	switch t.typ {
	case tRightBrace, tRightParen, tRightBracket, tDuration, tNumber:
		p.lastClosing = t.pos + len(t.val)
	}
	return t
}

func (p *parser) fail(pos int, format string, args ...any) {
	if p.err == nil {
		p.err = &ParseError{Pos: pos, Query: p.input, Err: fmt.Errorf(format, args...)}
	}
}

func (p *parser) failErr(pos int, err error) {
	if p.err == nil {
		p.err = &ParseError{Pos: pos, Query: p.input, Err: err}
	}
}

func (p *parser) lexError(t token) { p.failErr(t.pos, errors.New(t.val)) }

// unexpected composes Prometheus' `unexpected <desc>[ in <context>][, expected <what>]`.
func (p *parser) unexpected(t token, context, expected string) {
	if t.typ == tError {
		p.lexError(t)
		return
	}
	var b strings.Builder
	b.WriteString("unexpected ")
	b.WriteString(t.desc())
	if context != "" {
		b.WriteString(" in ")
		b.WriteString(context)
	}
	if expected != "" {
		b.WriteString(", expected ")
		b.WriteString(expected)
	}
	p.failErr(t.pos, errors.New(b.String()))
}

func (p *parser) notSupported(pos int, format string, args ...any) {
	p.unsupported = append(p.unsupported, &ParseError{Pos: pos, Query: p.input, Err: fmt.Errorf(format, args...)})
}

// parseTop parses a whole expression followed by EOF.
func (p *parser) parseTop() Expr {
	if t := p.peek(); t.typ == tEOF {
		p.failErr(0, errors.New("no expression found in input"))
		return nil
	}
	e := p.parseBinary(0)
	if p.err != nil {
		return nil
	}
	if t := p.peek(); t.typ != tEOF {
		p.unexpected(t, "", "")
		return nil
	}
	return e
}

// parseBinary is precedence climbing over the binary operators.
func (p *parser) parseBinary(minPrec int) Expr {
	lhs := p.parseUnary()
	for p.err == nil {
		t := p.peek()
		switch t.typ {
		case tError:
			p.lexError(t)
			return nil
		case tOffset:
			p.next()
			p.parseOffsetArg()
			p.notSupported(t.pos, "offset modifier is not supported")
			continue
		case tAt:
			p.next()
			p.parseAtArg()
			p.notSupported(t.pos, "@ modifier is not supported")
			continue
		case tAnchored, tSmoothed:
			// `anchored`/`smoothed` are experimental Prometheus modifiers; here they are plain identifiers.
			p.unexpected(token{tIdentifier, t.pos, t.val}, "", "")
			return nil
		}
		prec := precedence(t.typ)
		if prec == 0 || prec < minPrec {
			break
		}
		p.next()
		be := &BinaryExpr{Op: t.typ, LHS: lhs}
		p.parseBinModifiers(be, t)
		if p.err != nil {
			return nil
		}
		next := prec + 1
		if t.typ == tPow {
			next = prec
		}
		be.RHS = p.parseBinary(next)
		if p.err != nil {
			return nil
		}
		lhs = be
	}
	return lhs
}

// parseBinModifiers consumes bool / on / ignoring / group_left / group_right after an operator.
func (p *parser) parseBinModifiers(be *BinaryExpr, op token) {
	if p.peek().typ == tBool {
		p.next()
		be.ReturnBool = true
	}
	t := p.peek()
	switch t.typ {
	case tOn, tIgnoring:
		p.next()
		p.parseGroupingLabels()
		p.notSupported(t.pos, "vector matching modifier %q is not supported", t.val)
		if g := p.peek(); g.typ == tGroupLeft || g.typ == tGroupRight {
			p.next()
			if p.peek().typ == tLeftParen {
				p.parseGroupingLabels()
			}
		}
	case tGroupLeft, tGroupRight:
		p.next()
		if p.peek().typ == tLeftParen {
			p.parseGroupingLabels()
		}
		p.notSupported(t.pos, "vector matching modifier %q is not supported", t.val)
	}
	if t := p.peek(); t.typ == tFill || t.typ == tFillLeft || t.typ == tFillRight {
		p.unexpected(t, "", "")
	}
	_ = op
}

func (p *parser) parseOffsetArg() {
	t := p.peek()
	switch t.typ {
	case tDuration, tNumber:
		p.next()
	case tSub, tAdd:
		p.next()
		if n := p.peek(); n.typ == tDuration || n.typ == tNumber {
			p.next()
		} else {
			p.unexpected(n, "offset", "number, duration, step(), or range()")
		}
	default:
		p.unexpected(t, "offset", "number, duration, step(), or range()")
	}
}

func (p *parser) parseAtArg() {
	t := p.peek()
	switch t.typ {
	case tNumber:
		p.next()
	case tSub, tAdd:
		p.next()
		if n := p.peek(); n.typ == tNumber {
			p.next()
		} else {
			p.unexpected(n, "@", "timestamp")
		}
	case tStart, tEnd:
		p.next()
		if l := p.peek(); l.typ != tLeftParen {
			p.unexpected(l, "@", "timestamp")
			return
		}
		p.next()
		if r := p.peek(); r.typ != tRightParen {
			p.unexpected(r, "@", "timestamp")
			return
		}
		p.next()
	default:
		p.unexpected(t, "@", "timestamp")
	}
}

func (p *parser) parseUnary() Expr {
	t := p.peek()
	if t.typ == tAdd || t.typ == tSub {
		p.next()
		operand := p.parseBinary(precPow)
		if p.err != nil {
			return nil
		}
		if nl, ok := operand.(*NumberLiteral); ok {
			if t.typ == tSub {
				nl.Val *= -1
			}
			nl.pr.Start = t.pos
			return nl
		}
		return &UnaryExpr{Op: t.typ, Expr: operand, startPos: t.pos}
	}
	return p.parsePostfix(p.parsePrimary())
}

// parsePostfix handles range selectors and subqueries after a primary.
func (p *parser) parsePostfix(e Expr) Expr {
	if p.err != nil {
		return nil
	}
	for p.peek().typ == tLeftBracket {
		lb := p.next()
		rng, rngPos := p.parseRangeDuration()
		if p.err != nil {
			return nil
		}
		t := p.peek()
		if t.typ == tColon {
			// subquery: consume the optional step and the closing bracket
			p.next()
			if s := p.peek(); s.typ == tDuration || s.typ == tNumber {
				p.next()
			}
			if r := p.peek(); r.typ != tRightBracket {
				p.unexpected(r, "subquery selector", `"]"`)
				return nil
			}
			p.notSupported(t.pos, "subqueries are not supported")
		} else if t.typ != tRightBracket {
			if t.typ.isOperator() {
				p.unexpected(t, "range selector", `"]"`)
			} else {
				p.unexpected(t, "subquery or range", `":" or "]"`)
			}
			return nil
		}
		rb := p.next()
		vs, ok := e.(*VectorSelector)
		if !ok {
			p.fail(lb.pos, "ranges only allowed for vector selectors")
			return nil
		}
		if rng <= 0 {
			p.fail(rngPos, "duration must be greater than 0")
			return nil
		}
		e = &MatrixSelector{VS: vs, Range: rng, endPos: rb.pos + 1}
	}
	return e
}

// parseRangeDuration parses the duration inside [ ]: a duration literal or a number of seconds.
func (p *parser) parseRangeDuration() (time.Duration, int) {
	t := p.peek()
	switch t.typ {
	case tError:
		p.lexError(t)
		return 0, t.pos
	case tDuration:
		p.next()
		d, err := ParseDuration(t.val)
		if err != nil {
			p.failErr(t.pos, err)
			return 0, t.pos
		}
		return d, t.pos
	case tNumber:
		p.next()
		return time.Duration(math.Round(p.number(t) * float64(time.Second))), t.pos
	case tSub, tAdd:
		p.next()
		n := p.peek()
		if n.typ != tDuration && n.typ != tNumber {
			p.unexpected(n, "subquery or range selector", "number, duration, step(), or range()")
			return 0, t.pos
		}
		d, _ := p.parseRangeDuration()
		if t.typ == tSub {
			d = -d
		}
		return d, t.pos
	}
	p.unexpected(t, "subquery or range selector", "number, duration, step(), or range()")
	return 0, t.pos
}

func isMetricKeyword(t tokenType) bool {
	switch t {
	case tBy, tWithout, tOffset, tLand, tLor, tLunless, tStart, tEnd, tStep, tRange, tAnchored, tSmoothed, tMaxOf, tMinOf, tFill, tFillLeft, tFillRight:
		return true
	}
	return false
}

func (p *parser) parsePrimary() Expr {
	t := p.peek()
	switch {
	case t.typ == tError:
		p.lexError(t)
		return nil
	case t.typ == tNumber:
		p.next()
		return &NumberLiteral{Val: p.number(t), pr: posRange{t.pos, t.pos + len(t.val)}}
	case t.typ == tString:
		p.next()
		return &StringLiteral{Val: p.unquote(t), pr: posRange{t.pos, t.pos + len(t.val)}}
	case t.typ == tLeftParen:
		p.next()
		inner := p.parseBinary(0)
		if p.err != nil {
			return nil
		}
		r := p.peek()
		if r.typ != tRightParen {
			p.unexpected(r, "", "")
			return nil
		}
		p.next()
		return &ParenExpr{Expr: inner, pr: posRange{t.pos, r.pos + 1}}
	case t.typ == tLeftBrace:
		return p.parseSelector("", t.pos)
	case t.typ.isAggregator():
		return p.parseAggregate()
	case t.typ == tIdentifier:
		p.next()
		if p.peek().typ == tLeftParen {
			return p.parseCall(t)
		}
		return p.parseSelector(t.val, t.pos)
	case t.typ == tMetricIdentifier || isMetricKeyword(t.typ):
		p.next()
		return p.parseSelector(t.val, t.pos)
	}
	p.unexpected(t, "", "")
	return nil
}

// parseSelector parses the optional {matchers} after a metric name (name may be "").
func (p *parser) parseSelector(name string, start int) *VectorSelector {
	vs := &VectorSelector{Name: name, pr: posRange{start, start + len(name)}}
	if p.peek().typ == tLeftBrace {
		p.next()
		end := p.parseMatchers(vs)
		if p.err != nil {
			return nil
		}
		vs.pr.End = end
	}
	if name != "" {
		m, _ := NewMatcher(MatchEqual, MetricName, name)
		vs.Matchers = append(vs.Matchers, m)
	}
	return vs
}

// parseMatchers parses `label op "value", …}` after the opening brace; it returns the end position.
func (p *parser) parseMatchers(vs *VectorSelector) int {
	for {
		t := p.peek()
		switch t.typ {
		case tError:
			p.lexError(t)
			return 0
		case tRightBrace:
			p.next()
			return t.pos + 1
		case tString:
			p.fail(t.pos, "unexpected string %q in label matching, expected identifier or \"}\"", p.unquote(t))
			return 0
		case tIdentifier:
			p.next()
			op := p.peek()
			var mt MatchType
			switch op.typ {
			case tEql:
				mt = MatchEqual
			case tNeq:
				mt = MatchNotEqual
			case tEqlRegex:
				mt = MatchRegexp
			case tNeqRegex:
				mt = MatchNotRegexp
			default:
				p.unexpected(op, "label matching", "label matching operator")
				return 0
			}
			p.next()
			val := p.peek()
			if val.typ != tString {
				p.unexpected(val, "label matching", "string")
				return 0
			}
			p.next()
			m, err := NewMatcher(mt, t.val, p.unquote(val))
			if err != nil {
				p.failErr(t.pos, err)
				return 0
			}
			vs.Matchers = append(vs.Matchers, m)
			sep := p.peek()
			switch sep.typ {
			case tComma:
				p.next()
			case tRightBrace:
			default:
				p.unexpected(sep, "label matching", `"," or "}"`)
				return 0
			}
		default:
			p.unexpected(t, "label matching", `identifier or "}"`)
			return 0
		}
	}
}

func (p *parser) parseAggregate() Expr {
	op := p.next()
	next := p.peek()
	if next.typ != tLeftParen && next.typ != tBy && next.typ != tWithout {
		// an aggregator keyword used as a metric name (`sum{}`, `sum + 1`)
		return p.parseSelector(op.val, op.pos)
	}
	switch op.typ {
	case tSum, tAvg, tMin, tMax, tCount:
	default:
		p.fail(op.pos, "aggregation operator %q is not supported", strings.ToLower(op.val))
		return nil
	}
	ae := &AggregateExpr{Op: op.typ, pr: posRange{Start: op.pos}}
	modifierSeen := false
	if next.typ == tBy || next.typ == tWithout {
		p.next()
		ae.Without = next.typ == tWithout
		ae.Grouping = p.parseGroupingLabels()
		if p.err != nil {
			return nil
		}
		modifierSeen = true
		if l := p.peek(); l.typ != tLeftParen {
			p.unexpected(l, "aggregation", "")
			return nil
		}
	}
	args := p.parseCallArgs()
	if p.err != nil {
		return nil
	}
	ae.pr.End = p.lastClosing
	if !modifierSeen {
		if m := p.peek(); m.typ == tBy || m.typ == tWithout {
			p.next()
			ae.Without = m.typ == tWithout
			ae.Grouping = p.parseGroupingLabels()
			if p.err != nil {
				return nil
			}
		}
	}
	if len(args) == 0 {
		p.fail(ae.pr.Start, "no arguments for aggregate expression provided")
		return nil
	}
	if len(args) != 1 {
		p.fail(ae.pr.Start, "wrong number of arguments for aggregate expression provided, expected 1, got %d", len(args))
		return nil
	}
	ae.Expr = args[0]
	return ae
}

// parseGroupingLabels parses `(label, …)` after by/without/on/ignoring.
func (p *parser) parseGroupingLabels() []string {
	t := p.peek()
	if t.typ != tLeftParen {
		p.unexpected(t, "grouping opts", `"("`)
		return nil
	}
	p.next()
	labels := []string{}
	for {
		l := p.peek()
		switch {
		case l.typ == tError:
			p.lexError(l)
			return nil
		case l.typ == tRightParen && len(labels) == 0:
			p.next()
			return labels
		case l.typ == tIdentifier || l.typ == tMetricIdentifier || l.typ.isAggregator() || l.typ.isKeyword() || l.typ == tAtan2 || l.typ == tLand || l.typ == tLor || l.typ == tLunless:
			p.next()
			labels = append(labels, l.val)
		default:
			p.unexpected(l, "grouping opts", "label")
			return nil
		}
		sep := p.peek()
		switch sep.typ {
		case tComma:
			p.next()
			if r := p.peek(); r.typ == tRightParen {
				p.next()
				return labels
			}
		case tRightParen:
			p.next()
			return labels
		default:
			p.unexpected(sep, "grouping opts", `"," or ")"`)
			return nil
		}
	}
}

func (p *parser) parseCall(name token) Expr {
	args := p.parseCallArgs()
	if p.err != nil {
		return nil
	}
	fn, ok := Functions[name.val]
	if !ok {
		if prometheusFunctions[name.val] {
			p.fail(name.pos, "function %q is not supported", name.val)
		} else {
			p.fail(name.pos, "unknown function with name %q", name.val)
		}
		return nil
	}
	return &Call{Func: fn, Args: args, pr: posRange{name.pos, p.lastClosing}}
}

// parseCallArgs parses `( expr, … )`.
func (p *parser) parseCallArgs() []Expr {
	if t := p.peek(); t.typ != tLeftParen {
		p.unexpected(t, "", "")
		return nil
	}
	p.next()
	args := []Expr{}
	if p.peek().typ == tRightParen {
		p.next()
		return args
	}
	for {
		e := p.parseBinary(0)
		if p.err != nil {
			return nil
		}
		args = append(args, e)
		t := p.peek()
		switch t.typ {
		case tComma:
			p.next()
			if r := p.peek(); r.typ == tRightParen {
				p.fail(t.pos, "trailing commas not allowed in function call args")
				return nil
			}
		case tRightParen:
			p.next()
			return args
		default:
			p.unexpected(t, "", "")
			return nil
		}
	}
}

func (p *parser) number(t token) float64 {
	n, err := strconv.ParseInt(t.val, 0, 64)
	f := float64(n)
	if err != nil {
		f, err = strconv.ParseFloat(t.val, 64)
	}
	if err != nil {
		p.fail(t.pos, "error parsing number: %s", err)
	}
	return f
}

// unquote decodes a string token (double, single or backtick quoted).
func (p *parser) unquote(t token) string {
	s := t.val
	if len(s) < 2 {
		return s
	}
	q := s[0]
	body := s[1 : len(s)-1]
	switch q {
	case '`':
		return body
	case '"':
		out, err := strconv.Unquote(s)
		if err != nil {
			p.fail(t.pos, "error unquoting string %q: %s", s, err)
		}
		return out
	case '\'':
		// Go has no single-quoted strings: escape the double quotes, unescape the single ones.
		var b strings.Builder
		b.WriteByte('"')
		for i := 0; i < len(body); i++ {
			c := body[i]
			switch {
			case c == '\\' && i+1 < len(body):
				if body[i+1] == '\'' {
					b.WriteByte('\'')
				} else {
					b.WriteByte(c)
					b.WriteByte(body[i+1])
				}
				i++
			case c == '"':
				b.WriteString(`\"`)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('"')
		out, err := strconv.Unquote(b.String())
		if err != nil {
			p.fail(t.pos, "error unquoting string %q: %s", s, err)
		}
		return out
	}
	return s
}

// ---- static checks (Prometheus checkAST plus the subset's own rejections) ----

func (p *parser) checkAST(e Expr) ValueType {
	if e == nil {
		return ValueTypeNone
	}
	typ := e.Type()
	switch n := e.(type) {
	case *AggregateExpr:
		p.expectType(n.Expr, ValueTypeVector, "aggregation expression")
	case *BinaryExpr:
		lt := p.checkAST(n.LHS)
		rt := p.checkAST(n.RHS)
		opRange := func() int {
			r := n.LHS.pos().End
			for r < len(p.input) && isSpace(rune(p.input[r])) {
				r++
			}
			return r
		}
		if n.ReturnBool && !n.Op.isComparison() {
			p.fail(opRange(), "bool modifier can only be used on comparison operators")
		}
		if n.Op.isComparison() && !n.ReturnBool && rt == ValueTypeScalar && lt == ValueTypeScalar {
			p.fail(opRange(), "comparisons between scalars must use BOOL modifier")
		}
		if lt != ValueTypeScalar && lt != ValueTypeVector {
			p.fail(n.LHS.pos().Start, "binary expression must contain only scalar and instant vector types")
		}
		if rt != ValueTypeScalar && rt != ValueTypeVector {
			p.fail(n.RHS.pos().Start, "binary expression must contain only scalar and instant vector types")
		}
		if (lt == ValueTypeScalar || rt == ValueTypeScalar) && n.Op.isSetOperator() {
			p.fail(n.pos().Start, "set operator %q not allowed in binary scalar expression", n.Op.String())
		}
		if n.Op.isSetOperator() {
			p.notSupported(opRange(), "set operator %q is not supported", n.Op.String())
		}
		if n.Op == tAtan2 {
			p.notSupported(opRange(), "binary operator %q is not supported", n.Op.String())
		}
	case *Call:
		nargs := len(n.Func.ArgTypes)
		if n.Func.Variadic == 0 {
			if nargs != len(n.Args) {
				p.fail(n.pr.Start, "expected %d argument(s) in call to %q, got %d", nargs, n.Func.Name, len(n.Args))
			}
		} else {
			na := nargs - 1
			if na > len(n.Args) {
				p.fail(n.pr.Start, "expected at least %d argument(s) in call to %q, got %d", na, n.Func.Name, len(n.Args))
			} else if nargsmax := na + n.Func.Variadic; nargsmax < len(n.Args) {
				p.fail(n.pr.Start, "expected at most %d argument(s) in call to %q, got %d", nargsmax, n.Func.Name, len(n.Args))
			}
		}
		for i, arg := range n.Args {
			if i >= len(n.Func.ArgTypes) {
				if n.Func.Variadic == 0 {
					break
				}
				i = len(n.Func.ArgTypes) - 1
			}
			if t := p.checkAST(arg); t != n.Func.ArgTypes[i] {
				p.fail(arg.pos().Start, "expected type %s in call to function %q, got %s", DocumentedType(n.Func.ArgTypes[i]), n.Func.Name, DocumentedType(t))
			}
		}
	case *ParenExpr:
		p.checkAST(n.Expr)
	case *UnaryExpr:
		if t := p.checkAST(n.Expr); t != ValueTypeScalar && t != ValueTypeVector {
			p.fail(n.pos().Start, "unary expression only allowed on expressions of type scalar or instant vector, got %q", DocumentedType(t))
		}
	case *MatrixSelector:
		p.checkAST(n.VS)
	case *VectorSelector:
		if n.Name != "" {
			for _, m := range n.Matchers[:len(n.Matchers)-1] {
				if m.Name == MetricName {
					p.fail(n.pr.Start, "metric name must not be set twice: %q or %q", n.Name, m.Value)
				}
			}
			break
		}
		notEmpty := false
		for _, m := range n.Matchers {
			if !m.Matches("") {
				notEmpty = true
				break
			}
		}
		if !notEmpty {
			p.fail(n.pr.Start, "vector selector must contain at least one non-empty matcher")
		}
	case *NumberLiteral, *StringLiteral:
	}
	return typ
}

func (p *parser) expectType(e Expr, want ValueType, context string) {
	t := p.checkAST(e)
	if t != want {
		p.fail(e.pos().Start, "expected type %s in %s, got %s", DocumentedType(want), context, DocumentedType(t))
	}
}
