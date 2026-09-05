package logql

import (
	"fmt"
	"regexp"
	"strings"

	"divy.dev/internal/promql"
)

// errNonEmpty is Loki's verbatim message for a selector that matches everything.
const errNonEmpty = `queries require at least one regexp or equality matcher that does not have an empty-compatible value. For instance, app=~".*" does not meet this requirement, but app=~".+" will`

const (
	stageExpecting = "expecting |, |=, !=, |~, !~ or end of query"
	errBinaryOps   = "binary operators are only supported between vector() literals"
)

// reservedWords cannot be used as label names.
var reservedWords = map[string]bool{
	"json": true, "and": true, "or": true, "by": true, "without": true,
	"count_over_time": true, "rate": true,
	"sum": true, "count": true, "min": true, "max": true, "avg": true,
	"vector": true,
}

var rangeFns = map[string]bool{"count_over_time": true, "rate": true}

var aggOps = map[string]bool{"sum": true, "count": true, "min": true, "max": true, "avg": true}

var unsupportedFns = map[string]bool{
	"bytes_rate": true, "bytes_over_time": true, "absent_over_time": true,
	"sum_over_time": true, "avg_over_time": true, "min_over_time": true, "max_over_time": true,
	"quantile_over_time": true, "first_over_time": true, "last_over_time": true,
	"stddev_over_time": true, "stdvar_over_time": true,
}

var unsupportedAggs = map[string]bool{
	"topk": true, "bottomk": true, "stddev": true, "stdvar": true, "sort": true, "sort_desc": true,
	"approx_topk": true,
}

var unsupportedParsers = map[string]bool{"logfmt": true, "pattern": true, "regexp": true, "unpack": true}

var unsupportedStages = map[string]bool{
	"line_format": true, "label_format": true, "unwrap": true, "drop": true, "keep": true, "decolorize": true,
}

// Parse parses a LogQL query of the supported subset.
func Parse(input string) (q Query, err error) {
	if strings.TrimSpace(input) == "" {
		return nil, &ParseError{Msg: "syntax error: unexpected $end"}
	}
	p := &parser{lex: newLexer(input)}
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(*ParseError)
			if !ok {
				panic(r)
			}
			q, err = nil, pe
		}
	}()
	p.advance()
	return p.parseQuery(), nil
}

// ParseLogQuery parses a query that must be a log query (a selector with
// optional stages); metric and scalar queries are rejected.
func ParseLogQuery(input string) (*LogQuery, error) {
	q, err := Parse(input)
	if err != nil {
		return nil, err
	}
	lq, ok := q.(*LogQuery)
	if !ok {
		return nil, &ParseError{Msg: "syntax error: expected a log stream selector"}
	}
	return lq, nil
}

// ParseSelector parses a bare stream selector (`{a="b"}`) with no stages.
func ParseSelector(input string) ([]*Matcher, error) {
	lq, err := ParseLogQuery(input)
	if err != nil {
		return nil, err
	}
	if len(lq.Stages) > 0 {
		return nil, &ParseError{Msg: "syntax error: only a stream selector is allowed here"}
	}
	return lq.Selector, nil
}

type parser struct {
	lex *lexer
	cur token
}

func (p *parser) fail(t token, format string, args ...any) {
	panic(&ParseError{Line: t.line, Col: t.col, Msg: fmt.Sprintf(format, args...)})
}

func (p *parser) advance() {
	t, err := p.lex.next()
	if err != nil {
		panic(err)
	}
	p.cur = t
}

func (p *parser) expect(kind tokenKind, what string) token {
	if p.cur.kind != kind {
		p.fail(p.cur, "syntax error: unexpected %s, expecting %s", p.cur.describe(), what)
	}
	t := p.cur
	p.advance()
	return t
}

func (p *parser) isIdent(name string) bool { return p.cur.kind == tokIdent && p.cur.text == name }

func (p *parser) parseQuery() Query {
	if p.cur.kind == tokLBrace {
		q := p.parseLogQuery()
		if p.cur.kind != tokEOF {
			p.fail(p.cur, "syntax error: unexpected %s, %s", p.cur.describe(), stageExpecting)
		}
		return q
	}
	o := p.parseExpr()
	if p.cur.kind != tokEOF {
		p.fail(p.cur, "syntax error: unexpected %s", p.cur.describe())
	}
	if o.metric != nil {
		return o.metric
	}
	return &ScalarQuery{Value: o.scalar}
}

