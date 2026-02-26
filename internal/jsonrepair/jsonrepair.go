//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package jsonrepair provides a pluggable hook for repairing malformed JSON strings.
package jsonrepair

import (
	"bytes"
	"encoding/json"
)

// Repair repairs JSON using a non-streaming parser.
func Repair(input []byte) ([]byte, error) {
	repaired, err := repairRegular(string(input))
	if err != nil {
		return nil, err
	}
	return []byte(repaired), nil
}

type regularParser struct {
	text   []rune
	i      int
	output []rune
	err    *Error
}

// repairRegular performs JSON repair using a non-streaming parser.
func repairRegular(text string) (out string, err error) {
	p := &regularParser{text: []rune(text)}
	defer func() {
		err = recoverRegularParserError(p, recover(), err)
	}()
	p.parseMarkdownCodeBlock([]string{"```", "[```", "{```"})
	processed := p.parseValue()
	if p.err != nil {
		return "", p.err
	}
	if !processed {
		return "", &Error{Message: "Unexpected end of json string", Position: len(p.text)}
	}
	p.parseMarkdownCodeBlock([]string{"```", "```]", "```}"})
	processedComma := p.parseCharacter(',')
	if processedComma {
		p.parseWhitespaceAndSkipComments(true)
	}
	if p.i < len(p.text) && isStartOfValue(p.text[p.i]) && endsWithCommaOrNewline(p.output) {
		if !processedComma {
			p.output = insertBeforeLastWhitespace(p.output, []rune{','})
		}
		p.parseNewlineDelimitedJSON()
	} else if processedComma {
		p.output = stripLastOccurrence(p.output, ',', false)
	}
	for p.i < len(p.text) && (p.text[p.i] == '}' || p.text[p.i] == ']') {
		p.i++
		p.parseWhitespaceAndSkipComments(true)
	}
	if p.i >= len(p.text) {
		return string(p.output), nil
	}
	return "", &Error{Message: "Unexpected character " + jsonStringify(string(p.text[p.i])), Position: p.i}
}

// recoverRegularParserError converts a recovered panic into an Error.
func recoverRegularParserError(p *regularParser, recovered any, err error) error {
	if recovered == nil {
		return err
	}
	position := min(max(p.i, 0), len(p.text))
	return &Error{Message: "Unexpected error", Position: position}
}

// parseValue parses a JSON value and appends the repaired output.
func (p *regularParser) parseValue() bool {
	p.parseWhitespaceAndSkipComments(true)
	if p.err != nil {
		return false
	}
	processed := p.parseObject()
	if !processed {
		processed = p.parseArray()
	}
	if !processed {
		processed = p.parseString(false, -1)
	}
	if !processed {
		processed = p.parseNumber()
	}
	if !processed {
		processed = p.parseKeywords()
	}
	if !processed {
		processed = p.parseUnquotedString(false)
	}
	if !processed {
		processed = p.parseRegex()
	}
	p.parseWhitespaceAndSkipComments(true)
	if p.err != nil {
		return false
	}
	return processed
}

// parseWhitespaceAndSkipComments consumes whitespace and comments and appends normalized whitespace to the output.
func (p *regularParser) parseWhitespaceAndSkipComments(skipNewline bool) bool {
	start := p.i
	p.parseWhitespace(skipNewline)
	for p.parseComment() {
		// Stop when there is no whitespace after a comment to avoid consuming consecutive comments without a separator.
		if !p.parseWhitespace(skipNewline) {
			break
		}
	}
	return p.i > start
}

// parseWhitespace consumes whitespace and appends it to the output.
func (p *regularParser) parseWhitespace(skipNewline bool) bool {
	start := p.i
	for p.i < len(p.text) {
		char := p.text[p.i]
		if skipNewline {
			if isWhitespace(char) {
				p.output = append(p.output, char)
				p.i++
				continue
			}
		} else if isWhitespaceExceptNewline(char) {
			p.output = append(p.output, char)
			p.i++
			continue
		}
		if isSpecialWhitespace(char) {
			p.output = append(p.output, ' ')
			p.i++
			continue
		}
		break
	}
	return p.i > start
}

