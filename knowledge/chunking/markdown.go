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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// docIDGenerator provides thread-safe unique ID generation for document chunks.
type docIDGenerator struct {
	nextID int
	mu     sync.Mutex
}

// Next returns the next unique integer ID in a thread-safe manner.
// It increments the internal counter and returns the new value.
func (d *docIDGenerator) Next() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := d.nextID
	d.nextID++
	return id
}

// MarkdownChunking implements a chunking strategy optimized for markdown documents.
type MarkdownChunking struct {
	chunkSize int
	overlap   int
	md        goldmark.Markdown
}

// MarkdownOption represents a functional option for configuring MarkdownChunking.
type MarkdownOption func(*MarkdownChunking)

// WithMarkdownChunkSize sets the maximum size of each chunk in characters.
func WithMarkdownChunkSize(size int) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.chunkSize = size
	}
}

// WithMarkdownOverlap sets the number of characters to overlap between chunks.
func WithMarkdownOverlap(overlap int) MarkdownOption {
	return func(mc *MarkdownChunking) {
		mc.overlap = overlap
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
	// Validate parameters.
	if mc.overlap >= mc.chunkSize {
		mc.overlap = min(defaultOverlap, mc.chunkSize-1)
	}
	return mc
}

// Chunk splits the document using markdown-aware chunking.
func (m *MarkdownChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)

	// If content is small enough, return as single chunk.
	if encoding.RuneCount(content) <= m.chunkSize {
		chunk := m.createMarkdownChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	// Parse markdown structure and split recursively.
	chunks := m.splitRecursively(content, doc)

	// Apply overlap if specified.
	if m.overlap > 0 {
		chunks = m.applyOverlap(chunks)
	}

	return chunks, nil
}

// headerSection represents a section split by a specific header level.
type headerSection struct {
	Header  string   // The header text (e.g., "## Title")
	Content string   // The content under this header
	Level   int      // Header level (1-6)
	Path    []string // Header path (e.g., ["Main", "Sub", "Current"]) - for future use
}

// splitRecursively splits content by headers recursively (similar to LangChain).
// It tries to split by headers from level 1 to 6, then by double newlines, then by fixed size.
func (m *MarkdownChunking) splitRecursively(
	content string,
	originalDoc *document.Document,
) []*document.Document {
	idGen := &docIDGenerator{nextID: 1}
	return m.splitRecursivelyWithPath(content, originalDoc, nil, idGen)
}

// splitRecursivelyWithPath splits content recursively while maintaining header path.
func (m *MarkdownChunking) splitRecursivelyWithPath(
	content string,
	originalDoc *document.Document,
	headerPath []string,
	idGen *docIDGenerator,
) []*document.Document {
	var chunks []*document.Document

	contentSize := encoding.RuneCount(content)

	// Base case: content fits in one chunk
	if contentSize <= m.chunkSize {
		chunk := m.createMarkdownChunkWithPath(originalDoc, content, idGen.Next(), headerPath)
		return []*document.Document{chunk}
	}

	// Try splitting by headers from level 1 to 6
	for level := 1; level <= 6; level++ {
		sections := m.splitByHeader(content, level)
		if len(sections) > 1 {
			// Successfully split by this header level
			for _, section := range sections {
				// Skip empty sections
				if strings.TrimSpace(section.Content) == "" {
					continue
				}

				// Combine header and content for the full section text
				var fullContent string
				if section.Header != "" {
					fullContent = section.Header + "\n\n" + section.Content
				} else {
					fullContent = section.Content
				}

				sectionSize := encoding.RuneCount(fullContent)

				// Build new header path
				var newPath []string
				if headerPath != nil {
					newPath = append([]string{}, headerPath...)
				}
				if len(section.Path) > 0 && section.Path[0] != "" {
					newPath = append(newPath, section.Path...)
				}

				if sectionSize <= m.chunkSize {
					// Section fits in one chunk
					chunk := m.createMarkdownChunkWithPath(originalDoc, fullContent, idGen.Next(), newPath)
					chunks = append(chunks, chunk)
				} else {
					// Section is too large, split recursively
					subChunks := m.splitRecursivelyWithPath(fullContent, originalDoc, newPath, idGen)
					chunks = append(chunks, subChunks...)
				}
			}
			return chunks
		}
	}

	// No headers found or only one section, try splitting by paragraphs
	paragraphs := strings.Split(content, "\n\n")
	if len(paragraphs) > 1 {
		chunks = m.mergeSmallParagraphsWithPath(paragraphs, originalDoc, headerPath, idGen)
		if len(chunks) > 0 {
			return chunks
		}
	}

	// Still too large, split by fixed size (terminal case - prevents infinite recursion)
	textChunks := encoding.SafeSplitBySize(content, m.chunkSize)
	for _, chunkText := range textChunks {
		if strings.TrimSpace(chunkText) == "" {
			continue
		}
		chunk := m.createMarkdownChunkWithPath(originalDoc, chunkText, idGen.Next(), headerPath)
		chunks = append(chunks, chunk)
	}

	return chunks
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
				if strings.TrimSpace(lastHeader.Content) != "" {
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

			// Start tracking new section
			headerPrefix := strings.Repeat("#", level) + " "

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
				Header:  headerPrefix + headerText,
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
		if strings.TrimSpace(lastHeader.Content) != "" {
			sections = append(sections, *lastHeader)
		}
	}
	// Note: If len(sections) == 0, it means no headers found at this level.
	// We return empty slice to let caller try next level or other splitting strategies.

	return sections
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
	originalDoc *document.Document,
	headerPath []string,
	idGen *docIDGenerator,
) []*document.Document {
	var chunks []*document.Document
	var currentChunk strings.Builder

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		paraSize := encoding.RuneCount(para)
		currentSize := encoding.RuneCount(currentChunk.String())

		// If adding this paragraph exceeds chunk size, save current chunk
		if currentSize > 0 && currentSize+paraSize+2 > m.chunkSize {
			chunk := m.createMarkdownChunkWithPath(originalDoc, currentChunk.String(), idGen.Next(), headerPath)
			chunks = append(chunks, chunk)
			currentChunk.Reset()
		}

		// If paragraph itself is too large, split it
		if paraSize > m.chunkSize {
			// Save current chunk if not empty
			if currentChunk.Len() > 0 {
				chunk := m.createMarkdownChunkWithPath(originalDoc, currentChunk.String(), idGen.Next(), headerPath)
				chunks = append(chunks, chunk)
				currentChunk.Reset()
			}

			// Split large paragraph by fixed size
			paraChunks := encoding.SafeSplitBySize(para, m.chunkSize)
			for _, pc := range paraChunks {
				chunk := m.createMarkdownChunkWithPath(originalDoc, pc, idGen.Next(), headerPath)
				chunks = append(chunks, chunk)
			}
		} else {
			// Add paragraph to current chunk
			if currentChunk.Len() > 0 {
				currentChunk.WriteString("\n\n")
			}
			currentChunk.WriteString(para)
		}
	}

	// Add last chunk if not empty
	if currentChunk.Len() > 0 {
		chunk := m.createMarkdownChunkWithPath(originalDoc, currentChunk.String(), idGen.Next(), headerPath)
		chunks = append(chunks, chunk)
	}

	return chunks
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
func (m *MarkdownChunking) applyOverlap(chunks []*document.Document) []*document.Document {
	if len(chunks) <= 1 {
		return chunks
	}

	overlappedChunks := []*document.Document{chunks[0]}

	for i := 1; i < len(chunks); i++ {
		prevText := chunks[i-1].Content
		if encoding.RuneCount(prevText) > m.overlap {
			prevText = encoding.SafeOverlap(prevText, m.overlap)
		}

		// Create new metadata for overlapped chunk.
		metadata := make(map[string]any)
		for k, v := range chunks[i].Metadata {
			metadata[k] = v
		}

		// Combine with overlap markers to clearly indicate overlapped content
		var overlappedContent string
		if prevText != "" {
			overlappedContent = prevText + "\n\n" + chunks[i].Content
		} else {
			overlappedContent = chunks[i].Content
		}

		metadata[source.MetaOverlappedContentSize] = encoding.RuneCount(overlappedContent)

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
	return overlappedChunks
}