// ---- log queries ----

func (p *parser) parseLogQuery() *LogQuery {
	q := &LogQuery{Selector: p.parseSelector()}
	q.Stages = p.parseStages()
	return q
}

func (p *parser) parseSelector() []*Matcher {
	p.expect(tokLBrace, "{")
	var ms []*Matcher
	if p.cur.kind == tokRBrace {
		p.advance()
	} else {
		for {
			if p.cur.kind != tokIdent {
				p.fail(p.cur, "syntax error: unexpected %s", p.cur.describe())
			}
			name := p.cur
			if reservedWords[name.text] {
				p.fail(name, "reserved word %q cannot be a label name", name.text)
			}
			p.advance()
			var op MatchOp
			switch p.cur.kind {
			case tokEq:
				op = OpEq
			case tokNeq:
				op = OpNeq
			case tokRe:
				op = OpRe
			case tokNre:
				op = OpNre
			default:
				p.fail(p.cur, "syntax error: unexpected %s, expecting =, !=, =~ or !~", p.cur.describe())
			}
			p.advance()
			val := p.expect(tokString, "STRING")
			m, err := NewMatcher(name.text, op, val.val)
			if err != nil {
				p.fail(val, "%v", err)
			}
			ms = append(ms, m)
			if p.cur.kind == tokComma {
				p.advance()
				continue
			}
			if p.cur.kind == tokRBrace {
				p.advance()
				break
			}
			if p.cur.kind == tokEOF {
				p.fail(p.cur, "syntax error: unexpected $end, expecting }")
			}
			p.fail(p.cur, "syntax error: unexpected %s, expecting , or }", p.cur.describe())
		}
	}
	nonEmpty := false
	for _, m := range ms {
		if !m.Matches("") {
			nonEmpty = true
			break
		}
	}
	if !nonEmpty {
		panic(&ParseError{Msg: errNonEmpty})
	}
	return ms
}

// parseStages consumes pipeline stages; the first token that starts no stage
// (`[` inside a range aggregation, `)`, `$end`, …) ends the pipeline and the
// caller decides whether it is legal there.
func (p *parser) parseStages() []Stage {
	var stages []Stage
	for {
		switch p.cur.kind {
		case tokPipeEq, tokNeq, tokPipeRe, tokNre:
			opTok := p.cur
			p.advance()
			val := p.expect(tokString, "STRING")
			f := &LineFilter{Text: val.val}
			switch opTok.kind {
			case tokPipeEq:
				f.Op = LineContains
			case tokNeq:
				f.Op = LineNotContains
			case tokPipeRe:
				f.Op = LineMatches
			case tokNre:
				f.Op = LineNotMatches
			}
			if f.Op == LineMatches || f.Op == LineNotMatches {
				re, err := regexp.Compile(val.val)
				if err != nil {
					p.fail(val, "%v", err)
				}
				f.re = re
			}
			stages = append(stages, f)
		case tokPipe:
			p.advance()
			switch p.cur.kind {
			case tokIdent:
				name := p.cur
				switch {
				case name.text == "json":
					p.advance()
					if p.cur.kind == tokIdent || p.cur.kind == tokString {
						p.fail(p.cur, "json parser takes no arguments")
					}
					stages = append(stages, &JSONParser{})
				case unsupportedParsers[name.text]:
					p.fail(name, "unsupported parser %q (supported: json)", name.text)
				case unsupportedStages[name.text]:
					p.fail(name, "unsupported stage %q", name.text)
				case reservedWords[name.text]:
					p.fail(name, "reserved word %q cannot be a label name", name.text)
				default:
					stages = append(stages, &LabelFilter{Expr: p.parseLFOr()})
				}
			case tokLParen:
				stages = append(stages, &LabelFilter{Expr: p.parseLFOr()})
			default:
				p.fail(p.cur, "syntax error: unexpected %s", p.cur.describe())
			}
		default:
			return stages
		}
	}
}

func (p *parser) parseLFOr() LFExpr {
	l := p.parseLFAnd()
	for p.isIdent("or") {
		p.advance()
		r := p.parseLFAnd()
		l = &LFOr{L: l, R: r}
	}
	return l
}

func (p *parser) parseLFAnd() LFExpr {
	l := p.parseLFAtom()
	for p.isIdent("and") || p.cur.kind == tokComma {
		p.advance()
		r := p.parseLFAtom()
		l = &LFAnd{L: l, R: r}
	}
	return l
}

