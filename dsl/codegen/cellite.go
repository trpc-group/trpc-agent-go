package codegen

import (
	"fmt"
	"strconv"
	"strings"
)

// This file contains the CEL-lite lexer and parser.
// The compiler is in cellite_native.go.
//
// Supported CEL-lite subset:
//   - Literals: string, number, true/false, null
//   - Paths: state.*, input.*, nodes.* with optional [<int>] indexing
//   - Operators: +, ==, !=, <, <=, >, >=, ||
//   - Map literals: { "k": <expr>, ... }
//   - Function calls: string(<expr>), has_tool_calls()

// ---- Lexer ----

type celTokKind int

const (
	celTokEOF celTokKind = iota
	celTokIdent
	celTokString
	celTokNumber

	celTokDot
	celTokComma
	celTokColon
	celTokLBrace
	celTokRBrace
	celTokLBracket
	celTokRBracket
	celTokLParen
	celTokRParen

	celTokPlus
	celTokEqEq
	celTokNotEq
	celTokLT
	celTokLTE
	celTokGT
	celTokGTE
	celTokOrOr
)

type celTok struct {
	kind celTokKind
	text string
	pos  int // byte offset in src

	str   string  // for celTokString
	num   float64 // for celTokNumber
	isInt bool    // for celTokNumber
}

func lexCELLite(src string) ([]celTok, error) {
	l := &celLiteLexer{src: src}
	var toks []celTok
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
		if tok.kind == celTokEOF {
			break
		}
	}
	return toks, nil
}

type celLiteLexer struct {
	src string
	i   int
}

func (l *celLiteLexer) next() (celTok, error) {
	l.skipSpace()
	if l.i >= len(l.src) {
		return celTok{kind: celTokEOF, pos: len(l.src)}, nil
	}

	start := l.i
	ch := l.src[l.i]

	// Two-char operators.
	if l.i+1 < len(l.src) {
		switch l.src[l.i : l.i+2] {
		case "==":
			l.i += 2
			return celTok{kind: celTokEqEq, text: "==", pos: start}, nil
		case "!=":
			l.i += 2
			return celTok{kind: celTokNotEq, text: "!=", pos: start}, nil
		case "<=":
			l.i += 2
			return celTok{kind: celTokLTE, text: "<=", pos: start}, nil
		case ">=":
			l.i += 2
			return celTok{kind: celTokGTE, text: ">=", pos: start}, nil
		case "||":
			l.i += 2
			return celTok{kind: celTokOrOr, text: "||", pos: start}, nil
		}
	}

	// Single-char tokens.
	switch ch {
	case '.':
		l.i++
		return celTok{kind: celTokDot, text: ".", pos: start}, nil
	case ',':
		l.i++
		return celTok{kind: celTokComma, text: ",", pos: start}, nil
	case ':':
		l.i++
		return celTok{kind: celTokColon, text: ":", pos: start}, nil
	case '{':
		l.i++
		return celTok{kind: celTokLBrace, text: "{", pos: start}, nil
	case '}':
		l.i++
		return celTok{kind: celTokRBrace, text: "}", pos: start}, nil
	case '[':
		l.i++
		return celTok{kind: celTokLBracket, text: "[", pos: start}, nil
	case ']':
		l.i++
		return celTok{kind: celTokRBracket, text: "]", pos: start}, nil
	case '(':
		l.i++
		return celTok{kind: celTokLParen, text: "(", pos: start}, nil
	case ')':
		l.i++
		return celTok{kind: celTokRParen, text: ")", pos: start}, nil
	case '+':
		l.i++
		return celTok{kind: celTokPlus, text: "+", pos: start}, nil
	case '<':
		l.i++
		return celTok{kind: celTokLT, text: "<", pos: start}, nil
	case '>':
		l.i++
		return celTok{kind: celTokGT, text: ">", pos: start}, nil
	case '"':
		return l.scanString()
	}

	if isDigit(ch) || (ch == '-' && l.i+1 < len(l.src) && isDigit(l.src[l.i+1])) {
		return l.scanNumber()
	}
	if isIdentStart(ch) {
		return l.scanIdent()
	}

	return celTok{}, fmt.Errorf("CEL: unexpected character %q at %d", ch, start)
}

func (l *celLiteLexer) skipSpace() {
	for l.i < len(l.src) {
		switch l.src[l.i] {
		case ' ', '\t', '\n', '\r':
			l.i++
		default:
			return
		}
	}
}

