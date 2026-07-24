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

// cleanText normalizes whitespace in text content while ensuring UTF-8 safety.
// It automatically detects encoding and converts to UTF-8 if necessary.
func cleanText(content string) string {
	// Intelligently process text based on detected encoding
	processed, encodingInfo := encoding.SmartProcessText(content)

	// Log encoding information for debugging.
	if encodingInfo.Encoding != encoding.EncodingUTF8 || !encodingInfo.IsValid {
		log.Debugf("Text encoding detected: %s (confidence: %.2f, valid: %v)",
			encodingInfo.Encoding, encodingInfo.Confidence, encodingInfo.IsValid)
	}

	// Trim leading and trailing whitespace.
	processed = strings.TrimSpace(processed)

	// Normalize line breaks.
	processed = strings.ReplaceAll(processed, "\r\n", "\n")
	processed = strings.ReplaceAll(processed, "\r", "\n")

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(processed, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
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
	currentSize := encoding.RuneCount(current)
	separatorSize := encoding.RuneCount(separator)
	availableWithoutSeparator := maxSize - currentSize
	if maxOverlap <= 0 || availableWithoutSeparator <= 0 {
		return current, 0
	}
	availableOverlap := availableWithoutSeparator - separatorSize
	if availableOverlap <= 0 {
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
	overlapContent, naturalBoundary := naturalTextSuffix(previous, overlapSize)
	if !naturalBoundary && separator != "" {
		// For an unbroken token, preserve the exact source text rather than
		// inserting a separator in the middle of the token.
		separator = ""
		availableOverlap = availableWithoutSeparator
		overlapSize = min(
			maxOverlap,
			min(availableOverlap, encoding.RuneCount(previous)),
		)
		overlapContent, _ = naturalTextSuffix(previous, overlapSize)
	}
	actualOverlap := encoding.RuneCount(overlapContent)
	if actualOverlap <= 0 {
		return current, 0
	}
	return overlapContent + separator + current, actualOverlap
}

// splitTextAtNaturalBoundary splits one prefix from content without exceeding
// maxSize. It prefers line, sentence, punctuation, and whitespace boundaries,
// falling back to an exact rune boundary only when the text has no suitable
// natural boundary.
func splitTextAtNaturalBoundary(content string, maxSize int) (string, string) {
	content = strings.TrimSpace(content)
	contentRunes := []rune(content)
	if maxSize <= 0 || len(contentRunes) <= maxSize {
		return content, ""
	}

	splitPosition := preferredTextBoundary(contentRunes, maxSize)
	prefix := strings.TrimSpace(string(contentRunes[:splitPosition]))
	remaining := strings.TrimSpace(string(contentRunes[splitPosition:]))
	return prefix, remaining
}

// splitTextAtLineBoundary packs complete lines whenever they fit the budget.
// A single oversized line is refined with the normal text boundaries and tail
// balancing, but it is never allowed to consume the beginning of the next
// complete line merely to fill the current chunk.
func splitTextAtLineBoundary(content string, maxSize int) (string, string) {
	content = strings.TrimSpace(content)
	contentRunes := []rune(content)
	if maxSize <= 0 || len(contentRunes) <= maxSize {
		return content, ""
	}

	firstLineEnd := -1
	lastLineEndWithinBudget := -1
	for i, current := range contentRunes {
		if current != '\n' {
			continue
		}
		lineEnd := i + 1
		if firstLineEnd < 0 {
			firstLineEnd = lineEnd
		}
		if lineEnd <= maxSize {
			lastLineEndWithinBudget = lineEnd
			continue
		}
		break
	}
	if lastLineEndWithinBudget > 0 {
		return strings.TrimSpace(
				string(contentRunes[:lastLineEndWithinBudget]),
			), strings.TrimSpace(
				string(contentRunes[lastLineEndWithinBudget:]),
			)
	}
	if firstLineEnd > maxSize {
		firstLine := strings.TrimSpace(string(contentRunes[:firstLineEnd-1]))
		prefix, lineRemaining := splitTextWithBalancedTail(
			firstLine,
			maxSize,
			splitTextAtNaturalBoundary,
		)
		trailingLines := strings.TrimSpace(string(contentRunes[firstLineEnd:]))
		switch {
		case lineRemaining == "":
			return prefix, trailingLines
		case trailingLines == "":
			return prefix, lineRemaining
		default:
			return prefix, lineRemaining + "\n" + trailingLines
		}
	}
	return splitTextWithBalancedTail(
		content,
		maxSize,
		splitTextAtNaturalBoundary,
	)
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
	prefix, remaining := split(content, maxSize)
	if remaining == "" || maxSize <= 1 {
		return prefix, remaining
	}

	minimumSize := max(1, maxSize/2)
	if encoding.RuneCount(remaining) >= minimumSize {
		return prefix, remaining
	}

	contentSize := encoding.RuneCount(strings.TrimSpace(content))
	balancedLimit := contentSize - minimumSize
	if balancedLimit <= 0 || balancedLimit >= maxSize {
		return prefix, remaining
	}
	balancedPrefix, balancedRemaining := split(content, balancedLimit)
	minimumNaturalSize := max(1, maxSize*2/5)
	contentRunes := []rune(strings.TrimSpace(content))
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
		naturalPrefix := strings.TrimSpace(string(contentRunes[:position]))
		naturalRemaining := strings.TrimSpace(string(contentRunes[position:]))
		if encoding.RuneCount(naturalPrefix) < minimumNaturalSize ||
			encoding.RuneCount(naturalRemaining) < minimumNaturalSize {
			continue
		}
		return naturalPrefix, naturalRemaining
	}

	for position := min(balancedLimit, len(contentRunes)-1); position >= minimumSize; position-- {
		position = safeTextSplitPosition(contentRunes, position)
		hardPrefix := strings.TrimSpace(
			string(contentRunes[:position]),
		)
		hardRemaining := strings.TrimSpace(
			string(contentRunes[position:]),
		)
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
	contentRunes := []rune(content)
	if maxSize <= 0 || len(contentRunes) == 0 {
		return "", false
	}
	if len(contentRunes) <= maxSize {
		return content, true
	}

	start := len(contentRunes) - maxSize
	if isNaturalTextStart(contentRunes, start) {
		return strings.TrimLeftFunc(
			string(contentRunes[start:]),
			unicode.IsSpace,
		), true
	}
	for candidate := start + 1; candidate < len(contentRunes); candidate++ {
		if isNaturalTextStart(contentRunes, candidate) {
			return strings.TrimLeftFunc(
				string(contentRunes[candidate:]),
				unicode.IsSpace,
			), true
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
