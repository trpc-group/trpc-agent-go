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
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// chunkCounter tracks emitted semantic units while overlap budget is reserved.
type chunkCounter int

func (c *chunkCounter) Advance() {
	*c = *c + 1
}

func (c *chunkCounter) Count() int {
	return int(*c)
}

// MarkdownChunking implements a chunking strategy optimized for markdown documents.
type MarkdownChunking struct {
	chunkSize  int
	overlap    int
	md         goldmark.Markdown
	lengthFunc func(string) (int, error)
}

// MarkdownOption represents a functional option for configuring MarkdownChunking.
type MarkdownOption func(*MarkdownChunking)

// WithMarkdownChunkSize sets the maximum size of each chunk. The unit is
// Unicode runes unless WithMarkdownLengthFunc is configured.
func WithMarkdownChunkSize(size int) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.chunkSize = size
	}
}

// WithMarkdownOverlap sets the maximum overlap between chunks. The unit is
// Unicode runes unless WithMarkdownLengthFunc is configured.
func WithMarkdownOverlap(overlap int) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.overlap = overlap
	}
}

// WithMarkdownLengthFunc sets the function used to measure chunk size and
// overlap. By default, MarkdownChunking measures Unicode runes. The function
// must return a deterministic, non-negative length that broadly grows with its
// input. Local non-monotonic behavior from tokenizers is supported.
func WithMarkdownLengthFunc(
	lengthFunc func(string) (int, error),
) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.lengthFunc = lengthFunc
	}
}

// NewMarkdownChunking creates a new markdown chunking strategy with options.
func NewMarkdownChunking(opts ...MarkdownOption) *MarkdownChunking {
	mc := &MarkdownChunking{
		chunkSize: defaultChunkSize,
		overlap:   defaultOverlap,
		md:        goldmark.New(),
	}
	// Apply options.
	for _, opt := range opts {
		opt(mc)
	}
	return mc
}

// Chunk splits the document using markdown-aware chunking.
func (m *MarkdownChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if err := validateChunkConfig(m.chunkSize, m.overlap); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)

	// If content is small enough, return as single chunk.
	contentSize, err := measureTextLength(m.lengthFunc, content)
	if err != nil {
		return nil, fmt.Errorf("markdown chunking: %w", err)
	}
	if contentSize <= m.chunkSize {
		chunk := m.createMarkdownChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	// Parse Markdown structure, then pack adjacent semantic units.
	rawChunks, err := m.splitRecursively(content)
	if err != nil {
		return nil, fmt.Errorf("markdown chunking: %w", err)
	}
	rawChunks, err = m.mergeAdjacentChunks(content, rawChunks)
	if err != nil {
		return nil, fmt.Errorf("markdown chunking: %w", err)
	}
	chunks := make([]*document.Document, len(rawChunks))
	for i, chunk := range rawChunks {
		chunks[i] = m.createMarkdownChunkWithPath(
			doc,
			chunk.content,
			i+1,
			chunk.headerPath,
		)
	}

	// Apply overlap if specified.
	if m.overlap > 0 {
		chunks, err = m.applyOverlap(content, chunks)
		if err != nil {
			return nil, fmt.Errorf("markdown chunking: %w", err)
		}
	}
	for i, chunk := range chunks {
		size, err := measureTextLength(m.lengthFunc, chunk.Content)
		if err != nil {
			return nil, fmt.Errorf("markdown chunking: %w", err)
		}
		if size > m.chunkSize {
			return nil, fmt.Errorf(
				"markdown chunking: final chunk %d has length %d, exceeds chunk size %d",
				i+1,
				size,
				m.chunkSize,
			)
		}
	}

	return chunks, nil
}

// headerSection represents a section split by a specific header level.
type headerSection struct {
	Header  string   // The header text (e.g., "## Title")
	Content string   // The content under this header
	Level   int      // Header level (1-6)
	Path    []string // Header path (e.g., ["Main", "Sub", "Current"])
}

// markdownChunk keeps structural metadata until semantic units are packed.
type markdownChunk struct {
	content    string
	headerPath []string
	separator  string
}

// splitRecursively splits content by headers recursively (similar to LangChain).
// It tries to split by headers from level 1 to 6, then by double newlines, then by fixed size.
func (m *MarkdownChunking) splitRecursively(
	content string,
) ([]markdownChunk, error) {
	counter := new(chunkCounter)
	return m.splitRecursivelyWithPath(content, nil, counter, 1)
}

