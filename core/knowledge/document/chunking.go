// Package document provides document processing functionality for knowledge management.
package document

import (
	"strings"
)

// ChunkingStrategy defines the interface for document chunking strategies.
type ChunkingStrategy interface {
	// Chunk splits a document into smaller chunks based on the strategy's algorithm.
	Chunk(doc *Document) ([]*Document, error)
}

// FixedSizeChunking implements a chunking strategy that splits text into fixed-size chunks.
type FixedSizeChunking struct {
	ChunkSize int
	Overlap   int
}

// Option represents a functional option for configuring FixedSizeChunking.
type Option func(*FixedSizeChunking)

// WithChunkSize sets the maximum size of each chunk in characters.
func WithChunkSize(size int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.ChunkSize = size
	}
}

// WithOverlap sets the number of characters to overlap between chunks.
func WithOverlap(overlap int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.Overlap = overlap
	}
}

// NewFixedSizeChunking creates a new fixed-size chunking strategy with options.
func NewFixedSizeChunking(opts ...Option) *FixedSizeChunking {
	fsc := &FixedSizeChunking{
		ChunkSize: DefaultChunkSize,
		Overlap:   DefaultOverlap,
	}

	// Apply options.
	for _, opt := range opts {
		opt(fsc)
	}

	return fsc
}