func (p *parser) parseLFAtom() LFExpr {
	if p.cur.kind == tokLParen {
		p.advance()
		e := p.parseLFOr()
		p.expect(tokRParen, ")")
		return e
	}
	if p.cur.kind != tokIdent {
		p.fail(p.cur, "syntax error: unexpected %s, expecting IDENTIFIER", p.cur.describe())
	}
	name := p.cur
	if reservedWords[name.text] {
		p.fail(name, "reserved word %q cannot be a label name", name.text)
	}
	p.advance()
	opTok := p.cur
	switch opTok.kind {
	case tokEq, tokNeq, tokRe, tokNre, tokCmpEq, tokGt, tokGte, tokLt, tokLte:
	default:
		p.fail(opTok, "syntax error: unexpected %s, expecting =, !=, =~, !~, ==, >, >=, < or <=", opTok.describe())
	}
	p.advance()
	val := p.cur
	isNum := val.kind == tokNumber || val.kind == tokDuration || val.kind == tokBytes
	switch opTok.kind {
	case tokEq, tokRe, tokNre:
		if val.kind != tokString {
			p.fail(val, "syntax error: unexpected %s, expecting STRING", val.describe())
		}
	case tokNeq:
		if val.kind != tokString && !isNum {
			p.fail(val, "syntax error: unexpected %s, expecting STRING, NUMBER, DURATION or BYTES", val.describe())
		}
	default:
		if val.kind == tokString {
			p.fail(val, "numeric comparison needs a number, duration or bytes literal, got string")
		}
		if !isNum {
			p.fail(val, "syntax error: unexpected %s, expecting NUMBER, DURATION or BYTES", val.describe())
		}
	}
	p.advance()
	if val.kind == tokString {
		var op MatchOp
		switch opTok.kind {
		case tokEq:
			op = OpEq
		case tokNeq:
			op = OpNeq
		case tokRe:
			op = OpRe
		case tokNre:
			op = OpNre
		}
		f := &LFString{Name: name.text, Op: op, Value: val.val}
		if op == OpRe || op == OpNre {
			re, err := regexp.Compile("^(?s:" + val.val + ")$")
			if err != nil {
				p.fail(val, "%v", err)
			}
			f.re = re
		}
		return f
	}
	f := &LFNumber{Name: name.text, Value: val.num, Text: val.text}
	switch val.kind {
	case tokDuration:
		f.Kind = NumDuration
	case tokBytes:
		f.Kind = NumBytes
	}
	switch opTok.kind {
	case tokCmpEq:
		f.Op = CmpEq
	case tokNeq:
		f.Op = CmpNeq
	case tokGt:
		f.Op = CmpGt
	case tokGte:
		f.Op = CmpGte
	case tokLt:
		f.Op = CmpLt
	case tokLte:
		f.Op = CmpLte
	}
	return f
}

// ---- metric and scalar queries ----

// operand is either a folded scalar or a metric query.
type operand struct {
	scalar float64
	metric Query
}

func (p *parser) parseExpr() operand {
	left := p.parseTerm()
	for p.cur.kind == tokPlus || p.cur.kind == tokMinus {
		op := p.cur
		if left.metric != nil {
			p.fail(op, "%s", errBinaryOps)
		}
		p.advance()
		right := p.parseTerm()
		if right.metric != nil {
			p.fail(op, "%s", errBinaryOps)
		}
		if op.kind == tokPlus {
			left.scalar += right.scalar
		} else {
			left.scalar -= right.scalar
		}
	}
	return left
}

func (p *parser) parseTerm() operand {
	left := p.parseAtom()
	for p.cur.kind == tokMul || p.cur.kind == tokDiv {
		op := p.cur
		if left.metric != nil {
			p.fail(op, "%s", errBinaryOps)
		}
		p.advance()
		right := p.parseAtom()
		if right.metric != nil {
			p.fail(op, "%s", errBinaryOps)
		}
		if op.kind == tokMul {
			left.scalar *= right.scalar
		} else {
			left.scalar /= right.scalar
		}
	}
	return left
}

