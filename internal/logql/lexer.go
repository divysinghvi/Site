// Package logql implements the LogQL subset served under /loki/api/v1:
// stream selectors, line filters, `| json`, label filters, the
// count_over_time/rate range aggregations with sum/count/min/max/avg
// grouping, and vector() arithmetic for Grafana's health check. Everything
// is documented in docs/logql-subset.md; the parser and evaluator tests pin
// every rule.
package logql

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"

	"divy.dev/internal/promql"
)

// tokenKind is the lexical class of a token.
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokDuration
	tokBytes
	tokLBrace   // {
	tokRBrace   // }
	tokLParen   // (
	tokRParen   // )
	tokLBracket // [
	tokRBracket // ]
	tokComma    // ,
	tokPipe     // |
	tokEq       // =
	tokNeq      // !=
	tokRe       // =~
	tokNre      // !~
	tokPipeEq   // |=
	tokPipeRe   // |~
	tokCmpEq    // ==
	tokGt       // >
	tokGte      // >=
	tokLt       // <
	tokLte      // <=
	tokPlus     // +
	tokMinus    // -
	tokMul      // *
	tokDiv      // /
)

// token is one lexeme with its 1-based position in the query.
type token struct {
	kind tokenKind
	text string // the lexeme as written
	val  string // unquoted string value (tokString)
	num  float64
	line int
	col  int
}

// describe renders the token the way Loki's yacc parser names it in errors.
func (t token) describe() string {
	switch t.kind {
	case tokEOF:
		return "$end"
	case tokIdent:
		return fmt.Sprintf("IDENTIFIER %q", t.text)
	case tokString:
		return fmt.Sprintf("STRING %q", t.val)
	case tokNumber:
		return fmt.Sprintf("NUMBER %q", t.text)
	case tokDuration:
		return fmt.Sprintf("DURATION %q", t.text)
	case tokBytes:
		return fmt.Sprintf("BYTES %q", t.text)
	}
	return t.text
}

// ParseError is a LogQL parse error printed in Loki's format.
type ParseError struct {
	Line int
	Col  int
	Msg  string
}

// Error implements error: `parse error at line L, col C: msg`, or Loki's
// position-less form `parse error : msg` when both are zero.
func (e *ParseError) Error() string {
	if e.Line == 0 && e.Col == 0 {
		return "parse error : " + e.Msg
	}
	return fmt.Sprintf("parse error at line %d, col %d: %s", e.Line, e.Col, e.Msg)
}

var (
	numberRe   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	durationRe = regexp.MustCompile(`^([0-9]+(ms|s|m|h|d|w|y))+$`)
	bytesRe    = regexp.MustCompile(`(?i)^[0-9]+(\.[0-9]+)?(b|kb|kib|mb|mib|gb|gib|tb|tib|pb|pib|eb|eib)$`)
)

// lexer turns a query into tokens on demand.
type lexer struct {
	src  string
	pos  int
	line int
	col  int
}

func newLexer(src string) *lexer { return &lexer{src: src, line: 1, col: 1} }