// splitRecursivelyWithPath splits content recursively while maintaining header path.
func (m *MarkdownChunking) splitRecursivelyWithPath(
	content string,
	headerPath []string,
	counter *chunkCounter,
	startHeaderLevel int,
) ([]markdownChunk, error) {
	var chunks []markdownChunk

	contentSize, err := measureTextLength(m.lengthFunc, content)
	if err != nil {
		return nil, err
	}
	chunkSize := m.nextChunkSize(counter)

	// Base case: content fits in one chunk
	if contentSize <= chunkSize {
		counter.Advance()
		return []markdownChunk{newMarkdownChunk(content, headerPath)}, nil
	}

	// Try splitting by headers from the next unprocessed level to level 6.
	for level := startHeaderLevel; level <= 6; level++ {
		sections := m.splitByHeader(content, level)
		if len(sections) > 0 {
			// Successfully split by this header level
			for _, section := range sections {
				// Skip only a genuinely empty preamble. A header without
				// body text is still source content and must be preserved.
				if section.Header == "" &&
					strings.TrimSpace(section.Content) == "" {
					continue
				}

				// Combine header and content for the full section text. Trim
				// only the section edges so blank lines already represented by
				// the separator are not duplicated.
				var fullContent string
				sectionContent := strings.TrimSpace(section.Content)
				if section.Header != "" && sectionContent != "" {
					fullContent = section.Header + "\n\n" + sectionContent
				} else if section.Header != "" {
					fullContent = section.Header
				} else {
					fullContent = sectionContent
				}

				sectionSize, err := measureTextLength(
					m.lengthFunc,
					fullContent,
				)
				if err != nil {
					return nil, err
				}
				chunkSize = m.nextChunkSize(counter)

				// Build new header path
				var newPath []string
				if headerPath != nil {
					newPath = append([]string{}, headerPath...)
				}
				if len(section.Path) > 0 && section.Path[0] != "" {
					newPath = append(newPath, section.Path...)
				}

				if sectionSize <= chunkSize {
					// Section fits in one chunk
					counter.Advance()
					chunks = append(chunks, newMarkdownChunk(fullContent, newPath))
				} else {
					// Section is too large, split recursively
					subChunks, err := m.splitRecursivelyWithPath(
						fullContent,
						newPath,
						counter,
						level+1,
					)
					if err != nil {
						return nil, err
					}
					chunks = append(chunks, subChunks...)
				}
			}
			return chunks, nil
		}
	}

	// No headers found or only one section, try splitting by Markdown blocks.
	// Keep fenced code intact here so blank lines inside the fence do not
	// create unrelated, undersized chunks.
	paragraphs := splitMarkdownParagraphs(content)
	if len(paragraphs) > 1 {
		chunks, err = m.mergeSmallParagraphsWithPath(
			paragraphs,
			headerPath,
			counter,
		)
		if err != nil {
			return nil, err
		}
		if len(chunks) > 0 {
			return chunks, nil
		}
	}

	return m.splitRemainingTextWithPath(
		content,
		headerPath,
		counter,
	)
}

