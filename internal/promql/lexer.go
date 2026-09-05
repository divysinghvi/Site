// Package promql is the PromQL subset served by /api/v1/query and
// /api/v1/query_range: instant and range selectors, literals, arithmetic and
// comparison operators with one-to-one vector matching, the five basic
// aggregations, the rate family, the *_over_time family and a handful of
// scalar functions. The lexer, parser and evaluator reproduce Prometheus'
// grammar, error messages and float semantics for everything they accept
// and report a Prometheus-style parse error for every construct outside the
// subset (docs/promql-subset.md).
package promql

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// tokenType identifies a lexer token.
type tokenType int

// Token types. The groups (operators, aggregators, keywords) mirror the
// Prometheus lexer so token descriptions in error messages match.
const (
	tEOF tokenType = iota
	tError
	tComment
	tIdentifier
	tMetricIdentifier
	tLeftParen
	tRightParen
	tLeftBrace
	tRightBrace
	tLeftBracket
	tRightBracket
	tComma
	tColon
	tEql // = (matcher / rejected outside braces)
	tNumber
	tDuration
	tString

	operatorsStart
	tAdd
	tSub
	tMul
	tDiv
	tMod
	tPow
	tEqlc // ==
	tNeq  // != (matcher and comparison)
	tLss
	tLte
	tGtr
	tGte
	tEqlRegex // =~
	tNeqRegex // !~
	tAt
	tLand
	tLor
	tLunless
	tAtan2
	operatorsEnd

	aggregatorsStart
	tSum
	tAvg
	tCount
	tMin
	tMax
	tGroup
	tStddev
	tStdvar
	tTopk
	tBottomk
	tCountValues
	tQuantile
	tLimitk
	tLimitRatio
	aggregatorsEnd

	keywordsStart
	tBool
	tBy
	tWithout
	tOn
	tIgnoring
	tGroupLeft
	tGroupRight
	tOffset
	tFill
	tFillLeft
	tFillRight
	tSmoothed
	tAnchored
	tStart
	tEnd
	tStep
	tRange
	tMaxOf
	tMinOf
	keywordsEnd
)

// keywords maps the lower-cased word to its token (Prometheus `key`).
var keywords = map[string]tokenType{
	"and": tLand, "or": tLor, "unless": tLunless, "atan2": tAtan2,
	"sum": tSum, "avg": tAvg, "count": tCount, "min": tMin, "max": tMax, "group": tGroup,
	"stddev": tStddev, "stdvar": tStdvar, "topk": tTopk, "bottomk": tBottomk,
	"count_values": tCountValues, "quantile": tQuantile, "limitk": tLimitk, "limit_ratio": tLimitRatio,
	"offset": tOffset, "smoothed": tSmoothed, "anchored": tAnchored, "by": tBy, "without": tWithout,
	"on": tOn, "ignoring": tIgnoring, "group_left": tGroupLeft, "group_right": tGroupRight,
	"fill": tFill, "fill_left": tFillLeft, "fill_right": tFillRight, "bool": tBool,
	"start": tStart, "end": tEnd, "step": tStep, "range": tRange, "max_of": tMaxOf, "min_of": tMinOf,
	"inf": tNumber, "nan": tNumber,
}

// tokenText is the fixed text of delimiters, operators, aggregators and
// keywords (Prometheus ItemTypeStr), used by descriptions and the printer.
var tokenText = map[tokenType]string{
	tLeftParen: "(", tRightParen: ")", tLeftBrace: "{", tRightBrace: "}", tLeftBracket: "[", tRightBracket: "]",
	tComma: ",", tEql: "=", tColon: ":",
	tSub: "-", tAdd: "+", tMul: "*", tMod: "%", tDiv: "/", tEqlc: "==", tNeq: "!=", tLte: "<=", tLss: "<",
	tGte: ">=", tGtr: ">", tEqlRegex: "=~", tNeqRegex: "!~", tPow: "^", tAt: "@",
}