func (l *lexer) errorf(line, col int, format string, args ...any) *ParseError {
	return &ParseError{Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}

func (l *lexer) advance(n int) {
	for i := 0; i < n && l.pos < len(l.src); i++ {
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

func isIdentStart(b byte) bool {
	return b == '_' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

func isIdentByte(b byte) bool { return isIdentStart(b) || ('0' <= b && b <= '9') }

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// next returns the next token.
func (l *lexer) next() (token, *ParseError) {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case ' ', '\t', '\r', '\n':
			l.advance(1)
			continue
		}
		break
	}
	tok := token{line: l.line, col: l.col}
	if l.pos >= len(l.src) {
		tok.kind = tokEOF
		return tok, nil
	}
	c := l.src[l.pos]
	two := ""
	if l.pos+1 < len(l.src) {
		two = l.src[l.pos : l.pos+2]
	}
	emit := func(k tokenKind, text string) (token, *ParseError) {
		tok.kind, tok.text = k, text
		l.advance(len(text))
		return tok, nil
	}
	switch two {
	case "|=":
		return emit(tokPipeEq, two)
	case "|~":
		return emit(tokPipeRe, two)
	case "!=":
		return emit(tokNeq, two)
	case "!~":
		return emit(tokNre, two)
	case "=~":
		return emit(tokRe, two)
	case "==":
		return emit(tokCmpEq, two)
	case ">=":
		return emit(tokGte, two)
	case "<=":
		return emit(tokLte, two)
	}
	switch c {
	case '{':
		return emit(tokLBrace, "{")
	case '}':
		return emit(tokRBrace, "}")
	case '(':
		return emit(tokLParen, "(")
	case ')':
		return emit(tokRParen, ")")
	case '[':
		return emit(tokLBracket, "[")
	case ']':
		return emit(tokRBracket, "]")
	case ',':
		return emit(tokComma, ",")
	case '|':
		return emit(tokPipe, "|")
	case '=':
		return emit(tokEq, "=")
	case '>':
		return emit(tokGt, ">")
	case '<':
		return emit(tokLt, "<")
	case '+':
		return emit(tokPlus, "+")
	case '-':
		return emit(tokMinus, "-")
	case '*':
		return emit(tokMul, "*")
	case '/':
		return emit(tokDiv, "/")
	case '"':
		return l.quoted(tok)
	case '`':
		return l.raw(tok)
	}
	if isIdentStart(c) {
		end := l.pos
		for end < len(l.src) && isIdentByte(l.src[end]) {
			end++
		}
		return emit(tokIdent, l.src[l.pos:end])
	}
	if isDigit(c) {
		return l.number(tok)
	}
	return tok, l.errorf(tok.line, tok.col, "syntax error: unexpected %s", string(l.src[l.pos:l.pos+1]))
}

// quoted lexes a double-quoted string with Go escapes.
func (l *lexer) quoted(tok token) (token, *ParseError) {
	i := l.pos + 1
	for i < len(l.src) {
		switch l.src[i] {
		case '\\':
			i += 2
			continue
		case '"':
			text := l.src[l.pos : i+1]
			v, err := strconv.Unquote(text)
			if err != nil {
				return tok, l.errorf(tok.line, tok.col, "invalid string literal %s", text)
			}
			tok.kind, tok.text, tok.val = tokString, text, v
			l.advance(len(text))
			return tok, nil
		case '\n':
			return tok, l.errorf(tok.line, tok.col, "literal not terminated")
		}
		i++
	}
	return tok, l.errorf(tok.line, tok.col, "literal not terminated")
}

// raw lexes a backquoted string (no escapes).
func (l *lexer) raw(tok token) (token, *ParseError) {
	end := strings.IndexByte(l.src[l.pos+1:], '`')
	if end < 0 {
		return tok, l.errorf(tok.line, tok.col, "literal not terminated")
	}
	text := l.src[l.pos : l.pos+end+2]
	tok.kind, tok.text, tok.val = tokString, text, text[1:len(text)-1]
	l.advance(len(text))
	return tok, nil
}

// number lexes a number, a Prometheus duration (1h30m) or a bytes literal (3MiB).
func (l *lexer) number(tok token) (token, *ParseError) {
	end := l.pos
	for end < len(l.src) && (isIdentByte(l.src[end]) || l.src[end] == '.') {
		end++
	}
	text := l.src[l.pos:end]
	switch {
	case numberRe.MatchString(text):
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return tok, l.errorf(tok.line, tok.col, "invalid number %q", text)
		}
		tok.kind, tok.text, tok.num = tokNumber, text, v
	case durationRe.MatchString(text):
		d, err := promql.ParseDuration(text)
		if err != nil {
			return tok, l.errorf(tok.line, tok.col, "%v", err)
		}
		tok.kind, tok.text, tok.num = tokDuration, text, d.Seconds()
	case bytesRe.MatchString(text):
		b, err := humanize.ParseBytes(text)
		if err != nil {
			return tok, l.errorf(tok.line, tok.col, "invalid bytes literal %q", text)
		}
		tok.kind, tok.text, tok.num = tokBytes, text, float64(b)
	default:
		return tok, l.errorf(tok.line, tok.col, "syntax error: unexpected IDENTIFIER %q", text)
	}
	l.advance(len(text))
	return tok, nil
}