// splitRemainingTextWithPath splits content at the best available text
// boundary after structural Markdown splitters have been exhausted.
func (m *MarkdownChunking) splitRemainingTextWithPath(
	content string,
	headerPath []string,
	counter *chunkCounter,
) ([]markdownChunk, error) {
	var chunks []markdownChunk
	remainingText := content
	for remainingText != "" {
		chunkSize := m.nextChunkSize(counter)
		var chunkText, rest string
		var err error
		if m.lengthFunc == nil {
			chunkText, rest = splitTextWithBalancedTail(
				remainingText,
				chunkSize,
				splitMarkdownText,
			)
		} else {
			chunkText, rest, err =
				splitTextWithBalancedTailByLength(
					remainingText,
					chunkSize,
					m.lengthFunc,
				)
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(chunkText) == "" {
			if m.lengthFunc != nil {
				return nil, fmt.Errorf(
					"unable to split markdown within chunk size %d",
					chunkSize,
				)
			}
			textChunks := encoding.SafeSplitBySize(remainingText, chunkSize)
			chunkText = textChunks[0]
			rest = remainingText[len(chunkText):]
		}
		counter.Advance()
		chunks = append(chunks, newMarkdownChunk(chunkText, headerPath))
		remainingText = rest
	}

	return chunks, nil
}

// splitByHeader splits content by a specific header level.
func (m *MarkdownChunking) splitByHeader(content string, level int) []headerSection {
	reader := text.NewReader([]byte(content))
	doc := m.md.Parser().Parse(reader)
	source := []byte(content)

	var sections []headerSection
	lastHeaderPos := 0
	var lastHeader *headerSection

	// Walk the document to find headers at the target level
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		if heading, ok := node.(*ast.Heading); ok && heading.Level == level {
			// Extract heading text once so fallback strategies can reuse it.
			headerText := m.extractText(heading, source)

			// Find the start of the heading line (including the # symbols).
			var headingLineStart int
			if heading.Lines().Len() > 0 {
				headingLineStart = heading.Lines().At(0).Start
				// Move back to find the start of the line (before #)
				for headingLineStart > 0 && source[headingLineStart-1] != '\n' {
					headingLineStart--
				}
			} else {
				// Fallback: try to determine position from heading descendants.
				headingLineStart = findNodeStartPos(heading, source)
			}

			// Final fallback: scan from the last known content position to find
			// the next heading line at this level.
			if headingLineStart < 0 {
				headingLineStart = findHeadingLineStartFallback(source, lastHeaderPos, level, headerText)
			}
			// Keep monotonic progress to avoid invalid ranges while preserving
			// subsequent heading boundaries.
			//
			// Invariant:
			//   headingLineStart is always >= lastHeaderPos (non-decreasing).
			//   Equality is allowed when position recovery fails and we clamp to
			//   lastHeaderPos. In that case the previous range is empty and is
			//   safely dropped by the existing TrimSpace/empty-content filter.
			headingLineStart = normalizeHeadingLineStart(headingLineStart, lastHeaderPos)

			// Save the previous section before starting a new one
			if lastHeader != nil {
				// Extract content from last header position to current heading start
				sectionContent := string(source[lastHeaderPos:headingLineStart])
				lastHeader.Content = sectionContent
				if lastHeader.Header != "" ||
					strings.TrimSpace(lastHeader.Content) != "" {
					sections = append(sections, *lastHeader)
				}
			} else if lastHeaderPos == 0 {
				// Content before first header
				if headingLineStart > 0 {
					beforeContent := string(source[0:headingLineStart])
					if strings.TrimSpace(beforeContent) != "" {
						sections = append(sections, headerSection{
							Header:  "",
							Content: beforeContent,
							Level:   0,
							Path:    nil,
						})
					}
				}
			}

			// Calculate position after the header line (after the newline)
			var contentStartPos int
			if heading.Lines().Len() > 0 {
				lastLine := heading.Lines().At(heading.Lines().Len() - 1)
				contentStartPos = lastLine.Stop
				// Skip the newline after the header
				if contentStartPos < len(source) && source[contentStartPos] == '\n' {
					contentStartPos++
				}
			} else {
				// Fallback: move to the beginning of the next line so the header line
				// itself is not duplicated in section content.
				contentStartPos = findLineContentStartPos(source, headingLineStart)
			}

			lastHeader = &headerSection{
				Header:  markdownHeaderContent(source, headingLineStart, level, headerText),
				Level:   level,
				Path:    []string{headerText},
				Content: "", // Will be filled when we find the next header or reach the end
			}
			lastHeaderPos = contentStartPos
		}

		return ast.WalkContinue, nil
	})

	// Process the last section
	if lastHeader != nil {
		sectionContent := string(source[lastHeaderPos:])
		lastHeader.Content = sectionContent
		if lastHeader.Header != "" ||
			strings.TrimSpace(lastHeader.Content) != "" {
			sections = append(sections, *lastHeader)
		}
	}
	// Note: If len(sections) == 0, it means no headers found at this level.
	// We return empty slice to let caller try next level or other splitting strategies.

	return sections
}

func markdownHeaderContent(
	source []byte,
	lineStart int,
	level int,
	headerText string,
) string {
	fallback := strings.Repeat("#", level) + " " + headerText
	lineEnd := len(source)
	if offset := bytes.IndexByte(source[lineStart:], '\n'); offset >= 0 {
		lineEnd = lineStart + offset
	}
	rawHeader := source[lineStart:lineEnd]
	if !isATXHeadingLineAtLevel(rawHeader, level) {
		return fallback
	}
	return string(bytes.TrimSuffix(rawHeader, []byte{'\r'}))
}

// findNodeStartPos tries to determine the start position of a heading node
// by inspecting descendant text segments. It walks back to find the beginning
// of the line (before any '#' prefix). Returns -1 if no position can be determined.
func findNodeStartPos(heading ast.Node, source []byte) int {
	startPos := -1
	_ = ast.Walk(heading, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		textNode, ok := node.(*ast.Text)
		if !ok {
			return ast.WalkContinue, nil
		}
		startPos = textNode.Segment.Start
		for startPos > 0 && source[startPos-1] != '\n' {
			startPos--
		}
		return ast.WalkStop, nil
	})
	return startPos
}

