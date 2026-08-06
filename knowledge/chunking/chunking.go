//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Strategy defines the interface for document chunking strategies.
type Strategy interface {
	// Chunk splits a document into smaller chunks based on the strategy's algorithm.
	Chunk(doc *document.Document) ([]*document.Document, error)
}

var (
	defaultChunkSize = 1024
	defaultOverlap   = 0
)

func validateChunkConfig(chunkSize, overlap int) error {
	switch {
	case chunkSize <= 0:
		return ErrInvalidChunkSize
	case overlap < 0:
		return ErrInvalidOverlap
	case overlap >= chunkSize:
		return ErrOverlapTooLarge
	default:
		return nil
	}
}

// cleanText normalizes encoding and line breaks while preserving source
// whitespace.
func cleanText(content string) string {
	return cleanTextWithWhitespaceTrimming(content, false)
}

func cleanTextWithWhitespaceTrimming(
	content string,
	trimWhitespace bool,
) string {
	// Intelligently process text based on detected encoding
	processed, encodingInfo := encoding.SmartProcessText(content)

	// Log encoding information for debugging.
	if encodingInfo.Encoding != encoding.EncodingUTF8 || !encodingInfo.IsValid {
		log.Debugf("Text encoding detected: %s (confidence: %.2f, valid: %v)",
			encodingInfo.Encoding, encodingInfo.Confidence, encodingInfo.IsValid)
	}

	if trimWhitespace {
		// Preserve the whitespace normalization used before indentation became
		// source content by default.
		processed = strings.TrimSpace(processed)
	}

	// Normalize line breaks.
	processed = strings.ReplaceAll(processed, "\r\n", "\n")
	processed = strings.ReplaceAll(processed, "\r", "\n")

	if trimWhitespace {
		lines := strings.Split(processed, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}
		processed = strings.Join(lines, "\n")
	}
	return processed
}