// parseComment skips a block comment or line comment.
func (p *regularParser) parseComment() bool {
	if p.i+1 < len(p.text) && p.text[p.i] == '/' && p.text[p.i+1] == '*' {
		for p.i < len(p.text) && !p.atEndOfBlockComment(p.i) {
			p.i++
		}
		p.i = min(p.i+2, len(p.text))
		return true
	}
	if p.i+1 < len(p.text) && p.text[p.i] == '/' && p.text[p.i+1] == '/' {
		for p.i < len(p.text) && p.text[p.i] != '\n' {
			p.i++
		}
		return true
	}
	return false
}

// parseMarkdownCodeBlock strips a Markdown code fence and an optional language specifier.
func (p *regularParser) parseMarkdownCodeBlock(blocks []string) bool {
	if p.skipMarkdownCodeBlock(blocks) {
		if p.i < len(p.text) && isFunctionNameCharStart(p.text[p.i]) {
			for p.i < len(p.text) && isFunctionNameChar(p.text[p.i]) {
				p.i++
			}
		}
		p.parseWhitespaceAndSkipComments(true)
		return true
	}
	return false
}

// skipMarkdownCodeBlock skips a Markdown fence marker when it matches one of the given blocks.
func (p *regularParser) skipMarkdownCodeBlock(blocks []string) bool {
	p.parseWhitespace(true)
	for _, block := range blocks {
		runes := []rune(block)
		end := p.i + len(runes)
		if end <= len(p.text) && string(p.text[p.i:end]) == block {
			p.i = end
			return true
		}
	}
	return false
}

// parseCharacter consumes char when present and appends it to the output.
func (p *regularParser) parseCharacter(char rune) bool {
	if p.i < len(p.text) && p.text[p.i] == char {
		p.output = append(p.output, char)
		p.i++
		return true
	}
	return false
}

// skipCharacter consumes char when present without writing it to the output.
func (p *regularParser) skipCharacter(char rune) bool {
	if p.i < len(p.text) && p.text[p.i] == char {
		p.i++
		return true
	}
	return false
}

// skipEscapeCharacter skips a backslash escape prefix after a string repair.
func (p *regularParser) skipEscapeCharacter() bool {
	return p.skipCharacter('\\')
}

// skipEllipsis skips an ellipsis token and an optional trailing comma.
func (p *regularParser) skipEllipsis() bool {
	p.parseWhitespaceAndSkipComments(true)
	if p.i+2 < len(p.text) && p.text[p.i] == '.' && p.text[p.i+1] == '.' && p.text[p.i+2] == '.' {
		p.i += 3
		p.parseWhitespaceAndSkipComments(true)
		p.skipCharacter(',')
		return true
	}
	return false
}

// parseObject parses a JSON object and repairs common syntax issues.
func (p *regularParser) parseObject() bool {
	if p.err != nil {
		return false
	}
	if p.i >= len(p.text) || p.text[p.i] != '{' {
		return false
	}
	p.output = append(p.output, '{')
	p.i++
	p.parseWhitespaceAndSkipComments(true)
	p.skipLeadingObjectComma()

	initial := true
	for p.i < len(p.text) && p.text[p.i] != '}' {
		if !p.parseObjectMember(&initial) {
			break
		}
	}
	p.finishObject()
	return true
}

// skipLeadingObjectComma removes a leading comma inside an object.
func (p *regularParser) skipLeadingObjectComma() {
	if p.skipCharacter(',') {
		p.parseWhitespaceAndSkipComments(true)
	}
}