func (l *celLiteLexer) scanString() (celTok, error) {
	start := l.i
	// Scan until the closing quote, honoring backslash escapes.
	l.i++ // consume initial "
	escaped := false
	for l.i < len(l.src) {
		c := l.src[l.i]
		l.i++
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			raw := l.src[start:l.i]
			unq, err := strconv.Unquote(raw)
			if err != nil {
				return celTok{}, fmt.Errorf("CEL: invalid string literal at %d: %w", start, err)
			}
			return celTok{kind: celTokString, text: raw, pos: start, str: unq}, nil
		}
	}
	return celTok{}, fmt.Errorf("CEL: unterminated string literal at %d", start)
}

func (l *celLiteLexer) scanNumber() (celTok, error) {
	start := l.i
	if l.src[l.i] == '-' {
		l.i++
	}
	hasDot := false
	for l.i < len(l.src) {
		c := l.src[l.i]
		if isDigit(c) {
			l.i++
			continue
		}
		if c == '.' && !hasDot {
			hasDot = true
			l.i++
			continue
		}
		break
	}
	raw := l.src[start:l.i]
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return celTok{}, fmt.Errorf("CEL: invalid number literal %q at %d: %w", raw, start, err)
	}
	isInt := !strings.Contains(raw, ".")
	return celTok{kind: celTokNumber, text: raw, pos: start, num: f, isInt: isInt}, nil
}

func (l *celLiteLexer) scanIdent() (celTok, error) {
	start := l.i
	l.i++
	for l.i < len(l.src) {
		c := l.src[l.i]
		if isIdentPart(c) {
			l.i++
			continue
		}
		break
	}
	raw := l.src[start:l.i]
	return celTok{kind: celTokIdent, text: raw, pos: start}, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

// ---- Parser ----

type celLiteParser struct {
	src   string
	toks  []celTok
	index int
}

func (p *celLiteParser) peek() celTok {
	if p.index >= len(p.toks) {
		return celTok{kind: celTokEOF, pos: len(p.src)}
	}
	return p.toks[p.index]
}

func (p *celLiteParser) next() celTok {
	t := p.peek()
	if p.index < len(p.toks) {
		p.index++
	}
	return t
}

func (p *celLiteParser) match(kind celTokKind) (celTok, bool) {
	if p.peek().kind == kind {
		return p.next(), true
	}
	return celTok{}, false
}

func (p *celLiteParser) expect(kind celTokKind) (celTok, error) {
	if tok, ok := p.match(kind); ok {
		return tok, nil
	}
	return celTok{}, p.errf(p.peek(), "expected %s", celTokName(kind))
}

func (p *celLiteParser) errf(tok celTok, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("CEL: %s at %d (near %q)", msg, tok.pos, tok.text)
}

func celTokName(k celTokKind) string {
	switch k {
	case celTokEOF:
		return "EOF"
	case celTokIdent:
		return "identifier"
	case celTokString:
		return "string"
	case celTokNumber:
		return "number"
	case celTokDot:
		return "."
	case celTokComma:
		return ","
	case celTokColon:
		return ":"
	case celTokLBrace:
		return "{"
	case celTokRBrace:
		return "}"
	case celTokLBracket:
		return "["
	case celTokRBracket:
		return "]"
	case celTokLParen:
		return "("
	case celTokRParen:
		return ")"
	case celTokPlus:
		return "+"
	case celTokEqEq:
		return "=="
	case celTokNotEq:
		return "!="
	case celTokLT:
		return "<"
	case celTokLTE:
		return "<="
	case celTokGT:
		return ">"
	case celTokGTE:
		return ">="
	case celTokOrOr:
		return "||"
	default:
		return "token"
	}
}

type celExpr interface{ isCelExpr() }

type (
	celIdent     struct{ name string }
	celStringLit struct{ val string }
	celNumberLit struct {
		val   float64
		isInt bool
	}
	celBoolLit struct{ val bool }
	celNullLit struct{}

	celSelector struct {
		x     celExpr
		field string
	}
	celIndex struct {
		x     celExpr
		index int
	}
	celBinary struct {
		op          string
		left, right celExpr
	}
	celCall struct {
		name string
		args []celExpr
	}
	celMapLit struct {
		entries []celMapEntry
	}
	celMapEntry struct {
		key   string
		value celExpr
	}
)

func (*celIdent) isCelExpr()     {}
func (*celStringLit) isCelExpr() {}
func (*celNumberLit) isCelExpr() {}
func (*celBoolLit) isCelExpr()   {}
func (*celNullLit) isCelExpr()   {}
func (*celSelector) isCelExpr()  {}
func (*celIndex) isCelExpr()     {}
func (*celBinary) isCelExpr()    {}
func (*celCall) isCelExpr()      {}
func (*celMapLit) isCelExpr()    {}

func (p *celLiteParser) parseExpr() (celExpr, error) { return p.parseOr() }

func (p *celLiteParser) parseOr() (celExpr, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.match(celTokOrOr); !ok {
			break
		}
		right, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		left = &celBinary{op: "||", left: left, right: right}
	}
	return left, nil
}