// normalizeHeadingLineStart keeps headingLineStart monotonic with lastHeaderPos.
// This prevents invalid slice bounds when section ranges are computed.
func normalizeHeadingLineStart(headingLineStart, lastHeaderPos int) int {
	if headingLineStart < 0 {
		return lastHeaderPos
	}
	if headingLineStart < lastHeaderPos {
		return lastHeaderPos
	}
	return headingLineStart
}

// findHeadingLineStartFallback scans source lines to find the next ATX heading
// at the target level, starting from searchFrom.
//
// Matching policy:
//   - headingText is non-empty: prefer lines that contain headingText and
//     fall back to the first same-level ATX heading.
//   - headingText is empty: match only empty ATX headings (pure marker lines),
//     and do not match arbitrary same-level headings.
//
// The scan works on byte slices to avoid per-line string allocations in large
// markdown files and ignores lines inside fenced code blocks.
func findHeadingLineStartFallback(source []byte, searchFrom, level int, headingText string) int {
	if len(source) == 0 || level <= 0 {
		return -1
	}
	headingTextBytes := bytes.TrimSpace([]byte(headingText))
	lineStart := normalizeFallbackSearchStart(source, searchFrom)

	firstCandidate := -1
	inFence := false
	var fenceChar byte
	var fenceLen int
	for lineStart <= len(source) {
		lineEnd := lineStart
		for lineEnd < len(source) && source[lineEnd] != '\n' {
			lineEnd++
		}
		line := source[lineStart:lineEnd]

		var handled bool
		inFence, fenceChar, fenceLen, handled = handleFallbackFenceLine(line, inFence, fenceChar, fenceLen)
		if !handled && !inFence {
			if matchPos, updatedCandidate, ok := matchFallbackHeadingLine(
				line, lineStart, level, headingTextBytes, firstCandidate,
			); ok {
				return matchPos
			} else {
				firstCandidate = updatedCandidate
			}
		}

		if lineEnd == len(source) {
			break
		}
		lineStart = lineEnd + 1
	}
	return firstCandidate
}

// normalizeFallbackSearchStart clamps searchFrom to [0,len(source)] and then
// backtracks to the beginning of the current line.
func normalizeFallbackSearchStart(source []byte, searchFrom int) int {
	if searchFrom < 0 {
		searchFrom = 0
	}
	if searchFrom > len(source) {
		searchFrom = len(source)
	}
	for searchFrom > 0 && source[searchFrom-1] != '\n' {
		searchFrom--
	}
	return searchFrom
}

// handleFallbackFenceLine updates fenced-code-block state for a scanned line.
// handled=true means the line is a fence delimiter and should not be treated as
// a heading candidate.
func handleFallbackFenceLine(
	line []byte,
	inFence bool,
	fenceChar byte,
	fenceLen int,
) (newInFence bool, newFenceChar byte, newFenceLen int, handled bool) {
	delimChar, delimLen, delimRest, ok := parseFenceDelimiter(line)
	if !ok {
		return inFence, fenceChar, fenceLen, false
	}
	if !inFence {
		return true, delimChar, delimLen, true
	}
	if delimChar == fenceChar && delimLen >= fenceLen && len(bytes.TrimSpace(delimRest)) == 0 {
		return false, fenceChar, fenceLen, true
	}
	return inFence, fenceChar, fenceLen, true
}

// matchFallbackHeadingLine evaluates whether line is a fallback match.
// It returns (matchPos, updatedFirstCandidate, ok).
func matchFallbackHeadingLine(
	line []byte,
	lineStart int,
	level int,
	headingTextBytes []byte,
	firstCandidate int,
) (int, int, bool) {
	if !isATXHeadingLineAtLevel(line, level) {
		return -1, firstCandidate, false
	}
	if len(headingTextBytes) == 0 {
		if isEmptyATXHeadingLineAtLevel(line, level) {
			return lineStart, firstCandidate, true
		}
		return -1, firstCandidate, false
	}
	if firstCandidate < 0 {
		firstCandidate = lineStart
	}
	if bytes.Contains(line, headingTextBytes) {
		return lineStart, firstCandidate, true
	}
	return -1, firstCandidate, false
}