// parseObjectMember parses a single object member and repairs missing separators when possible.
func (p *regularParser) parseObjectMember(initial *bool) bool {
	p.parseObjectMemberSeparator(initial)
	p.skipEllipsis()

	processedKey := p.parseString(false, -1) || p.parseUnquotedString(true)
	if p.err != nil {
		return false
	}
	if !processedKey {
		p.handleMissingObjectKey()
		return false
	}

	p.parseWhitespaceAndSkipComments(true)
	processedColon, truncatedText := p.ensureObjectColon()
	if p.err != nil {
		return false
	}

	processedValue := p.parseValue()
	if p.err != nil {
		return false
	}
	if !processedValue {
		p.handleMissingObjectValue(processedColon, truncatedText)
	}

	return p.err == nil
}

// parseObjectMemberSeparator parses or repairs the comma between object members.
func (p *regularParser) parseObjectMemberSeparator(initial *bool) {
	if *initial {
		*initial = false
		return
	}
	if !p.parseCharacter(',') {
		p.output = insertBeforeLastWhitespace(p.output, []rune{','})
	}
	p.parseWhitespaceAndSkipComments(true)
}

// handleMissingObjectKey repairs a missing object key when possible or sets an error.
func (p *regularParser) handleMissingObjectKey() {
	if p.i >= len(p.text) || isObjectKeyTermination(p.text[p.i]) {
		p.output = stripLastOccurrence(p.output, ',', false)
		return
	}
	p.setError("Object key expected", p.i)
}

// isObjectKeyTermination reports whether char terminates an object key.
func isObjectKeyTermination(char rune) bool {
	switch char {
	case '}', '{', ']', '[':
		return true
	default:
		return false
	}
}

// ensureObjectColon parses or repairs the colon between an object key and value.
func (p *regularParser) ensureObjectColon() (processedColon bool, truncatedText bool) {
	processedColon = p.parseCharacter(':')
	truncatedText = p.i >= len(p.text)
	if processedColon {
		return processedColon, truncatedText
	}
	if truncatedText || (p.i < len(p.text) && isStartOfValue(p.text[p.i])) {
		p.output = insertBeforeLastWhitespace(p.output, []rune{':'})
		return processedColon, truncatedText
	}
	p.setError("Colon expected", p.i)
	return processedColon, truncatedText
}

// handleMissingObjectValue repairs a missing object value when possible or sets an error.
func (p *regularParser) handleMissingObjectValue(processedColon bool, truncatedText bool) {
	if processedColon || truncatedText {
		p.output = append(p.output, []rune("null")...)
		return
	}
	p.setError("Colon expected", p.i)
}

// finishObject appends or repairs a closing brace for an object.
func (p *regularParser) finishObject() {
	if p.i < len(p.text) && p.text[p.i] == '}' {
		p.output = append(p.output, '}')
		p.i++
		return
	}
	p.output = insertBeforeLastWhitespace(p.output, []rune{'}'})
}

// parseArray parses a JSON array and repairs common syntax issues.
func (p *regularParser) parseArray() bool {
	if p.err != nil {
		return false
	}
	if p.i >= len(p.text) || p.text[p.i] != '[' {
		return false
	}
	p.output = append(p.output, '[')
	p.i++
	p.parseWhitespaceAndSkipComments(true)
	if p.skipCharacter(',') {
		p.parseWhitespaceAndSkipComments(true)
	}
	initial := true
	for p.i < len(p.text) && p.text[p.i] != ']' {
		if !initial {
			if !p.parseCharacter(',') {
				p.output = insertBeforeLastWhitespace(p.output, []rune{','})
			}
		} else {
			initial = false
		}
		p.skipEllipsis()
		processedValue := p.parseValue()
		if p.err != nil {
			return true
		}
		if !processedValue {
			p.output = stripLastOccurrence(p.output, ',', false)
			break
		}
	}
	if p.i < len(p.text) && p.text[p.i] == ']' {
		p.output = append(p.output, ']')
		p.i++
	} else {
		p.output = insertBeforeLastWhitespace(p.output, []rune{']'})
	}
	return true
}