// Chunk splits the document into fixed-size chunks with optional overlap.
func (f *FixedSizeChunking) Chunk(doc *Document) ([]*Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	// Get content directly as string.
	content := doc.Content
	content = cleanText(content)
	contentLength := len(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= f.ChunkSize {
		chunk := createChunk(doc, content, 1)
		return []*Document{chunk}, nil
	}

	var chunks []*Document
	chunkNumber := 1
	start := 0

	for start+f.Overlap < contentLength {
		end := min(start+f.ChunkSize, contentLength)

		// Try to find a good break point (whitespace) to avoid splitting words.
		if end < contentLength {
			// Look for whitespace characters near the end position.
			breakPoint := f.findBreakPoint(content, start, end)
			if breakPoint != -1 {
				end = breakPoint
			}
		}

		// If we couldn't find a good break point, use the original end.
		if end == start {
			end = start + f.ChunkSize
		}

		chunkContent := content[start:end]
		chunk := createChunk(doc, chunkContent, chunkNumber)
		chunks = append(chunks, chunk)

		chunkNumber++
		start = end - f.Overlap
	}
	return chunks, nil
}

// findBreakPoint looks for a suitable break point near the target position.
func (f *FixedSizeChunking) findBreakPoint(content string, start, targetEnd int) int {
	// Search backwards from target end to find whitespace.
	for i := targetEnd - 1; i > start; i-- {
		if f.isWhitespace(rune(content[i])) {
			return i + 1 // Return position after the whitespace.
		}
	}
	return -1 // No suitable break point found.
}

// isWhitespace checks if a character is considered whitespace.
func (f *FixedSizeChunking) isWhitespace(char rune) bool {
	for _, ws := range WhitespaceChars {
		if char == ws {
			return true
		}
	}
	return false
}

// NaturalBreakChunking implements a chunking strategy that finds natural break points.
type NaturalBreakChunking struct {
	MaxChunkSize int
	MinChunkSize int
	Overlap      int
}

// Option represents a functional option for configuring NaturalBreakChunking.
type NaturalBreakOption func(*NaturalBreakChunking)

// WithMaxChunkSize sets the maximum size of each chunk in characters.
func WithMaxChunkSize(size int) NaturalBreakOption {
	return func(nbc *NaturalBreakChunking) {
		nbc.MaxChunkSize = size
	}
}

// WithMinChunkSize sets the minimum size of each chunk in characters.
func WithMinChunkSize(size int) NaturalBreakOption {
	return func(nbc *NaturalBreakChunking) {
		nbc.MinChunkSize = size
	}
}

// WithNaturalBreakOverlap sets the number of characters to overlap between chunks.
func WithNaturalBreakOverlap(overlap int) NaturalBreakOption {
	return func(nbc *NaturalBreakChunking) {
		nbc.Overlap = overlap
	}
}

// NewNaturalBreakChunking creates a new natural break chunking strategy with options.
func NewNaturalBreakChunking(opts ...NaturalBreakOption) *NaturalBreakChunking {
	nbc := &NaturalBreakChunking{
		MaxChunkSize: DefaultChunkSize,
		MinChunkSize: DefaultChunkSize / 2,
		Overlap:      DefaultOverlap,
	}

	// Apply options.
	for _, opt := range opts {
		opt(nbc)
	}

	return nbc
}

// Chunk splits the document by finding natural break points like newlines and sentence endings.
func (n *NaturalBreakChunking) Chunk(doc *Document) ([]*Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	// Get content directly as string.
	content := doc.Content
	content = cleanText(content)
	contentLength := len(content)

	// If content is smaller than min chunk size, return as single chunk.
	if contentLength <= n.MinChunkSize {
		chunk := createChunk(doc, content, 1)
		return []*Document{chunk}, nil
	}

	// Split content into paragraphs first.
	paragraphs := n.splitIntoParagraphs(content)

	var chunks []*Document
	chunkNumber := 1
	currentChunk := ""
	currentSize := 0

	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		paragraphSize := len(paragraph)

		// If adding this paragraph would exceed max chunk size, create a new chunk.
		if currentSize+paragraphSize > n.MaxChunkSize && currentChunk != "" {
			chunk := createChunk(doc, currentChunk, chunkNumber)
			chunks = append(chunks, chunk)
			chunkNumber++

			// Start new chunk with overlap.
			if n.Overlap > 0 && currentSize > n.Overlap {
				overlapStart := currentSize - n.Overlap
				currentChunk = currentChunk[overlapStart:]
				currentSize = n.Overlap
			} else {
				currentChunk = ""
				currentSize = 0
			}
		}

		// Add paragraph to current chunk.
		if currentChunk != "" {
			currentChunk += "\n\n"
		}
		currentChunk += paragraph
		currentSize += paragraphSize
	}

	// Add the last chunk.
	if currentChunk != "" {
		chunk := createChunk(doc, currentChunk, chunkNumber)
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// splitIntoParagraphs splits content into paragraphs based on double line breaks.
func (n *NaturalBreakChunking) splitIntoParagraphs(content string) []string {
	// Split by double line breaks (paragraph breaks).
	paragraphs := strings.Split(content, "\n\n")

	var result []string
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph != "" {
			result = append(result, paragraph)
		}
	}

	return result
}

// cleanText normalizes whitespace in text content.
func cleanText(content string) string {
	// Trim leading and trailing whitespace.
	content = strings.TrimSpace(content)

	// Normalize line breaks.
	content = strings.ReplaceAll(content, CarriageReturnLineFeed, LineFeed)
	content = strings.ReplaceAll(content, CarriageReturn, LineFeed)

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(content, LineFeed)
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, LineFeed)
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *Document, content string, chunkNumber int) *Document {
	chunk := &Document{
		Name:      originalDoc.Name,
		Content:   content,
		CreatedAt: originalDoc.CreatedAt,
		UpdatedAt: originalDoc.UpdatedAt,
	}

	// Generate chunk ID.
	if originalDoc.ID != "" {
		chunk.ID = originalDoc.ID + "_chunk_" + itoa(chunkNumber)
	}

	// Copy and extend metadata.
	if originalDoc.Metadata != nil {
		chunk.Metadata = make(map[string]interface{})
		for k, v := range originalDoc.Metadata {
			chunk.Metadata[k] = v
		}
	} else {
		chunk.Metadata = make(map[string]interface{})
	}

	// Add chunk-specific metadata.
	chunk.Metadata["chunk_number"] = chunkNumber
	chunk.Metadata["is_chunk"] = true
	return chunk
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// itoa converts an integer to a string.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var result []byte
	negative := i < 0
	if negative {
		i = -i
	}

	for i > 0 {
		result = append([]byte{byte('0' + i%10)}, result...)
		i /= 10
	}

	if negative {
		result = append([]byte{'-'}, result...)
	}
	return string(result)
}