func (p *parser) parseAtom() operand {
	switch p.cur.kind {
	case tokLParen:
		p.advance()
		o := p.parseExpr()
		p.expect(tokRParen, ")")
		return o
	case tokNumber:
		p.fail(p.cur, "%s", errBinaryOps)
	case tokIdent:
		name := p.cur
		switch {
		case name.text == "vector":
			p.advance()
			p.expect(tokLParen, "(")
			n := p.expect(tokNumber, "NUMBER")
			p.expect(tokRParen, ")")
			return operand{scalar: n.num}
		case rangeFns[name.text]:
			return operand{metric: &MetricQuery{Range: p.parseRangeAgg()}}
		case aggOps[name.text]:
			return operand{metric: p.parseAggregation()}
		case unsupportedFns[name.text]:
			p.fail(name, "unsupported function %q (supported: count_over_time, rate)", name.text)
		case unsupportedAggs[name.text]:
			p.fail(name, "unsupported aggregation %q (supported: sum, count, min, max, avg)", name.text)
		default:
			p.fail(name, "syntax error: unexpected %s", name.describe())
		}
	case tokLBrace:
		p.fail(p.cur, "syntax error: unexpected {")
	}
	p.fail(p.cur, "syntax error: unexpected %s", p.cur.describe())
	return operand{}
}

func (p *parser) parseRangeAgg() *RangeAgg {
	fn := p.cur
	p.advance()
	p.expect(tokLParen, "(")
	if p.cur.kind != tokLBrace {
		p.fail(p.cur, "syntax error: unexpected %s, expecting {", p.cur.describe())
	}
	log := p.parseLogQuery()
	p.expect(tokLBracket, "[")
	if p.cur.kind != tokDuration {
		p.fail(p.cur, "syntax error: unexpected %s, expecting DURATION", p.cur.describe())
	}
	d := p.cur
	p.advance()
	p.expect(tokRBracket, "]")
	log.Stages = append(log.Stages, p.parseStages()...)
	if p.isIdent("offset") {
		p.fail(p.cur, "offset modifier is not supported")
	}
	p.expect(tokRParen, ")")
	dur, err := promql.ParseDuration(d.text)
	if err != nil {
		p.fail(d, "%v", err)
	}
	return &RangeAgg{Fn: fn.text, Log: log, Range: dur, RangeText: d.text}
}

func (p *parser) parseAggregation() *MetricQuery {
	op := p.cur
	p.advance()
	agg := &Aggregation{Op: op.text}
	if p.isIdent("by") || p.isIdent("without") {
		p.parseGrouping(agg)
	}
	p.expect(tokLParen, "(")
	if p.cur.kind != tokIdent {
		p.fail(p.cur, "syntax error: unexpected %s, expecting count_over_time or rate", p.cur.describe())
	}
	inner := p.cur
	var r *RangeAgg
	switch {
	case rangeFns[inner.text]:
		r = p.parseRangeAgg()
	case unsupportedFns[inner.text]:
		p.fail(inner, "unsupported function %q (supported: count_over_time, rate)", inner.text)
	case unsupportedAggs[inner.text]:
		p.fail(inner, "unsupported aggregation %q (supported: sum, count, min, max, avg)", inner.text)
	default:
		p.fail(inner, "syntax error: unexpected %s, expecting count_over_time or rate", inner.describe())
	}
	p.expect(tokRParen, ")")
	if p.isIdent("by") || p.isIdent("without") {
		if agg.Grouping {
			p.fail(p.cur, "duplicate grouping")
		}
		p.parseGrouping(agg)
	}
	return &MetricQuery{Agg: agg, Range: r}
}

func (p *parser) parseGrouping(agg *Aggregation) {
	agg.Grouping = true
	agg.Without = p.cur.text == "without"
	agg.Labels = []string{}
	p.advance()
	p.expect(tokLParen, "(")
	for p.cur.kind != tokRParen {
		if p.cur.kind != tokIdent {
			p.fail(p.cur, "syntax error: unexpected %s, expecting IDENTIFIER", p.cur.describe())
		}
		if reservedWords[p.cur.text] {
			p.fail(p.cur, "reserved word %q cannot be a label name", p.cur.text)
		}
		agg.Labels = append(agg.Labels, p.cur.text)
		p.advance()
		if p.cur.kind == tokComma {
			p.advance()
			if p.cur.kind == tokRParen {
				p.fail(p.cur, "syntax error: unexpected )")
			}
			continue
		}
		if p.cur.kind != tokRParen {
			p.fail(p.cur, "syntax error: unexpected %s, expecting , or )", p.cur.describe())
		}
	}
	p.advance()
}