// parseNewlineDelimitedJSON repairs multiple root values by wrapping them into an array.
func (p *regularParser) parseNewlineDelimitedJSON() {
	initial := true
	processedValue := true
	for processedValue {
		if !initial {
			if !p.parseCharacter(',') {
				p.output = insertBeforeLastWhitespace(p.output, []rune{','})
			}
		} else {
			initial = false
		}
		processedValue = p.parseValue()
	}
	if !processedValue {
		p.output = stripLastOccurrence(p.output, ',', false)
	}
	p.output = append([]rune{'[', '\n'}, append(p.output, '\n', ']')...)
}

// parseString parses and repairs a quoted string value.
func (p *regularParser) parseString(stopAtDelimiter bool, stopAtIndex int) bool {
	if p.err != nil {
		return false
	}
	skipEscapeChars := false
	if p.i < len(p.text) && p.text[p.i] == '\\' {
		skipEscapeChars = true
		p.i++
	}
	if p.i >= len(p.text) || !isQuote(p.text[p.i]) {
		return false
	}

	startQuote := p.text[p.i]
	isEndQuote := endQuoteMatcher(startQuote)

	iBefore := p.i
	outputBefore := len(p.output)
	str := []rune{'"'}
	p.i++

	for {
		if p.i >= len(p.text) {
			return p.handleStringEOF(&str, stopAtDelimiter, iBefore, outputBefore)
		}
		if stopAtIndex != -1 && p.i == stopAtIndex {
			p.closeString(&str)
			return true
		}

		char := p.text[p.i]
		if isEndQuote(char) {
			done, processed := p.handleStringEndQuote(&str, stopAtDelimiter, iBefore, outputBefore)
			if done {
				return processed
			}
			continue
		}
		if stopAtDelimiter && isUnquotedStringDelimiter(char) {
			p.handleStringStopAtDelimiter(&str, iBefore)
			return true
		}
		if char == '\\' {
			if !p.consumeStringEscape(&str) {
				return false
			}
		} else if !p.consumeStringChar(&str) {
			return false
		}

		if skipEscapeChars {
			p.skipEscapeCharacter()
		}
	}
}

type quoteMatcher func(rune) bool

// endQuoteMatcher returns a matcher for the end quote based on startQuote.
func endQuoteMatcher(startQuote rune) quoteMatcher {
	if isDoubleQuote(startQuote) {
		return isDoubleQuote
	}
	if isSingleQuote(startQuote) {
		return isSingleQuote
	}
	if isSingleQuoteLike(startQuote) {
		return isSingleQuoteLike
	}
	return isDoubleQuoteLike
}

// handleStringEOF repairs a string when the input ends before finding an end quote.
func (p *regularParser) handleStringEOF(str *[]rune, stopAtDelimiter bool, iBefore int, outputBefore int) bool {
	iPrev := p.prevNonWhitespaceIndex(p.i - 1)
	if p.shouldRestartStringAtEOF(stopAtDelimiter, iPrev) {
		p.i = iBefore
		p.output = p.output[:outputBefore]
		return p.parseString(true, -1)
	}
	p.closeString(str)
	return true
}

// shouldRestartStringAtEOF reports whether the string parser should retry in delimiter mode.
func (p *regularParser) shouldRestartStringAtEOF(stopAtDelimiter bool, iPrev int) bool {
	if stopAtDelimiter {
		return false
	}
	if iPrev < 0 || iPrev >= len(p.text) {
		return false
	}
	return isDelimiter(p.text[iPrev])
}

// closeString closes the current string buffer and writes it to the output.
func (p *regularParser) closeString(str *[]rune) {
	*str = insertBeforeLastWhitespace(*str, []rune{'"'})
	p.output = append(p.output, (*str)...)
}