// isEmptyATXHeadingLineAtLevel checks whether an ATX heading line is an empty
// heading at the given level (for example: "#", "##   ", "### ###").
func isEmptyATXHeadingLineAtLevel(line []byte, level int) bool {
	if !isATXHeadingLineAtLevel(line, level) {
		return false
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})
	trimmedLeft := bytes.TrimLeft(line, " ")
	rest := trimmedLeft[level:]
	if len(rest) == 0 {
		return true
	}
	rest = bytes.TrimLeft(rest, " \t")
	if len(rest) == 0 {
		return true
	}
	rest = bytes.TrimSpace(rest)
	if len(rest) == 0 {
		return true
	}
	for _, b := range rest {
		if b != '#' {
			return false
		}
	}
	return true
}

// parseFenceDelimiter parses a potential fenced code block delimiter line.
// It supports up to 3 leading spaces and requires at least 3 delimiter chars.
func parseFenceDelimiter(line []byte) (fenceChar byte, fenceLen int, rest []byte, ok bool) {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	trimmedLeft := bytes.TrimLeft(line, " ")
	leadingSpaces := len(line) - len(trimmedLeft)
	if leadingSpaces > 3 || len(trimmedLeft) == 0 {
		return 0, 0, nil, false
	}
	first := trimmedLeft[0]
	if first != '`' && first != '~' {
		return 0, 0, nil, false
	}
	count := 0
	for count < len(trimmedLeft) && trimmedLeft[count] == first {
		count++
	}
	if count < 3 {
		return 0, 0, nil, false
	}
	return first, count, trimmedLeft[count:], true
}

// isATXHeadingLineAtLevel checks whether a line matches an ATX heading marker
// of the given level ("#", "##", ...). It allows up to 3 leading spaces.
func isATXHeadingLineAtLevel(line []byte, level int) bool {
	if level <= 0 {
		return false
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})

	trimmedLeft := bytes.TrimLeft(line, " ")
	leadingSpaces := len(line) - len(trimmedLeft)
	if leadingSpaces > 3 {
		return false
	}
	if len(trimmedLeft) < level {
		return false
	}
	for i := 0; i < level; i++ {
		if trimmedLeft[i] != '#' {
			return false
		}
	}
	if len(trimmedLeft) == level {
		return true
	}
	next := trimmedLeft[level]
	return next == ' ' || next == '\t'
}

// findLineContentStartPos returns the index of the first character on the
// line following lineStart, used as fallback section content start.
func findLineContentStartPos(source []byte, lineStart int) int {
	if lineStart < 0 {
		return 0
	}
	if lineStart >= len(source) {
		return len(source)
	}
	pos := lineStart
	for pos < len(source) && source[pos] != '\n' {
		pos++
	}
	if pos < len(source) {
		pos++
	}
	return pos
}

func splitMarkdownParagraphs(content string) []string {
	lines := strings.SplitAfter(content, "\n")
	paragraphs := make([]string, 0, len(lines))
	var current strings.Builder
	var fenceMarker byte
	fenceLength := 0

	flush := func() {
		paragraph := strings.TrimSpace(current.String())
		if paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
		}
		current.Reset()
	}

	for _, line := range lines {
		marker, markerLength, rest, ok := markdownFence(line)
		if fenceMarker == 0 && ok {
			fenceMarker = marker
			fenceLength = markerLength
		} else if fenceMarker != 0 &&
			ok &&
			marker == fenceMarker &&
			markerLength >= fenceLength &&
			strings.TrimSpace(rest) == "" {
			fenceMarker = 0
			fenceLength = 0
		}

		if fenceMarker == 0 && strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current.WriteString(line)
	}
	flush()
	return paragraphs
}

func markdownFence(line string) (byte, int, string, bool) {
	line = strings.TrimSuffix(line, "\n")
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return 0, 0, "", false
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return 0, 0, "", false
	}
	length := 1
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}
	if length < 3 {
		return 0, 0, "", false
	}
	return marker, length, trimmed[length:], true
}

// extractText extracts text content from an AST node.
func (m *MarkdownChunking) extractText(node ast.Node, source []byte) string {
	var buf bytes.Buffer
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch v := n.(type) {
		case *ast.Text:
			buf.Write(v.Text(source))
		case *ast.String:
			buf.Write(v.Value)
		}
		return ast.WalkContinue, nil
	})
	return buf.String()
}

