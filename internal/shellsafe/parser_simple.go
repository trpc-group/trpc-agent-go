// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// This file is the default backend for the parseCommand seam declared
// in parser.go. It is a hand-rolled, dependency-free lexer that
// implements the "v1 / 80%" grammar described in the package doc:
// literal words (bare, single-quoted, expansion-free double-quoted),
// optionally joined by the four safe sequencing operators '|', '&&',
// '||' and ';'. Every other shell construct - parameter / command /
// arithmetic / process substitution, redirection, subshell, block,
// control flow, function declaration, leading variable assignment,
// background, history expansion, glob, comment - is structurally
// rejected with a model-readable error before any policy lookup.
//
// The lexer is single-pass O(n) over the input bytes; UTF-8 is
// transparent because all of its multi-byte sequences live above
// 0x7F and never overlap with any of the ASCII operator bytes the
// lexer cares about.
//
// To plug in a richer backend later (full bash AST, glob support,
// safe redirections, ...), add a sibling file that implements a
// commandParser-shaped function and have parser.go point
// parseCommand at it. Nothing else in the package needs to change.

package shellsafe

import (
	"errors"
	"fmt"
	"strings"
)

// maxCommandLen is a sanity cap on the input length so a pathological
// command does not balloon allocations downstream. The lexer is O(n)
// in the input size, so this is not a DoS boundary; it just gives
// callers a clear "too long" error instead of an opaque parse
// failure deep in the policy layer.
const maxCommandLen = 16 * 1024

// maxSegments caps the number of pipeline segments. Any reasonable
// command stays well below this; the cap exists so a malicious /
// confused model that emits 10000 pipes does not turn the policy
// check into an unbounded loop in callers that iterate over segments.
const maxSegments = 32

// tokKind enumerates the (very small) set of lexical token kinds
// the v1 parser recognises.
type tokKind int

const (
	tokWord  tokKind = iota // a single argv element (post-quoting)
	tokPipe                 // |
	tokAndIf                // &&
	tokOrIf                 // ||
	tokSemi                 // ;
	tokEOF
)

type token struct {
	kind  tokKind
	value string // populated only for tokWord
}

// lexState carries the running state of lexSimple. Pulling it into
// a struct keeps lexSimple's signature small and avoids a long list
// of out-parameters in the per-byte helpers.
type lexState struct {
	tokens         []token
	atSegmentStart bool
	attachedWord   bool
}

// shellKeywords maps a bareword that appears at command-start
// position to the human-readable name of the construct it would
// open. Recognising these at the lexer level lets us produce
// friendly errors (e.g. "if statement is not allowed") instead of
// failing later on an unmatched token.
var shellKeywords = map[string]string{
	"if":       "if statement",
	"then":     "if statement",
	"elif":     "if statement",
	"else":     "if statement",
	"fi":       "if statement",
	"for":      "for loop",
	"do":       "for loop",
	"done":     "for loop",
	"while":    "while/until loop",
	"until":    "while/until loop",
	"case":     "case statement",
	"esac":     "case statement",
	"function": "function declaration",
	"select":   "select loop",
}

// parseCommandSimple is the default implementation of commandParser.
// It runs the v1 hand-rolled lexer over src and produces the flat
// list of pipeline segments expected by the Policy layer.
func parseCommandSimple(src string) ([][]string, error) {
	if len(src) > maxCommandLen {
		return nil, fmt.Errorf(
			"command too long (max %d bytes)", maxCommandLen)
	}
	tokens, err := lexSimple(src)
	if err != nil {
		return nil, err
	}
	return buildSegments(tokens)
}

