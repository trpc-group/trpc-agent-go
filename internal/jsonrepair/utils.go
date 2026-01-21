//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import "strings"

// isHex reports whether the rune is a hexadecimal digit.
func isHex(char rune) bool {
	return ('0' <= char && char <= '9') || ('A' <= char && char <= 'F') || ('a' <= char && char <= 'f')
}

// isDigit reports whether the rune is an ASCII digit.
func isDigit(char rune) bool {
	return '0' <= char && char <= '9'
}

// isValidStringCharacter reports whether the rune is allowed unescaped in a JSON string.
func isValidStringCharacter(char rune) bool {
	return char >= 0x20
}

// isDelimiter reports whether the rune is treated as a delimiter by the parser.
func isDelimiter(char rune) bool {
	switch char {
	case ',', ':', '[', ']', '/', '{', '}', '(', ')', '\n', '+':
		return true
	default:
		return false
	}
}

// isFunctionNameCharStart reports whether the rune can start an identifier.
func isFunctionNameCharStart(char rune) bool {
	return ('a' <= char && char <= 'z') || ('A' <= char && char <= 'Z') || char == '_' || char == '$'
}

// isFunctionNameChar reports whether the rune can be part of an identifier.
func isFunctionNameChar(char rune) bool {
	return isFunctionNameCharStart(char) || isDigit(char)
}

// isURLStart reports whether the text ends with a supported scheme prefix ending in "://".
func isURLStart(text string) bool {
	if !strings.HasSuffix(text, "://") {
		return false
	}
	switch strings.TrimSuffix(text, "://") {
	case "http", "https", "ftp", "mailto", "file", "data", "irc":
		return true
	default:
		return false
	}
}

// isURLChar reports whether the rune is allowed inside a URL token.
func isURLChar(char rune) bool {
	if ('a' <= char && char <= 'z') || ('A' <= char && char <= 'Z') || isDigit(char) {
		return true
	}
	switch char {
	case '-', '.', '_', '~', ':', '/', '?', '#', '@', '!', '$', '&', '\'', '(', ')', '*', '+', ';', '=':
		return true
	default:
		return false
	}
}

// isUnquotedStringDelimiter reports whether the rune ends an unquoted string token.
func isUnquotedStringDelimiter(char rune) bool {
	switch char {
	case ',', '[', ']', '/', '{', '}', '\n', '+':
		return true
	default:
		return false
	}
}

// isStartOfValue reports whether the rune can start a JSON value.
func isStartOfValue(char rune) bool {
	if isQuote(char) {
		return true
	}
	return char == '[' || char == '{' || char == '-' || isDigit(char) || ('a' <= char && char <= 'z') || ('A' <= char && char <= 'Z') || char == '_'
}

// isControlCharacter reports whether the rune is a control character that must be escaped.
func isControlCharacter(char rune) bool {
	switch char {
	case '\n', '\r', '\t', '\b', '\f':
		return true
	default:
		return false
	}
}

// isWhitespace reports whether the rune is treated as whitespace.
func isWhitespace(char rune) bool {
	switch char {
	case ' ', '\n', '\t', '\r':
		return true
	default:
		return false
	}
}

// isWhitespaceExceptNewline reports whether the rune is whitespace excluding newline.
func isWhitespaceExceptNewline(char rune) bool {
	switch char {
	case ' ', '\t', '\r':
		return true
	default:
		return false
	}
}

// isSpecialWhitespace reports whether the rune is a special whitespace character normalized to a space.
func isSpecialWhitespace(char rune) bool {
	code := int(char)
	return code == 0xa0 ||
		(0x2000 <= code && code <= 0x200a) ||
		code == 0x202f ||
		code == 0x205f ||
		code == 0x3000
}

// isQuote reports whether the rune is a supported quote character.
func isQuote(char rune) bool {
	return isDoubleQuoteLike(char) || isSingleQuoteLike(char)
}

// isDoubleQuoteLike reports whether the rune is a double-quote-like character.
func isDoubleQuoteLike(char rune) bool {
	return char == '"' || char == 0x201c || char == 0x201d
}

// isDoubleQuote reports whether the rune is a double quote character.
func isDoubleQuote(char rune) bool {
	return char == '"'
}

// isSingleQuoteLike reports whether the rune is a single-quote-like character.
func isSingleQuoteLike(char rune) bool {
	return char == '\'' || char == 0x2018 || char == 0x2019 || char == 0x0060 || char == 0x00b4
}

// isSingleQuote reports whether the rune is a single quote character.
func isSingleQuote(char rune) bool {
	return char == '\''
}

// stripLastOccurrence removes the last occurrence of char from text.
func stripLastOccurrence(text []rune, char rune, stripRemainingText bool) []rune {
	index := -1
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == char {
			index = i
			break
		}
	}
	if index == -1 {
		return text
	}
	if stripRemainingText {
		return text[:index]
	}
	return append(text[:index], text[index+1:]...)
}

// insertBeforeLastWhitespace inserts runes before any trailing whitespace in the text.
func insertBeforeLastWhitespace(text []rune, insert []rune) []rune {
	index := len(text)
	if index == 0 || !isWhitespace(text[index-1]) {
		return append(text, insert...)
	}
	for index > 0 && isWhitespace(text[index-1]) {
		index--
	}
	out := make([]rune, 0, len(text)+len(insert))
	out = append(out, text[:index]...)
	out = append(out, insert...)
	out = append(out, text[index:]...)
	return out
}

// removeAtIndex removes count runes from text starting at start.
func removeAtIndex(text []rune, start int, count int) []rune {
	if start < 0 || count < 0 || start+count > len(text) {
		return text
	}
	return append(text[:start], text[start+count:]...)
}

// endsWithCommaOrNewline reports whether text ends with a comma or newline, ignoring trailing spaces.
func endsWithCommaOrNewline(text []rune) bool {
	i := len(text) - 1
	for i >= 0 {
		switch text[i] {
		case ' ', '\t', '\r':
			i--
		default:
			if text[i] == ',' || text[i] == '\n' {
				return true
			}
			return false
		}
	}
	return false
}