// mergeSmallParagraphsWithPath merges paragraphs with header path tracking.
func (m *MarkdownChunking) mergeSmallParagraphsWithPath(
	paragraphs []string,
	headerPath []string,
	counter *chunkCounter,
) ([]markdownChunk, error) {
	if m.lengthFunc != nil {
		return m.mergeSmallParagraphsWithPathByLength(
			paragraphs,
			headerPath,
			counter,
		)
	}
	var chunks []markdownChunk
	var currentChunk strings.Builder

	flush := func() {
		if currentChunk.Len() == 0 {
			return
		}
		counter.Advance()
		chunks = append(
			chunks,
			newMarkdownChunk(currentChunk.String(), headerPath),
		)
		currentChunk.Reset()
	}

	for _, paragraph := range paragraphs {
		remainingText := strings.TrimSpace(paragraph)
		for remainingText != "" {
			chunkSize := m.nextChunkSize(counter)
			currentSize := encoding.RuneCount(currentChunk.String())
			remainingSize := encoding.RuneCount(remainingText)
			separatorSize := 0
			if currentSize > 0 {
				separatorSize = 2
			}

			if currentSize+separatorSize+remainingSize <= chunkSize {
				if separatorSize > 0 {
					currentChunk.WriteString("\n\n")
				}
				currentChunk.WriteString(remainingText)
				break
			}

			if currentSize > 0 {
				availableSize := chunkSize - currentSize - separatorSize
				if isMarkdownHeading(currentChunk.String()) && availableSize > 0 {
					var prefix string
					prefix, remainingText = splitMarkdownTextWithBalancedTail(
						remainingText,
						availableSize,
					)
					if prefix != "" {
						currentChunk.WriteString("\n\n")
						currentChunk.WriteString(prefix)
					}
				}
				flush()
				continue
			}

			var prefix string
			prefix, remainingText = splitMarkdownTextWithBalancedTail(
				remainingText,
				chunkSize,
			)
			currentChunk.WriteString(prefix)
			if remainingText != "" {
				flush()
			}
		}
	}
	flush()
	return chunks, nil
}

func (m *MarkdownChunking) mergeSmallParagraphsWithPathByLength(
	paragraphs []string,
	headerPath []string,
	counter *chunkCounter,
) ([]markdownChunk, error) {
	var chunks []markdownChunk
	var currentChunk string

	flush := func() {
		if currentChunk == "" {
			return
		}
		counter.Advance()
		chunks = append(chunks, newMarkdownChunk(currentChunk, headerPath))
		currentChunk = ""
	}

	for _, paragraph := range paragraphs {
		remainingText := strings.TrimSpace(paragraph)
		for remainingText != "" {
			chunkSize := m.nextChunkSize(counter)
			candidate := remainingText
			if currentChunk != "" {
				candidate = currentChunk + "\n\n" + remainingText
			}
			candidateSize, err := measureTextLength(
				m.lengthFunc,
				candidate,
			)
			if err != nil {
				return nil, err
			}
			if candidateSize <= chunkSize {
				currentChunk = candidate
				break
			}

			if currentChunk != "" {
				if isMarkdownHeading(currentChunk) {
					current := currentChunk
					firstRune := string([]rune(remainingText)[:1])
					incrementalLength := func(
						text string,
					) (int, error) {
						return measureTextLength(
							m.lengthFunc,
							current+"\n\n"+text,
						)
					}
					firstSize, err := incrementalLength(firstRune)
					if err != nil {
						return nil, err
					}
					if firstSize <= chunkSize {
						prefix, rest, err :=
							splitTextAtNaturalBoundaryByLength(
								remainingText,
								chunkSize,
								incrementalLength,
							)
						if err != nil {
							return nil, err
						}
						if prefix != "" {
							currentChunk += "\n\n" + prefix
							remainingText = rest
						}
					}
				}
				flush()
				continue
			}

			prefix, rest, err :=
				splitTextWithBalancedTailByLength(
					remainingText,
					chunkSize,
					m.lengthFunc,
				)
			if err != nil {
				return nil, err
			}
			if prefix == "" {
				return nil, fmt.Errorf(
					"unable to split markdown paragraph within chunk size %d",
					chunkSize,
				)
			}
			currentChunk = prefix
			remainingText = rest
			if remainingText != "" {
				flush()
			}
		}
	}
	flush()
	return chunks, nil
}

func (m *MarkdownChunking) nextChunkSize(counter *chunkCounter) int {
	if m.overlap > 0 && counter.Count() > 0 {
		return m.chunkSize - m.overlap
	}
	return m.chunkSize
}

func splitMarkdownText(content string, chunkSize int) (string, string) {
	return splitTextAtNaturalBoundary(content, chunkSize)
}