func init() {
	for word, typ := range keywords {
		if typ != tNumber {
			tokenText[typ] = word
		}
	}
}

func (t tokenType) isOperator() bool   { return t > operatorsStart && t < operatorsEnd }
func (t tokenType) isAggregator() bool { return t > aggregatorsStart && t < aggregatorsEnd }
func (t tokenType) isKeyword() bool    { return t > keywordsStart && t < keywordsEnd }
func (t tokenType) isComparison() bool {
	switch t {
	case tEqlc, tNeq, tLss, tLte, tGtr, tGte:
		return true
	}
	return false
}
func (t tokenType) isSetOperator() bool { return t == tLand || t == tLor || t == tLunless }

// String returns the operator text (used by the printer).
func (t tokenType) String() string {
	if s, ok := tokenText[t]; ok {
		return s
	}
	return fmt.Sprintf("<token %d>", int(t))
}

// token is one lexed item; pos is the 0-based byte offset of its first byte.
type token struct {
	typ tokenType
	pos int
	val string
}

// String renders the token the way Prometheus prints it inside messages.
func (t token) String() string {
	switch {
	case t.typ == tEOF:
		return "EOF"
	case t.typ == tError:
		return t.val
	case t.typ == tIdentifier || t.typ == tMetricIdentifier:
		return fmt.Sprintf("%q", t.val)
	case t.typ.isKeyword():
		return fmt.Sprintf("<%s>", t.val)
	case t.typ.isOperator():
		return fmt.Sprintf("<op:%s>", t.val)
	case t.typ.isAggregator():
		return fmt.Sprintf("<aggr:%s>", t.val)
	case len(t.val) > 10:
		return fmt.Sprintf("%.10q...", t.val)
	}
	return fmt.Sprintf("%q", t.val)
}

// desc is the "unexpected <desc>" text (Prometheus Item.desc).
func (t token) desc() string {
	if _, ok := tokenText[t.typ]; ok {
		return t.String()
	}
	switch t.typ {
	case tEOF:
		return "end of input"
	case tError:
		return "error"
	case tComment:
		return "comment"
	case tIdentifier:
		return "identifier " + t.String()
	case tMetricIdentifier:
		return "metric identifier " + t.String()
	case tString:
		return "string " + t.String()
	case tNumber:
		return "number " + t.String()
	case tDuration:
		return "duration " + t.String()
	}
	return t.String()
}

const eof = -1

// lexer is a port of the Prometheus PromQL lexer restricted to the subset:
// the state machine, positions and error texts are the same, so parse errors
// read exactly like Prometheus'.
type lexer struct {
	input string
	pos   int
	start int
	width int

	parenDepth  int
	braceOpen   bool
	bracketOpen bool
	stringOpen  rune

	items []token
	err   *token // first lexer error (terminates the scan)
}

// lex tokenises the whole input; the returned slice ends with tEOF or tError.
func lex(input string) []token {
	l := &lexer{input: input}
	state := lexStatements
	for state != nil {
		state = state(l)
	}
	return l.items
}

type stateFn func(*lexer) stateFn

func (l *lexer) next() rune {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = w
	l.pos += w
	return r
}

func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

func (l *lexer) backup() { l.pos -= l.width }

func (l *lexer) emit(t tokenType) {
	l.items = append(l.items, token{t, l.start, l.input[l.start:l.pos]})
	l.start = l.pos
}

func (l *lexer) ignore() { l.start = l.pos }

func (l *lexer) accept(valid string) bool {
	if strings.ContainsRune(valid, l.next()) {
		return true
	}
	l.backup()
	return false
}

func (l *lexer) is(valid string) bool { return strings.ContainsRune(valid, l.peek()) }

func (l *lexer) acceptRun(valid string) {
	for strings.ContainsRune(valid, l.next()) {
	}
	l.backup()
}