func (p *celLiteParser) parseCompare() (celExpr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}

	switch p.peek().kind {
	case celTokEqEq, celTokNotEq, celTokLT, celTokLTE, celTokGT, celTokGTE:
		opTok := p.next()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return &celBinary{op: opTok.text, left: left, right: right}, nil
	default:
		return left, nil
	}
}

func (p *celLiteParser) parseAdd() (celExpr, error) {
	left, err := p.parsePostfix()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.match(celTokPlus); !ok {
			break
		}
		right, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		left = &celBinary{op: "+", left: left, right: right}
	}
	return left, nil
}

func (p *celLiteParser) parsePostfix() (celExpr, error) {
	x, err := p.parseAtom()
	if err != nil {
		return nil, err
	}

	for {
		switch p.peek().kind {
		case celTokDot:
			p.next()
			tok, err := p.expect(celTokIdent)
			if err != nil {
				return nil, err
			}
			x = &celSelector{x: x, field: tok.text}
		case celTokLBracket:
			p.next()
			idxTok, err := p.expect(celTokNumber)
			if err != nil {
				return nil, err
			}
			if !idxTok.isInt {
				return nil, p.errf(idxTok, "index must be an integer")
			}
			if _, err := p.expect(celTokRBracket); err != nil {
				return nil, err
			}
			x = &celIndex{x: x, index: int(idxTok.num)}
		case celTokLParen:
			// Function call: only supported on a bare identifier.
			ident, ok := x.(*celIdent)
			if !ok {
				return nil, p.errf(p.peek(), "only function calls on identifiers are supported")
			}
			p.next() // consume '('
			var args []celExpr
			if p.peek().kind != celTokRParen {
				for {
					arg, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if _, ok := p.match(celTokComma); !ok {
						break
					}
				}
			}
			if _, err := p.expect(celTokRParen); err != nil {
				return nil, err
			}
			x = &celCall{name: ident.name, args: args}
		default:
			return x, nil
		}
	}
}

func (p *celLiteParser) parseAtom() (celExpr, error) {
	switch tok := p.peek(); tok.kind {
	case celTokIdent:
		tok = p.next()
		switch tok.text {
		case "true":
			return &celBoolLit{val: true}, nil
		case "false":
			return &celBoolLit{val: false}, nil
		case "null":
			return &celNullLit{}, nil
		default:
			return &celIdent{name: tok.text}, nil
		}
	case celTokString:
		tok = p.next()
		return &celStringLit{val: tok.str}, nil
	case celTokNumber:
		tok = p.next()
		return &celNumberLit{val: tok.num, isInt: tok.isInt}, nil
	case celTokLParen:
		p.next()
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(celTokRParen); err != nil {
			return nil, err
		}
		return x, nil
	case celTokLBrace:
		return p.parseMap()
	default:
		return nil, p.errf(tok, "unexpected token %q", tok.text)
	}
}

func (p *celLiteParser) parseMap() (celExpr, error) {
	if _, err := p.expect(celTokLBrace); err != nil {
		return nil, err
	}

	// Empty map.
	if _, ok := p.match(celTokRBrace); ok {
		return &celMapLit{entries: nil}, nil
	}

	var entries []celMapEntry
	for {
		keyTok, err := p.expect(celTokString)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(celTokColon); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		entries = append(entries, celMapEntry{key: keyTok.str, value: val})

		if _, ok := p.match(celTokComma); ok {
			// Allow trailing comma before }.
			if p.peek().kind == celTokRBrace {
				break
			}
			continue
		}
		break
	}

	if _, err := p.expect(celTokRBrace); err != nil {
		return nil, err
	}
	return &celMapLit{entries: entries}, nil
}

// ---- Path utilities (used by cellite_native.go) ----

type celPathStep struct {
	isIndex bool
	key     string
	index   int
}

func flattenCELLitePath(e celExpr) (root string, steps []celPathStep, ok bool) {
	switch x := e.(type) {
	case *celIdent:
		if x.name == "state" || x.name == "input" || x.name == "nodes" {
			return x.name, nil, true
		}
		return "", nil, false
	case *celSelector:
		r, s, ok := flattenCELLitePath(x.x)
		if !ok {
			return "", nil, false
		}
		s = append(s, celPathStep{key: x.field})
		return r, s, true
	case *celIndex:
		r, s, ok := flattenCELLitePath(x.x)
		if !ok {
			return "", nil, false
		}
		s = append(s, celPathStep{isIndex: true, index: x.index})
		return r, s, true
	default:
		return "", nil, false
	}
}

func formatFloat(f float64) string {
	// Use a stable, compact representation without introducing scientific
	// notation for the small values seen in DSL examples.
	return strconv.FormatFloat(f, 'f', -1, 64)
}