func splitMarkdownTextWithBalancedTail(
	content string,
	chunkSize int,
) (string, string) {
	return splitTextWithBalancedTail(
		content,
		chunkSize,
		splitMarkdownText,
	)
}

func isMarkdownHeading(content string) bool {
	if strings.ContainsRune(content, '\n') {
		return false
	}
	line := []byte(content)
	for level := 1; level <= 6; level++ {
		if isATXHeadingLineAtLevel(line, level) {
			return true
		}
	}
	return false
}

func newMarkdownChunk(content string, headerPath []string) markdownChunk {
	return markdownChunk{
		content:    content,
		headerPath: append([]string(nil), headerPath...),
	}
}

// mergeAdjacentChunks treats headings as preferred split points rather than
// mandatory chunk boundaries. Adjacent semantic units are packed in source
// order when they fit in the active chunk budget.
func (m *MarkdownChunking) mergeAdjacentChunks(
	content string,
	chunks []markdownChunk,
) ([]markdownChunk, error) {
	if len(chunks) <= 1 {
		return chunks, nil
	}

	rawContents := make([]string, len(chunks))
	for i, chunk := range chunks {
		rawContents[i] = chunk.content
	}
	separators := sourceChunkSeparators(content, rawContents, "\n\n")
	for i := range chunks {
		chunks[i].separator = separators[i]
	}

	groups := make([][]markdownChunk, 0, len(chunks))
	currentGroup := []markdownChunk{chunks[0]}
	currentContent := chunks[0].content

	for i := 1; i < len(chunks); i++ {
		nextChunk := chunks[i]
		limit := m.chunkSize
		if len(groups) > 0 {
			limit -= m.overlap
		}

		// Do not attach a heading without body text to the section before it.
		// A run of consecutive heading-only units remains open so the first
		// section with body text can absorb the whole run.
		if isMarkdownHeading(nextChunk.content) &&
			!markdownChunksOnlyHeadings(currentGroup) {
			groups = append(groups, currentGroup)
			currentGroup = []markdownChunk{nextChunk}
			currentContent = nextChunk.content
			continue
		}

		candidateContent := currentContent + nextChunk.separator +
			nextChunk.content
		candidateSize, err := measureTextLength(
			m.lengthFunc,
			candidateContent,
		)
		if err != nil {
			return nil, err
		}
		if candidateSize <= limit {
			currentGroup = append(currentGroup, nextChunk)
			currentContent = candidateContent
			continue
		}

		groups = append(groups, currentGroup)
		currentGroup = []markdownChunk{nextChunk}
		currentContent = nextChunk.content
	}
	groups = append(groups, currentGroup)
	groups, err := m.rebalanceMarkdownTail(groups)
	if err != nil {
		return nil, err
	}

	mergedChunks := make([]markdownChunk, len(groups))
	for i, group := range groups {
		mergedChunks[i] = mergeMarkdownChunkGroup(group)
	}
	return mergedChunks, nil
}

func markdownChunksOnlyHeadings(chunks []markdownChunk) bool {
	if len(chunks) == 0 {
		return false
	}
	for _, chunk := range chunks {
		if !isMarkdownHeading(chunk.content) {
			return false
		}
	}
	return true
}

func (m *MarkdownChunking) rebalanceMarkdownTail(
	groups [][]markdownChunk,
) ([][]markdownChunk, error) {
	if len(groups) < 2 {
		return groups, nil
	}

	leftIndex := len(groups) - 2
	rightIndex := len(groups) - 1
	left := groups[leftIndex]
	right := groups[rightIndex]
	if markdownChunksOnlyHeadings(right) {
		return groups, nil
	}

	rightLimit := m.chunkSize - m.overlap
	leftSize, err := m.markdownChunkGroupSize(left)
	if err != nil {
		return nil, err
	}
	rightSize, err := m.markdownChunkGroupSize(right)
	if err != nil {
		return nil, err
	}
	for len(left) > 1 {
		nextLeft := left[:len(left)-1]
		moved := left[len(left)-1]
		nextRight := make([]markdownChunk, 0, len(right)+1)
		nextRight = append(nextRight, moved)
		nextRight = append(nextRight, right...)

		nextLeftSize, err := m.markdownChunkGroupSize(nextLeft)
		if err != nil {
			return nil, err
		}
		nextRightSize, err := m.markdownChunkGroupSize(nextRight)
		if err != nil {
			return nil, err
		}
		if nextRightSize > rightLimit ||
			absInt(nextLeftSize-nextRightSize) >=
				absInt(leftSize-rightSize) {
			break
		}

		left = nextLeft
		right = nextRight
		leftSize = nextLeftSize
		rightSize = nextRightSize
	}
	groups[leftIndex] = left
	groups[rightIndex] = right
	return groups, nil
}