// handleStringEndQuote validates and repairs an encountered end quote inside a string.
func (p *regularParser) handleStringEndQuote(str *[]rune, stopAtDelimiter bool, iBefore int, outputBefore int) (done bool, processed bool) {
	iQuote := p.i
	quoteIndex := len(*str)

	*str = append(*str, '"')
	p.i++
	p.output = append(p.output, (*str)...)

	p.parseWhitespaceAndSkipComments(false)
	if p.shouldStopAfterStringEndQuote(stopAtDelimiter) {
		p.parseConcatenatedString()
		return true, true
	}

	iPrevChar := p.prevNonWhitespaceIndex(iQuote - 1)
	prevChar := p.charAt(iPrevChar)
	if prevChar == ',' {
		p.i = iBefore
		p.output = p.output[:outputBefore]
		return true, p.parseString(false, iPrevChar)
	}
	if isDelimiter(prevChar) {
		p.i = iBefore
		p.output = p.output[:outputBefore]
		return true, p.parseString(true, -1)
	}

	p.output = p.output[:outputBefore]
	p.i = iQuote + 1
	*str = insertAtIndex(*str, quoteIndex, '\\')
	return false, false
}

// insertAtIndex inserts char into text at the given index.
func insertAtIndex(text []rune, index int, char rune) []rune {
	if index < 0 || index > len(text) {
		return text
	}
	out := make([]rune, 0, len(text)+1)
	out = append(out, text[:index]...)
	out = append(out, char)
	out = append(out, text[index:]...)
	return out
}

// shouldStopAfterStringEndQuote reports whether the current quote can terminate the string.
func (p *regularParser) shouldStopAfterStringEndQuote(stopAtDelimiter bool) bool {
	if stopAtDelimiter {
		return true
	}
	if p.i >= len(p.text) {
		return true
	}
	next := p.text[p.i]
	if isDelimiter(next) {
		return true
	}
	if isQuote(next) {
		return true
	}
	return isDigit(next)
}

// charAt returns the rune at index i or zero when out of range.
func (p *regularParser) charAt(i int) rune {
	if i < 0 || i >= len(p.text) {
		return 0
	}
	return p.text[i]
}

// handleStringStopAtDelimiter finalizes a string when stopping at the first delimiter.
func (p *regularParser) handleStringStopAtDelimiter(str *[]rune, iBefore int) {
	p.extendStringWithURLIfNeeded(str, iBefore)
	p.closeString(str)
	p.parseConcatenatedString()
}

// extendStringWithURLIfNeeded extends a string with URL characters when it likely contains a URL.
func (p *regularParser) extendStringWithURLIfNeeded(str *[]rune, iBefore int) {
	if p.i-1 < 0 || p.text[p.i-1] != ':' {
		return
	}

	start := iBefore + 1
	end := min(p.i+2, len(p.text))
	if start >= len(p.text) {
		return
	}
	if !isURLStart(string(p.text[start:end])) {
		return
	}
	for p.i < len(p.text) && isURLChar(p.text[p.i]) {
		*str = append(*str, p.text[p.i])
		p.i++
	}
}

// consumeStringEscape consumes an escape sequence inside a string and appends its repaired form to str.
func (p *regularParser) consumeStringEscape(str *[]rune) bool {
	if p.i+1 >= len(p.text) {
		p.i = len(p.text)
		return true
	}

	char := p.text[p.i+1]
	switch char {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		*str = append(*str, p.text[p.i], char)
		p.i += 2
		return true
	case 'u':
		return p.consumeUnicodeEscape(str)
	default:
		*str = append(*str, char)
		p.i += 2
		return true
	}
}

// consumeUnicodeEscape consumes a Unicode escape sequence and sets an error on invalid input.
func (p *regularParser) consumeUnicodeEscape(str *[]rune) bool {
	j := 2
	for j < 6 && p.i+j < len(p.text) && isHex(p.text[p.i+j]) {
		j++
	}
	if j == 6 {
		*str = append(*str, p.text[p.i:p.i+6]...)
		p.i += 6
		return true
	}
	if p.i+j >= len(p.text) {
		p.i = len(p.text)
		return true
	}
	end := min(p.i+6, len(p.text))
	chars := string(p.text[p.i:end])
	p.setError("Invalid unicode character \""+chars+"\"", p.i)
	return false
}