// lexSimple walks src once, dispatching each byte to a small helper.
// The big switch is intentionally narrow so cyclomatic complexity
// stays under the project lint budget; each helper is responsible
// for advancing the cursor and updating state.
func lexSimple(src string) ([]token, error) {
	st := lexState{atSegmentStart: true}
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t':
			i++
			st.attachedWord = false
		case c == '\n' || c == '\r':
			return nil, errors.New("newline is not allowed")
		case isOperatorByte(c):
			ni, err := lexOperator(src, i, &st)
			if err != nil {
				return nil, err
			}
			i = ni
		case isRejectByte(c):
			return nil, rejectByteError(src, i, &st)
		case isWordStarter(c):
			ni, err := lexAndAppendWord(src, i, &st)
			if err != nil {
				return nil, err
			}
			i = ni
		default:
			return nil, fmt.Errorf("character %q is not allowed", c)
		}
	}
	st.tokens = append(st.tokens, token{kind: tokEOF})
	return st.tokens, nil
}

// isOperatorByte returns true for the leading byte of any of the
// four safe sequencing operators ('|', '&&', '||', ';'). The second
// byte (when present) is consumed by lexOperator.
func isOperatorByte(c byte) bool {
	return c == '|' || c == '&' || c == ';'
}

// isRejectByte returns true for any byte that, if seen outside of a
// quoted word, would either re-introduce shell evaluation or
// require parsing a construct we deliberately do not support in v1.
//
// '{' and '}' are deliberately absent: they are allowed inside a
// word so common literal uses like "xargs -I{}" and "find -exec {} \\;"
// keep working. A '{' that would actually start a block, or a
// braced sequence that would trigger bash brace expansion, is
// caught later by validateBracesInWord.
func isRejectByte(c byte) bool {
	switch c {
	case '$', '`', '<', '>', '(', ')', '[', ']',
		'*', '?', '#', '!':
		return true
	}
	return c < 0x20 // any remaining control byte (NUL, ESC, etc.)
}

// isWordStarter reports whether c can begin a word (a bareword byte,
// a quote, or a backslash escape).
func isWordStarter(c byte) bool {
	return c == '\'' || c == '"' || c == '\\' || isBareword(c)
}

// isBareword reports whether c can appear unquoted inside a word.
// It is the complement of every byte the lexer treats specially.
// '{' and '}' are allowed here so a word can carry literal braces
// (e.g. "-I{}"); validateBracesInWord enforces the actual safety
// rule (no block, no brace expansion) once the full word is known.
func isBareword(c byte) bool {
	switch c {
	case '$', '`', '(', ')', '<', '>', '&', '[', ']',
		'*', '?', '#', '!', '|', ';', ' ', '\t', '\n', '\r',
		'\'', '"', '\\':
		return false
	}
	return c >= 0x20
}

// lexOperator handles '|', '&' and ';' starting at src[i] and emits
// the corresponding token (or returns an error if the byte starts
// an unsupported construct like '&', '|&' or ';;').
func lexOperator(src string, i int, st *lexState) (int, error) {
	switch src[i] {
	case '|':
		if i+1 < len(src) && src[i+1] == '|' {
			st.tokens = append(st.tokens, token{kind: tokOrIf})
			return resetAfterOp(st, i+2), nil
		}
		if i+1 < len(src) && src[i+1] == '&' {
			return 0, errors.New(
				"pipe with stderr '|&' is not allowed")
		}
		st.tokens = append(st.tokens, token{kind: tokPipe})
		return resetAfterOp(st, i+1), nil
	case '&':
		if i+1 < len(src) && src[i+1] == '&' {
			st.tokens = append(st.tokens, token{kind: tokAndIf})
			return resetAfterOp(st, i+2), nil
		}
		return 0, errors.New("background operator '&' is not allowed")
	case ';':
		if i+1 < len(src) && src[i+1] == ';' {
			return 0, errors.New(
				"case statement separator ';;' is not allowed")
		}
		st.tokens = append(st.tokens, token{kind: tokSemi})
		return resetAfterOp(st, i+1), nil
	}
	// Unreachable: caller filters via isOperatorByte.
	return 0, fmt.Errorf("internal: unexpected operator byte %q", src[i])
}