func isBlankText(content string) bool {
	return strings.TrimSpace(content) == ""
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *document.Document, content string, chunkNumber int) *document.Document {
	// Create a copy of the original metadata.
	metadata := make(map[string]any)
	for k, v := range originalDoc.Metadata {
		metadata[k] = v
	}

	// Add chunk-specific metadata.
	metadata[source.MetaChunkIndex] = chunkNumber
	metadata[source.MetaChunkSize] = encoding.RuneCount(content)

	// Generate chunk ID.
	var chunkID string
	if originalDoc.ID != "" {
		chunkID = originalDoc.ID + "_" + strconv.Itoa(chunkNumber)
	} else if originalDoc.Name != "" {
		chunkID = originalDoc.Name + "_" + strconv.Itoa(chunkNumber)
	} else {
		chunkID = "chunk_" + strconv.Itoa(chunkNumber)
	}

	return &document.Document{
		ID:        chunkID,
		Name:      originalDoc.Name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func joinWithOverlap(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
) (string, int) {
	return joinWithOverlapMode(
		previous,
		current,
		maxOverlap,
		maxSize,
		separator,
		false,
	)
}

func joinWithOverlapMode(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
	trimWhitespace bool,
) (string, int) {
	return joinWithOverlapSeparatorMode(
		previous,
		current,
		maxOverlap,
		maxSize,
		separator,
		false,
		trimWhitespace,
	)
}

func joinWithOverlapSeparator(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
	preserveSeparator bool,
) (string, int) {
	return joinWithOverlapSeparatorMode(
		previous,
		current,
		maxOverlap,
		maxSize,
		separator,
		preserveSeparator,
		false,
	)
}

func joinWithOverlapSeparatorMode(
	previous string,
	current string,
	maxOverlap int,
	maxSize int,
	separator string,
	preserveSeparator bool,
	trimWhitespace bool,
) (string, int) {
	currentSize := encoding.RuneCount(current)
	separatorSize := encoding.RuneCount(separator)
	availableWithoutSeparator := maxSize - currentSize
	if maxOverlap <= 0 || availableWithoutSeparator <= 0 {
		return current, 0
	}
	availableOverlap := availableWithoutSeparator - separatorSize
	if availableOverlap <= 0 {
		if preserveSeparator {
			return current, 0
		}
		separator = ""
		availableOverlap = availableWithoutSeparator
	}

	overlapSize := min(
		maxOverlap,
		min(availableOverlap, encoding.RuneCount(previous)),
	)
	if overlapSize <= 0 {
		return current, 0
	}
	overlapContent, naturalBoundary := naturalTextSuffixWithWhitespaceTrimming(
		previous,
		overlapSize,
		trimWhitespace,
	)
	if !naturalBoundary && separator != "" && !preserveSeparator {
		// For an unbroken token, preserve the exact source text rather than
		// inserting a separator in the middle of the token.
		separator = ""
		availableOverlap = availableWithoutSeparator
		overlapSize = min(
			maxOverlap,
			min(availableOverlap, encoding.RuneCount(previous)),
		)
		overlapContent, _ = naturalTextSuffixWithWhitespaceTrimming(
			previous,
			overlapSize,
			trimWhitespace,
		)
	}
	actualOverlap := encoding.RuneCount(overlapContent)
	if actualOverlap <= 0 {
		return current, 0
	}
	return overlapContent + separator + current, actualOverlap
}

func sourceChunkSeparators(
	content string,
	chunks []string,
	fallback string,
) []string {
	separators := make([]string, len(chunks))
	searchFrom := 0
	previousEnd := 0
	for i, chunk := range chunks {
		position := strings.Index(content[searchFrom:], chunk)
		if position < 0 {
			if i > 0 {
				separators[i] = fallback
			}
			continue
		}
		start := searchFrom + position
		if i > 0 {
			separators[i] = sourceGapSeparator(
				content[previousEnd:start],
				fallback,
			)
		}
		previousEnd = start + len(chunk)
		searchFrom = previousEnd
	}
	return separators
}

func sourceGapSeparator(gap string, fallback string) string {
	if gap == "" {
		return ""
	}
	if strings.TrimSpace(gap) != "" {
		return fallback
	}
	if strings.Contains(gap, "\n\n") {
		return "\n\n"
	}
	if strings.ContainsRune(gap, '\n') {
		return "\n"
	}
	return " "
}

// splitTextAtNaturalBoundary splits one prefix from content without exceeding
// maxSize. It prefers line, sentence, punctuation, and whitespace boundaries,
// falling back to an exact rune boundary only when the text has no suitable
// natural boundary.
func splitTextAtNaturalBoundary(content string, maxSize int) (string, string) {
	return splitTextAtNaturalBoundaryWithWhitespaceTrimming(
		content,
		maxSize,
		false,
	)
}

func splitTextAtNaturalBoundaryWithWhitespaceTrimming(
	content string,
	maxSize int,
	trimWhitespace bool,
) (string, string) {
	processed := content
	if trimWhitespace {
		processed = strings.TrimSpace(processed)
	}
	contentRunes := []rune(processed)
	if maxSize <= 0 || len(contentRunes) <= maxSize {
		return processed, ""
	}

	splitPosition := preferredTextBoundary(contentRunes, maxSize)
	prefix := string(contentRunes[:splitPosition])
	remaining := string(contentRunes[splitPosition:])
	if trimWhitespace {
		prefix = strings.TrimSpace(prefix)
		remaining = strings.TrimSpace(remaining)
	}
	return prefix, remaining
}

// splitTextWithBalancedTail avoids leaving a very small final piece when one
// logical block needs multiple chunks. It first keeps the splitter's normal
// boundary choice. Only when that choice would leave less than half a chunk
// does it move the boundary earlier, without crossing the size budget.
func splitTextWithBalancedTail(
	content string,
	maxSize int,
	split func(string, int) (string, string),
) (string, string) {
	return splitTextWithBalancedTailAndWhitespaceTrimming(
		content,
		maxSize,
		split,
		false,
	)
}

func splitTextWithBalancedTailAndWhitespaceTrimming(
	content string,
	maxSize int,
	split func(string, int) (string, string),
	trimWhitespace bool,
) (string, string) {
	prefix, remaining := split(content, maxSize)
	if remaining == "" || maxSize <= 1 {
		return prefix, remaining
	}

	minimumSize := max(1, maxSize/2)
	if encoding.RuneCount(remaining) >= minimumSize {
		return prefix, remaining
	}

	processed := content
	if trimWhitespace {
		processed = strings.TrimSpace(processed)
	}
	contentSize := encoding.RuneCount(processed)
	balancedLimit := contentSize - minimumSize
	if balancedLimit <= 0 || balancedLimit >= maxSize {
		return prefix, remaining
	}
	balancedPrefix, balancedRemaining := split(content, balancedLimit)
	minimumNaturalSize := max(1, maxSize*2/5)
	contentRunes := []rune(processed)
	balancedPrefixSize := encoding.RuneCount(balancedPrefix)
	balancedRemainingSize := encoding.RuneCount(balancedRemaining)
	balancedStart := len(contentRunes) - balancedRemainingSize
	if balancedPrefixSize >= minimumNaturalSize &&
		balancedRemainingSize >= minimumNaturalSize &&
		isNaturalTextStart(contentRunes, balancedStart) {
		return balancedPrefix, balancedRemaining
	}

	// The balanced target can fall just before a nearby natural boundary, for
	// example before the closing delimiter of a Markdown table row. Prefer the
	// next boundary that still fits instead of hard-splitting the row.
	maxPosition := min(maxSize, len(contentRunes)-1)
	for position := max(balancedLimit+1, 1); position <= maxPosition; position++ {
		if !isNaturalTextStart(contentRunes, position) {
			continue
		}
		naturalPrefix := string(contentRunes[:position])
		naturalRemaining := string(contentRunes[position:])
		if trimWhitespace {
			naturalPrefix = strings.TrimSpace(naturalPrefix)
			naturalRemaining = strings.TrimSpace(naturalRemaining)
		}
		if encoding.RuneCount(naturalPrefix) < minimumNaturalSize ||
			encoding.RuneCount(naturalRemaining) < minimumNaturalSize {
			continue
		}
		return naturalPrefix, naturalRemaining
	}

	for position := min(balancedLimit, len(contentRunes)-1); position >= minimumSize; position-- {
		position = safeTextSplitPosition(contentRunes, position)
		hardPrefix := string(contentRunes[:position])
		hardRemaining := string(contentRunes[position:])
		if trimWhitespace {
			hardPrefix = strings.TrimSpace(hardPrefix)
			hardRemaining = strings.TrimSpace(hardRemaining)
		}
		if encoding.RuneCount(hardPrefix) < minimumSize ||
			encoding.RuneCount(hardRemaining) < minimumSize {
			continue
		}
		return hardPrefix, hardRemaining
	}
	return prefix, remaining
}

func preferredTextBoundary(content []rune, maxSize int) int {
	minPreferredSize := max(1, maxSize/2)
	lineBoundary := -1
	sentenceBoundary := -1
	punctuationBoundary := -1
	whitespaceBoundary := -1

	for i := 0; i < maxSize && i < len(content); i++ {
		current := content[i]
		switch {
		case current == '\n':
			lineBoundary = i + 1
		case isSentenceBoundary(content, i):
			sentenceBoundary = i + 1
		case strings.ContainsRune(",，;；:：、", current):
			punctuationBoundary = i + 1
		case unicode.IsSpace(current):
			whitespaceBoundary = i
		}
	}

	for _, boundary := range []int{
		lineBoundary,
		sentenceBoundary,
		punctuationBoundary,
		whitespaceBoundary,
	} {
		if boundary >= minPreferredSize {
			return boundary
		}
	}
	return safeTextSplitPosition(content, min(maxSize, len(content)))
}

func isSentenceBoundary(content []rune, position int) bool {
	if position+1 < len(content) &&
		isSentencePunctuation(content[position+1]) {
		return false
	}
	switch content[position] {
	case '。', '！', '？':
		return true
	case '.', '!', '?':
		return position+1 == len(content) ||
			unicode.IsSpace(content[position+1])
	default:
		return false
	}
}

func isSentencePunctuation(r rune) bool {
	return strings.ContainsRune(".!?。！？", r)
}

func safeTextSplitPosition(content []rune, position int) int {
	if position <= 0 || position >= len(content) {
		return position
	}
	originalPosition := position
	for position > 0 &&
		isSentencePunctuation(content[position-1]) &&
		isSentencePunctuation(content[position]) {
		position--
	}
	if position == 0 {
		return originalPosition
	}
	return position
}

// naturalTextSuffix returns at most maxSize trailing runes. When the exact
// start would cut through a word, it advances to the next natural boundary.
// An unbroken token still uses an exact rune suffix so overlap remains useful.
func naturalTextSuffix(content string, maxSize int) (string, bool) {
	return naturalTextSuffixWithWhitespaceTrimming(content, maxSize, false)
}

func naturalTextSuffixWithWhitespaceTrimming(
	content string,
	maxSize int,
	trimWhitespace bool,
) (string, bool) {
	contentRunes := []rune(content)
	if maxSize <= 0 || len(contentRunes) == 0 {
		return "", false
	}
	if len(contentRunes) <= maxSize {
		return content, true
	}

	start := len(contentRunes) - maxSize
	if isNaturalTextStart(contentRunes, start) {
		suffix := string(contentRunes[start:])
		if trimWhitespace {
			suffix = strings.TrimLeftFunc(suffix, unicode.IsSpace)
		}
		return suffix, true
	}
	for candidate := start + 1; candidate < len(contentRunes); candidate++ {
		if isNaturalTextStart(contentRunes, candidate) {
			suffix := string(contentRunes[candidate:])
			if trimWhitespace {
				suffix = strings.TrimLeftFunc(suffix, unicode.IsSpace)
			}
			return suffix, true
		}
	}
	return string(contentRunes[start:]), false
}

func isNaturalTextStart(content []rune, position int) bool {
	if position <= 0 {
		return true
	}
	previous := content[position-1]
	return unicode.IsSpace(previous) ||
		isSentenceBoundary(content, position-1) ||
		strings.ContainsRune(",，;；:：、", previous)
}