// consumeStringChar consumes one character inside a string and repairs invalid characters when possible.
func (p *regularParser) consumeStringChar(str *[]rune) bool {
	char := p.text[p.i]
	if char == '"' && p.i > 0 && p.text[p.i-1] != '\\' {
		*str = append(*str, '\\', char)
		p.i++
		return true
	}
	if isControlCharacter(char) {
		*str = append(*str, []rune(escapeControlCharacter(char))...)
		p.i++
		return true
	}
	if !isValidStringCharacter(char) {
		p.setError("Invalid character "+jsonStringify(string(char)), p.i)
		return false
	}
	*str = append(*str, char)
	p.i++
	return true
}

// parseConcatenatedString repairs concatenated string expressions joined with '+'.
func (p *regularParser) parseConcatenatedString() bool {
	if p.err != nil {
		return false
	}
	processed := false
	p.parseWhitespaceAndSkipComments(true)
	for p.i < len(p.text) && p.text[p.i] == '+' {
		processed = true
		p.i++
		p.parseWhitespaceAndSkipComments(true)
		p.output = stripLastOccurrence(p.output, '"', true)
		start := len(p.output)
		parsedStr := p.parseString(false, -1)
		if p.err != nil {
			return processed
		}
		if parsedStr {
			p.output = removeAtIndex(p.output, start, 1)
		} else {
			p.output = insertBeforeLastWhitespace(p.output, []rune{'"'})
		}
	}
	return processed
}

// parseNumber parses and repairs a JSON number token.
func (p *regularParser) parseNumber() bool {
	if p.err != nil {
		return false
	}
	start := p.i
	if repaired, ok := p.parseNumberLeadingSign(start); repaired || !ok {
		return repaired
	}
	p.consumeDigits()

	if repaired, ok := p.parseNumberFraction(start); repaired || !ok {
		return repaired
	}
	if repaired, ok := p.parseNumberExponent(start); repaired || !ok {
		return repaired
	}
	if !p.atEndOfNumber() {
		p.i = start
		return false
	}
	if p.i <= start {
		return false
	}
	p.appendNumberRunes(start, p.i)
	return true
}

// parseNumberLeadingSign parses an optional leading '-' and repairs truncated numbers.
func (p *regularParser) parseNumberLeadingSign(start int) (repaired bool, ok bool) {
	if p.i >= len(p.text) || p.text[p.i] != '-' {
		return false, true
	}

	p.i++
	if p.atEndOfNumber() {
		p.repairNumberEndingWithNumericSymbol(start)
		return true, true
	}
	if p.i >= len(p.text) || !isDigit(p.text[p.i]) {
		p.i = start
		return false, false
	}
	return false, true
}

// consumeDigits consumes consecutive digit runes.
func (p *regularParser) consumeDigits() {
	for p.i < len(p.text) && isDigit(p.text[p.i]) {
		p.i++
	}
}

// parseNumberFraction parses the fractional part of a number and repairs truncation.
func (p *regularParser) parseNumberFraction(start int) (repaired bool, ok bool) {
	if p.i >= len(p.text) || p.text[p.i] != '.' {
		return false, true
	}

	p.i++
	if p.atEndOfNumber() {
		p.repairNumberEndingWithNumericSymbol(start)
		return true, true
	}
	if p.i >= len(p.text) || !isDigit(p.text[p.i]) {
		p.i = start
		return false, false
	}
	p.consumeDigits()
	return false, true
}

// parseNumberExponent parses the exponent part of a number and repairs truncation.
func (p *regularParser) parseNumberExponent(start int) (repaired bool, ok bool) {
	if p.i >= len(p.text) {
		return false, true
	}
	if p.text[p.i] != 'e' && p.text[p.i] != 'E' {
		return false, true
	}

	p.i++
	p.skipNumberExponentSign()
	if p.atEndOfNumber() {
		p.repairNumberEndingWithNumericSymbol(start)
		return true, true
	}
	if p.i >= len(p.text) || !isDigit(p.text[p.i]) {
		p.i = start
		return false, false
	}
	p.consumeDigits()
	return false, true
}