func resetAfterOp(st *lexState, next int) int {
	st.atSegmentStart = true
	st.attachedWord = false
	return next
}

// rejectByteError returns the most specific error message the lexer
// can produce for a single forbidden byte. The wording is chosen so
// every (input, expected-substring) pair the existing test suite
// asserts on continues to match, and so a model reading the error
// can plausibly fix the command on the next turn.
func rejectByteError(src string, i int, st *lexState) error {
	c := src[i]
	switch c {
	case '$':
		return dollarError(src, i)
	case '`':
		return errors.New("command substitution backtick is not allowed")
	case '<':
		if i+1 < len(src) && src[i+1] == '(' {
			return errors.New(
				"process substitution '<(...)' is not allowed")
		}
		return errors.New("input redirection '<' is not allowed")
	case '>':
		if i+1 < len(src) && src[i+1] == '(' {
			return errors.New(
				"process substitution '>(...)' is not allowed")
		}
		return errors.New("output redirection '>' is not allowed")
	case '(':
		if st.attachedWord {
			return errors.New("function declaration is not allowed")
		}
		return errors.New("subshell '(...)' is not allowed")
	case ')':
		return errors.New("unexpected ')' is not allowed")
	case '[':
		return errors.New(
			"test expression or glob '[...]' is not allowed")
	case ']':
		return errors.New("unexpected ']' is not allowed")
	case '*', '?':
		return fmt.Errorf(
			"glob character '%c' is not allowed (quote it if literal)",
			c)
	case '!':
		if st.atSegmentStart {
			return errors.New("negation operator '!' is not allowed")
		}
		return errors.New("history expansion '!' is not allowed")
	case '#':
		return errors.New("comment character '#' is not allowed")
	}
	return fmt.Errorf("control character %#02x is not allowed", c)
}

// dollarError returns the specific expansion kind a '$' byte was
// about to start. The lookahead is bounded to two bytes so the
// helper stays simple; deeper recognition is not needed because
// the construct is rejected either way.
func dollarError(src string, i int) error {
	if i+1 >= len(src) {
		return errors.New("parameter expansion '$' is not allowed")
	}
	switch src[i+1] {
	case '(':
		if i+2 < len(src) && src[i+2] == '(' {
			return errors.New(
				"arithmetic expansion '$((...))' is not allowed")
		}
		return errors.New(
			"command substitution '$(...)' is not allowed")
	case '{':
		return errors.New(
			"parameter expansion '${...}' is not allowed")
	}
	return errors.New("parameter expansion '$VAR' is not allowed")
}

// lexAndAppendWord lexes a single word starting at src[i], appends
// it to st.tokens, and updates state to reflect that we are now in
// the middle of a segment.
func lexAndAppendWord(src string, i int, st *lexState) (int, error) {
	w, ni, err := lexWord(src, i)
	if err != nil {
		return 0, err
	}
	if st.atSegmentStart {
		if kw, ok := shellKeywords[w]; ok {
			return 0, fmt.Errorf("%s is not allowed", kw)
		}
		if w == "{" {
			return 0, errors.New("block '{...}' is not allowed")
		}
	}
	if err := validateBracesInWord(w); err != nil {
		return 0, err
	}
	st.tokens = append(st.tokens, token{kind: tokWord, value: w})
	st.atSegmentStart = false
	st.attachedWord = true
	return ni, nil
}

// validateBracesInWord rejects the two unsafe shapes that '{' / '}'
// can take inside a word: an unbalanced '{' (which in bash would
// either start a block or remain literal depending on context, both
// states we do not want to reason about), and a balanced '{...}'
// whose body contains the comma or '..' that triggers bash brace
// expansion - the latter would silently change argv length at exec
// time. A balanced '{}' with no expansion-triggering byte inside is
// accepted so common literal uses like "xargs -I{}" keep working.
func validateBracesInWord(word string) error {
	for i := 0; i < len(word); i++ {
		if word[i] != '{' {
			continue
		}
		end := strings.IndexByte(word[i+1:], '}')
		if end < 0 {
			return errors.New(
				"unmatched '{' is not allowed (block or brace expansion?)")
		}
		inner := word[i+1 : i+1+end]
		if strings.ContainsAny(inner, ",") ||
			strings.Contains(inner, "..") {
			return fmt.Errorf(
				"brace expansion '{%s}' is not allowed", inner)
		}
		i += end + 1
	}
	return nil
}