func (l *lexer) errorf(format string, args ...any) stateFn {
	t := token{tError, l.start, fmt.Sprintf(format, args...)}
	l.items = append(l.items, t)
	l.err = &t
	return nil
}

func isSpace(r rune) bool        { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
func isDigit(r rune) bool        { return '0' <= r && r <= '9' }
func isAlpha(r rune) bool        { return r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') }
func isAlphaNumeric(r rune) bool { return isAlpha(r) || isDigit(r) }
func isEndOfLine(r rune) bool    { return r == '\r' || r == '\n' }

func lexStatements(l *lexer) stateFn {
	if l.braceOpen {
		return lexInsideBraces
	}
	if strings.HasPrefix(l.input[l.pos:], "#") {
		return lexLineComment
	}
	switch r := l.next(); {
	case r == eof:
		switch {
		case l.parenDepth != 0:
			return l.errorf("unclosed left parenthesis")
		case l.bracketOpen:
			return l.errorf("unclosed left bracket")
		}
		l.emit(tEOF)
		return nil
	case r == ',':
		l.emit(tComma)
	case isSpace(r):
		return lexSpace
	case r == '*':
		l.emit(tMul)
	case r == '/':
		l.emit(tDiv)
	case r == '%':
		l.emit(tMod)
	case r == '+':
		l.emit(tAdd)
	case r == '-':
		l.emit(tSub)
	case r == '^':
		l.emit(tPow)
	case r == '=':
		switch t := l.peek(); t {
		case '=':
			l.next()
			l.emit(tEqlc)
		case '~':
			return l.errorf("unexpected character after '=': %q", t)
		default:
			l.emit(tEql)
		}
	case r == '!':
		if t := l.next(); t != '=' {
			return l.errorf("unexpected character after '!': %q", t)
		}
		l.emit(tNeq)
	case r == '<':
		if l.peek() == '=' {
			l.next()
			l.emit(tLte)
		} else {
			l.emit(tLss)
		}
	case r == '>':
		if l.peek() == '=' {
			l.next()
			l.emit(tGte)
		} else {
			l.emit(tGtr)
		}
	case isDigit(r) || (r == '.' && isDigit(l.peek())):
		l.backup()
		return lexNumberOrDuration
	case r == '"' || r == '\'':
		l.stringOpen = r
		return lexString
	case r == '`':
		l.stringOpen = r
		return lexRawString
	case isAlpha(r) || r == ':':
		if !l.bracketOpen {
			l.backup()
			return lexKeywordOrIdentifier
		}
		if r == ':' {
			l.emit(tColon)
			return lexStatements
		}
		// Prometheus lexes step()/range()/min_of/max_of here (duration
		// expressions); the subset reports them through the parser.
		l.backup()
		return lexKeywordOrIdentifier
	case r == '(':
		l.emit(tLeftParen)
		l.parenDepth++
		return lexStatements
	case r == ')':
		l.emit(tRightParen)
		l.parenDepth--
		if l.parenDepth < 0 {
			// Prometheus overwrites the just-emitted item with the error item.
			l.items = l.items[:len(l.items)-1]
			return l.errorf("unexpected right parenthesis %q", r)
		}
		return lexStatements
	case r == '{':
		l.emit(tLeftBrace)
		l.braceOpen = true
		return lexInsideBraces
	case r == '[':
		if l.bracketOpen {
			return l.errorf("unexpected left bracket %q", r)
		}
		l.emit(tLeftBracket)
		if isSpace(l.peek()) {
			skipSpaces(l)
		}
		l.bracketOpen = true
		return lexStatements
	case r == ']':
		if !l.bracketOpen {
			return l.errorf("unexpected right bracket %q", r)
		}
		l.emit(tRightBracket)
		l.bracketOpen = false
	case r == '@':
		l.emit(tAt)
	default:
		return l.errorf("unexpected character: %q", r)
	}
	return lexStatements
}

func lexInsideBraces(l *lexer) stateFn {
	if strings.HasPrefix(l.input[l.pos:], "#") {
		return lexLineComment
	}
	switch r := l.next(); {
	case r == eof:
		return l.errorf("unexpected end of input inside braces")
	case isSpace(r):
		return lexSpace
	case isAlpha(r):
		l.backup()
		return lexIdentifier
	case r == ',':
		l.emit(tComma)
	case r == '"' || r == '\'':
		l.stringOpen = r
		return lexString
	case r == '`':
		l.stringOpen = r
		return lexRawString
	case r == '=':
		if l.next() == '~' {
			l.emit(tEqlRegex)
			break
		}
		l.backup()
		l.emit(tEql)
	case r == '!':
		switch nr := l.next(); nr {
		case '~':
			l.emit(tNeqRegex)
		case '=':
			l.emit(tNeq)
		default:
			return l.errorf("unexpected character after '!' inside braces: %q", nr)
		}
	case r == '{':
		return l.errorf("unexpected left brace %q", r)
	case r == '}':
		l.emit(tRightBrace)
		l.braceOpen = false
		return lexStatements
	default:
		return l.errorf("unexpected character inside braces: %q", r)
	}
	return lexInsideBraces
}

func lexIdentifier(l *lexer) stateFn {
	for isAlphaNumeric(l.next()) {
	}
	l.backup()
	l.emit(tIdentifier)
	return lexStatements
}

func lexKeywordOrIdentifier(l *lexer) stateFn {
	for {
		switch r := l.next(); {
		case isAlphaNumeric(r) || r == ':':
		default:
			l.backup()
			word := l.input[l.start:l.pos]
			switch kw, ok := keywords[strings.ToLower(word)]; {
			case ok:
				if kw == tFill || kw == tFillLeft || kw == tFillRight {
					if !l.peekFollowedByLeftParen() {
						l.emit(tIdentifier)
						return lexStatements
					}
				}
				l.emit(kw)
			case !strings.Contains(word, ":"):
				l.emit(tIdentifier)
			default:
				l.emit(tMetricIdentifier)
			}
			return lexStatements
		}
	}
}

func (l *lexer) peekFollowedByLeftParen() bool {
	pos := l.pos
	for {
		if pos >= len(l.input) {
			return false
		}
		r, w := utf8.DecodeRuneInString(l.input[pos:])
		if !isSpace(r) {
			return r == '('
		}
		pos += w
	}
}

func lexSpace(l *lexer) stateFn {
	for isSpace(l.peek()) {
		l.next()
	}
	l.ignore()
	return lexStatements
}

func skipSpaces(l *lexer) {
	for isSpace(l.peek()) {
		l.next()
	}
	l.ignore()
}

func lexLineComment(l *lexer) stateFn {
	l.pos++
	for r := l.next(); !isEndOfLine(r) && r != eof; {
		r = l.next()
	}
	l.backup()
	l.emit(tComment)
	return lexStatements
}

func lexString(l *lexer) stateFn {
	for {
		switch l.next() {
		case '\\':
			if s := lexEscape(l); s != nil {
				return s
			}
		case utf8.RuneError:
			return l.errorf("invalid UTF-8 rune")
		case eof, '\n':
			return l.errorf("unterminated quoted string")
		case l.stringOpen:
			l.emit(tString)
			return lexStatements
		}
	}
}

// lexEscape validates one escape sequence; it returns a non-nil state only on error.
func lexEscape(l *lexer) stateFn {
	var n int
	var base, maxVal uint32
	ch := l.next()
	switch ch {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', l.stringOpen:
		return nil
	case '0', '1', '2', '3', '4', '5', '6', '7':
		n, base, maxVal = 3, 8, 255
	case 'x':
		ch = l.next()
		n, base, maxVal = 2, 16, 255
	case 'u':
		ch = l.next()
		n, base, maxVal = 4, 16, 0x10FFFF
	case 'U':
		ch = l.next()
		n, base, maxVal = 8, 16, 0x10FFFF
	case eof:
		return l.errorf("escape sequence not terminated")
	default:
		return l.errorf("unknown escape sequence %#U", ch)
	}
	var x uint32
	for n > 0 {
		d := uint32(digitVal(ch))
		if d >= base {
			if ch == eof {
				return l.errorf("escape sequence not terminated")
			}
			return l.errorf("illegal character %#U in escape sequence", ch)
		}
		x = x*base + d
		n--
		if n > 0 {
			ch = l.next()
		}
	}
	if x > maxVal || 0xD800 <= x && x < 0xE000 {
		return l.errorf("escape sequence is an invalid Unicode code point")
	}
	return nil
}

func digitVal(ch rune) int {
	switch {
	case '0' <= ch && ch <= '9':
		return int(ch - '0')
	case 'a' <= ch && ch <= 'f':
		return int(ch - 'a' + 10)
	case 'A' <= ch && ch <= 'F':
		return int(ch - 'A' + 10)
	}
	return 16
}

func lexRawString(l *lexer) stateFn {
	for {
		switch l.next() {
		case utf8.RuneError:
			return l.errorf("invalid UTF-8 rune")
		case eof:
			return l.errorf("unterminated raw string")
		case l.stringOpen:
			l.emit(tString)
			return lexStatements
		}
	}
}

func lexNumberOrDuration(l *lexer) stateFn {
	if l.scanNumber() {
		l.emit(tNumber)
		return lexStatements
	}
	if l.acceptRemainingDuration() {
		l.backup()
		l.emit(tDuration)
		return lexStatements
	}
	return l.errorf("bad number or duration syntax: %q", l.input[l.start:l.pos])
}

// scanNumber is Prometheus' number scanner: decimal, hex, exponent, `_` separators.
func (l *lexer) scanNumber() bool {
	initialPos := l.pos
	digitPattern := "0123456789"
	if l.accept("0") && l.accept("xX") {
		l.accept("_")
		digitPattern = "0123456789abcdefABCDEF"
	}
	const (
		dotPattern            = "."
		exponentPattern       = "eE"
		underscorePattern     = "_"
		dotAntiPattern        = "_."
		exponentAntiPattern   = "._eE"
		underscoreAntiPattern = "._eE"
	)
	l.accept(dotPattern)
	l.accept(digitPattern)
	dotConsumed := false
	exponentConsumed := false
	for l.is(digitPattern + dotPattern + underscorePattern + exponentPattern) {
		if l.is(dotPattern) && dotConsumed {
			l.accept(dotPattern)
			return false
		}
		if l.is(exponentPattern) && exponentConsumed {
			l.accept(exponentPattern)
			return false
		}
		if l.accept(dotPattern) {
			dotConsumed = true
			if l.accept(dotAntiPattern) {
				return false
			}
			if len(digitPattern) > 10 {
				return false
			}
			continue
		}
		if l.accept(exponentPattern) {
			exponentConsumed = true
			l.accept("+-")
			if l.accept(exponentAntiPattern) || l.peek() == eof {
				return false
			}
			continue
		}
		if l.accept(underscorePattern) {
			if l.accept(underscoreAntiPattern) || l.peek() == eof {
				return false
			}
			continue
		}
		l.acceptRun(digitPattern)
	}
	if l.pos == initialPos {
		return false
	}
	return !isAlphaNumeric(l.peek())
}

func (l *lexer) acceptRemainingDuration() bool {
	if !l.accept("smhdwy") {
		return false
	}
	l.accept("s")
	for l.accept("0123456789") {
		for l.accept("0123456789") {
		}
		if !l.accept("smhdw") {
			return false
		}
		l.accept("s")
	}
	return !isAlphaNumeric(l.next())
}