// skipNumberExponentSign consumes an optional exponent sign.
func (p *regularParser) skipNumberExponentSign() {
	if p.i >= len(p.text) {
		return
	}
	if p.text[p.i] == '-' || p.text[p.i] == '+' {
		p.i++
	}
}

// appendNumberRunes appends the parsed number to the output and quotes invalid leading-zero numbers.
func (p *regularParser) appendNumberRunes(start int, end int) {
	numRunes := p.text[start:end]
	if p.hasInvalidLeadingZero(numRunes) {
		p.output = append(p.output, []rune(jsonStringify(string(numRunes)))...)
		return
	}
	p.output = append(p.output, numRunes...)
}

// hasInvalidLeadingZero reports whether numRunes begins with a leading zero followed by a digit.
func (p *regularParser) hasInvalidLeadingZero(numRunes []rune) bool {
	if len(numRunes) < 2 {
		return false
	}
	if numRunes[0] != '0' {
		return false
	}
	return isDigit(numRunes[1])
}

// parseKeywords parses and repairs keyword tokens like true, false, and null.
func (p *regularParser) parseKeywords() bool {
	if p.err != nil {
		return false
	}
	return p.parseKeyword("true", "true") ||
		p.parseKeyword("false", "false") ||
		p.parseKeyword("null", "null") ||
		p.parseKeyword("True", "true") ||
		p.parseKeyword("False", "false") ||
		p.parseKeyword("None", "null")
}

// parseKeyword matches a keyword and appends its normalized value to the output.
func (p *regularParser) parseKeyword(name string, value string) bool {
	if p.err != nil {
		return false
	}
	runes := []rune(name)
	end := p.i + len(runes)
	if end <= len(p.text) && string(p.text[p.i:end]) == name {
		p.output = append(p.output, []rune(value)...)
		p.i = end
		return true
	}
	return false
}

// parseUnquotedString repairs an unquoted token by quoting it or parsing a function-like wrapper.
func (p *regularParser) parseUnquotedString(isKey bool) bool {
	if p.err != nil {
		return false
	}
	start := p.i
	if p.parseUnquotedFunctionCall() {
		return true
	}

	p.scanUnquotedString(isKey)
	p.extendUnquotedStringWithURL(start)
	if p.i <= start {
		return false
	}
	p.trimTrailingWhitespace()
	p.appendUnquotedSymbol(start)
	p.skipMissingStartQuote()
	return true
}

// parseUnquotedFunctionCall parses a function-like wrapper and returns true when it was handled.
func (p *regularParser) parseUnquotedFunctionCall() bool {
	if p.i >= len(p.text) || !isFunctionNameCharStart(p.text[p.i]) {
		return false
	}

	for p.i < len(p.text) && isFunctionNameChar(p.text[p.i]) {
		p.i++
	}

	nameEnd := p.i
	openParenIndex := p.nextNonWhitespaceIndex(nameEnd)
	if openParenIndex >= len(p.text) || p.text[openParenIndex] != '(' {
		p.i = nameEnd
		return false
	}

	p.i = openParenIndex + 1
	p.parseValue()
	if p.err != nil {
		return true
	}
	p.skipFunctionCallEnd()
	return true
}

// nextNonWhitespaceIndex returns the next index at or after start that is not whitespace.
func (p *regularParser) nextNonWhitespaceIndex(start int) int {
	i := start
	for i < len(p.text) && isWhitespace(p.text[i]) {
		i++
	}
	return i
}

// skipFunctionCallEnd skips a closing ')' and an optional trailing ';'.
func (p *regularParser) skipFunctionCallEnd() {
	if p.i >= len(p.text) || p.text[p.i] != ')' {
		return
	}
	p.i++
	if p.i < len(p.text) && p.text[p.i] == ';' {
		p.i++
	}
}

// scanUnquotedString advances the cursor until the end of the current unquoted token.
func (p *regularParser) scanUnquotedString(isKey bool) {
	for p.i < len(p.text) && p.shouldContinueUnquotedStringScan(isKey) {
		p.i++
	}
}