// lexWord scans one word starting at src[start] and returns its
// post-quoting value plus the next index. A word is the maximal
// concatenation of (bareword | single-quoted | double-quoted-literal
// | backslash-escape) parts with no intervening whitespace.
func lexWord(src string, start int) (string, int, error) {
	var sb strings.Builder
	i := start
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\'':
			ni, err := lexSingleQuoted(src, i, &sb)
			if err != nil {
				return "", 0, err
			}
			i = ni
		case c == '"':
			ni, err := lexDoubleQuoted(src, i, &sb)
			if err != nil {
				return "", 0, err
			}
			i = ni
		case c == '\\':
			ni, err := lexBackslash(src, i, &sb)
			if err != nil {
				return "", 0, err
			}
			i = ni
		case isBareword(c):
			sb.WriteByte(c)
			i++
		default:
			return sb.String(), i, nil
		}
	}
	return sb.String(), i, nil
}

// lexSingleQuoted copies the literal bytes between matched single
// quotes into sb. Inside single quotes, no escape sequence is
// honoured - bash itself has the same behaviour - so the loop is
// simply "everything up to the next '". Bare newlines (\n / \r)
// are rejected to keep the "no multi-line command" rule uniform
// across quoting styles; without this an attacker could hide a
// second logical command behind a single-quoted "argument".
func lexSingleQuoted(src string, i int, sb *strings.Builder) (int, error) {
	j := i + 1
	for j < len(src) && src[j] != '\'' {
		if src[j] == '\n' || src[j] == '\r' {
			return 0, errors.New(
				"newline is not allowed inside single-quoted string",
			)
		}
		j++
	}
	if j >= len(src) {
		return 0, errors.New("unterminated single-quoted string")
	}
	sb.WriteString(src[i+1 : j])
	return j + 1, nil
}

// lexDoubleQuoted copies the literal bytes between matched double
// quotes into sb. '$' and '`' inside double quotes are rejected
// because they would re-introduce expansion; bare newlines are
// rejected so a multi-line command cannot hide intent.
//
// Backslash handling follows POSIX: inside a double-quoted string
// the backslash is only special before one of `$`, '`', `"`, `\`
// (and newline, which we reject outright). Before any other byte
// the backslash is preserved literally. Folding `\X` to `X`
// unconditionally would let `"./s\afe"` parse as `./safe` while
// the shell still execs `./s\afe`, letting a workspace-controlled
// file with a backslash-bearing name bypass an allowlist entry
// for the folded form.
func lexDoubleQuoted(src string, i int, sb *strings.Builder) (int, error) {
	j := i + 1
	for j < len(src) {
		c := src[j]
		switch {
		case c == '"':
			return j + 1, nil
		case c == '$':
			return 0, errors.New(
				"parameter expansion inside double-quoted string is not allowed")
		case c == '`':
			return 0, errors.New(
				"command substitution inside double-quoted string is not allowed")
		case c == '\\':
			if j+1 >= len(src) {
				return 0, errors.New(
					"trailing backslash in double-quoted string")
			}
			n := src[j+1]
			if n == '\n' || n == '\r' {
				return 0, errors.New(
					"escaped newline is not allowed")
			}
			if isDQEscapable(n) {
				sb.WriteByte(n)
				j += 2
			} else {
				sb.WriteByte('\\')
				j++
			}
		case c == '\n' || c == '\r':
			return 0, errors.New(
				"newline inside double-quoted string is not allowed")
		default:
			sb.WriteByte(c)
			j++
		}
	}
	return 0, errors.New("unterminated double-quoted string")
}