func (m *MarkdownChunking) markdownChunkGroupSize(
	chunks []markdownChunk,
) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}
	return measureTextLength(
		m.lengthFunc,
		mergeMarkdownChunkGroup(chunks).content,
	)
}

func mergeMarkdownChunkGroup(chunks []markdownChunk) markdownChunk {
	var content strings.Builder
	content.WriteString(chunks[0].content)
	headerPath := append([]string(nil), chunks[0].headerPath...)
	for i := 1; i < len(chunks); i++ {
		content.WriteString(chunks[i].separator)
		content.WriteString(chunks[i].content)
		headerPath = commonMarkdownHeaderPath(
			headerPath,
			chunks[i].headerPath,
		)
	}
	return newMarkdownChunk(content.String(), headerPath)
}

func commonMarkdownHeaderPath(left, right []string) []string {
	commonLength := min(len(left), len(right))
	for i := 0; i < commonLength; i++ {
		if left[i] != right[i] {
			commonLength = i
			break
		}
	}
	if commonLength == 0 {
		return nil
	}
	return append([]string(nil), left[:commonLength]...)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// createMarkdownChunk creates a chunk with markdown-specific metadata.
func (m *MarkdownChunking) createMarkdownChunk(
	originalDoc *document.Document,
	content string,
	chunkNumber int,
) *document.Document {
	return m.createMarkdownChunkWithPath(originalDoc, content, chunkNumber, nil)
}

// createMarkdownChunkWithPath creates a chunk with markdown-specific metadata and header path.
func (m *MarkdownChunking) createMarkdownChunkWithPath(
	originalDoc *document.Document,
	content string,
	chunkNumber int,
	headerPath []string,
) *document.Document {
	// Create a copy of the original metadata.
	metadata := make(map[string]any)
	for k, v := range originalDoc.Metadata {
		metadata[k] = v
	}

	// Add chunk-specific metadata.
	metadata[source.MetaChunkIndex] = chunkNumber
	metadata[source.MetaChunkSize] = encoding.RuneCount(content)

	// Add header path if available
	if len(headerPath) > 0 {
		metadata[source.MetaMarkdownHeaderPath] = strings.Join(headerPath, " > ")
	}

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

// applyOverlap applies overlap between consecutive chunks.
func (m *MarkdownChunking) applyOverlap(
	content string,
	chunks []*document.Document,
) ([]*document.Document, error) {
	if len(chunks) <= 1 {
		return chunks, nil
	}

	rawContents := make([]string, len(chunks))
	for i, chunk := range chunks {
		rawContents[i] = chunk.Content
	}
	separators := sourceChunkSeparators(content, rawContents, "\n\n")
	overlappedChunks := []*document.Document{chunks[0]}

	for i := 1; i < len(chunks); i++ {
		// Create new metadata for overlapped chunk.
		metadata := make(map[string]any)
		for k, v := range chunks[i].Metadata {
			metadata[k] = v
		}

		var overlappedContent string
		var actualOverlap int
		if m.lengthFunc == nil {
			overlappedContent, actualOverlap = joinWithOverlap(
				overlappedChunks[len(overlappedChunks)-1].Content,
				chunks[i].Content,
				m.overlap,
				m.chunkSize,
				separators[i],
			)
		} else {
			var err error
			overlappedContent, actualOverlap, err =
				joinWithOverlapSeparatorByLength(
					overlappedChunks[len(overlappedChunks)-1].Content,
					chunks[i].Content,
					m.overlap,
					m.chunkSize,
					separators[i],
					false,
					m.lengthFunc,
				)
			if err != nil {
				return nil, err
			}
		}
		if actualOverlap > 0 {
			metadata[source.MetaOverlappedContentSize] = encoding.RuneCount(overlappedContent)
		}

		overlappedChunk := &document.Document{
			ID:        chunks[i].ID,
			Name:      chunks[i].Name,
			Content:   overlappedContent,
			Metadata:  metadata,
			CreatedAt: chunks[i].CreatedAt,
			UpdatedAt: chunks[i].UpdatedAt,
		}
		overlappedChunks = append(overlappedChunks, overlappedChunk)
	}
	return overlappedChunks, nil
}