// shouldContinueUnquotedStringScan reports whether the unquoted string scan should continue.
func (p *regularParser) shouldContinueUnquotedStringScan(isKey bool) bool {
	char := p.text[p.i]
	if isUnquotedStringDelimiter(char) {
		return false
	}
	if isQuote(char) {
		return false
	}
	if isKey && char == ':' {
		return false
	}
	return true
}

// extendUnquotedStringWithURL extends the token when it matches a URL prefix.
func (p *regularParser) extendUnquotedStringWithURL(start int) {
	if p.i <= start || p.text[p.i-1] != ':' {
		return
	}
	end := min(p.i+2, len(p.text))
	if !isURLStart(string(p.text[start:end])) {
		return
	}
	for p.i < len(p.text) && isURLChar(p.text[p.i]) {
		p.i++
	}
}

// trimTrailingWhitespace rewinds p.i to remove trailing whitespace from the current token.
func (p *regularParser) trimTrailingWhitespace() {
	for p.i > 0 && isWhitespace(p.text[p.i-1]) {
		p.i--
	}
}

// appendUnquotedSymbol appends the current unquoted token to the output, repairing undefined to null.
func (p *regularParser) appendUnquotedSymbol(start int) {
	symbol := string(p.text[start:p.i])
	if symbol == "undefined" {
		p.output = append(p.output, []rune("null")...)
		return
	}
	p.output = append(p.output, []rune(jsonStringify(symbol))...)
}

// skipMissingStartQuote skips an end quote when the start quote was missing.
func (p *regularParser) skipMissingStartQuote() {
	if p.i < len(p.text) && p.text[p.i] == '"' {
		p.i++
	}
}

// parseRegex repairs a regular expression literal by turning it into a quoted string.
func (p *regularParser) parseRegex() bool {
	if p.err != nil {
		return false
	}
	if p.i >= len(p.text) || p.text[p.i] != '/' {
		return false
	}
	start := p.i
	p.i++
	for p.i < len(p.text) && (p.text[p.i] != '/' || p.text[p.i-1] == '\\') {
		p.i++
	}
	end := min(p.i+1, len(p.text))
	p.i = end
	p.output = append(p.output, []rune(jsonStringify(string(p.text[start:end])))...)
	return true
}

// prevNonWhitespaceIndex returns the previous index at or before start that is not whitespace.
func (p *regularParser) prevNonWhitespaceIndex(start int) int {
	prev := start
	for prev > 0 && prev < len(p.text) && isWhitespace(p.text[prev]) {
		prev--
	}
	return prev
}

// atEndOfNumber reports whether the current cursor is at the end of a number token.
func (p *regularParser) atEndOfNumber() bool {
	return p.i >= len(p.text) || isDelimiter(p.text[p.i]) || isWhitespace(p.text[p.i])
}

// repairNumberEndingWithNumericSymbol appends a trailing zero to complete a truncated number token.
func (p *regularParser) repairNumberEndingWithNumericSymbol(start int) {
	p.output = append(p.output, p.text[start:p.i]...)
	p.output = append(p.output, '0')
}

// escapeControlCharacter returns the escaped representation of a control character.
func escapeControlCharacter(char rune) string {
	switch char {
	case '\b':
		return "\\b"
	case '\f':
		return "\\f"
	case '\n':
		return "\\n"
	case '\r':
		return "\\r"
	case '\t':
		return "\\t"
	default:
		return string(char)
	}
}

// jsonStringify returns a JSON-encoded string value without a trailing newline.
func jsonStringify(value string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// setError sets the parser error once.
func (p *regularParser) setError(message string, position int) {
	if p.err != nil {
		return
	}
	p.err = &Error{Message: message, Position: position}
}

// atEndOfBlockComment reports whether i points at the end of a block comment terminator.
func (p *regularParser) atEndOfBlockComment(i int) bool {
	return i+1 < len(p.text) && p.text[i] == '*' && p.text[i+1] == '/'
}