// isDQEscapable reports whether c is a byte that, when preceded by
// '\\' inside a double-quoted string, is folded into a literal c
// per POSIX. Any byte not in this set keeps the leading backslash.
func isDQEscapable(c byte) bool {
	return c == '$' || c == '`' || c == '"' || c == '\\'
}

// lexBackslash consumes a '\X' escape outside any quotes and writes
// the literal byte X to sb. Escaping a newline (line continuation)
// is rejected because we already reject bare newlines.
func lexBackslash(src string, i int, sb *strings.Builder) (int, error) {
	if i+1 >= len(src) {
		return 0, errors.New("trailing backslash")
	}
	n := src[i+1]
	if n == '\n' || n == '\r' {
		return 0, errors.New("escaped newline is not allowed")
	}
	sb.WriteByte(n)
	return i + 2, nil
}

// buildSegments turns the flat token stream into the segment slice
// the Policy layer expects, rejecting empty segments and leading
// variable assignments along the way.
func buildSegments(tokens []token) ([][]string, error) {
	var segments [][]string
	var cur []string
	for _, t := range tokens {
		switch t.kind {
		case tokWord:
			cur = append(cur, t.value)
		case tokPipe, tokAndIf, tokOrIf, tokSemi:
			if err := flushSegment(&segments, &cur, t.kind, false); err != nil {
				return nil, err
			}
		case tokEOF:
			if err := flushSegment(&segments, &cur, tokEOF, true); err != nil {
				return nil, err
			}
		}
		if len(segments) > maxSegments {
			return nil, fmt.Errorf(
				"too many pipeline segments (max %d)", maxSegments)
		}
	}
	if len(segments) == 0 {
		return nil, errors.New("command is empty")
	}
	return segments, nil
}

// flushSegment moves the words accumulated in *cur into *segments,
// enforcing the per-segment rules: non-empty left side at every
// operator, optional non-empty right side at EOF, and no leading
// variable assignment on argv[0].
func flushSegment(
	segments *[][]string,
	cur *[]string,
	kind tokKind,
	atEOF bool,
) error {
	if len(*cur) == 0 {
		if atEOF {
			if len(*segments) > 0 {
				return errors.New(
					"trailing operator with empty right side")
			}
			return nil
		}
		return fmt.Errorf(
			"operator %q has empty left side", opName(kind))
	}
	if isLeadingAssignment((*cur)[0]) {
		return fmt.Errorf(
			"leading variable assignment %q is not allowed",
			(*cur)[0])
	}
	*segments = append(*segments, *cur)
	*cur = nil
	return nil
}

// opName returns a printable form of a sequencing operator for
// inclusion in error messages.
func opName(k tokKind) string {
	switch k {
	case tokPipe:
		return "|"
	case tokAndIf:
		return "&&"
	case tokOrIf:
		return "||"
	case tokSemi:
		return ";"
	}
	return "?"
}

// isLeadingAssignment reports whether word looks like the
// "NAME=VALUE" or "NAME+=VALUE" prefix bash / zsh treat as a
// one-shot environment assignment ahead of a command. NAME must
// start with a letter or '_' and contain only [A-Za-z0-9_]
// before the assignment operator. Both forms are rejected so
// "X+=1 curl http://x" (which the shell still parses as a
// leading assignment and runs curl) cannot smuggle "X+=1" past
// the policy as a bareword.
func isLeadingAssignment(word string) bool {
	if word == "" {
		return false
	}
	if !isIdentStart(word[0]) {
		return false
	}
	for i := 1; i < len(word); i++ {
		c := word[i]
		if c == '=' {
			return true
		}
		if c == '+' && i+1 < len(word) && word[i+1] == '=' {
			return true
		}
		if !isIdentCont(c) {
			return false
		}
	}
	return false
}

func isIdentStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		c == '_'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
