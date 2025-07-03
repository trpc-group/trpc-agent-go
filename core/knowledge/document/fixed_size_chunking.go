package document

// FixedSizeChunking implements a chunking strategy that splits text into fixed-size chunks.
// It attempts to split at whitespace boundaries to avoid breaking words.
type FixedSizeChunking struct {
	opts *options
}

// NewFixedSizeChunking creates a new fixed-size chunking strategy.
func NewFixedSizeChunking(opts ...Option) (*FixedSizeChunking, error) {
	options := buildOptions(opts...)

	if err := options.validate(); err != nil {
		return nil, err
	}
	return &FixedSizeChunking{
		opts: options,
	}, nil
}

// Chunk splits the document into fixed-size chunks with optional overlap.
func (f *FixedSizeChunking) Chunk(doc *Document) ([]*Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)
	contentLength := len(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= f.opts.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*Document{chunk}, nil
	}

	var chunks []*Document
	chunkNumber := 1
	start := 0

	for start+f.opts.overlap < contentLength {
		end := min(start+f.opts.chunkSize, contentLength)

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
			end = start + f.opts.chunkSize
		}

		chunkContent := content[start:end]
		chunk := createChunk(doc, chunkContent, chunkNumber)
		chunks = append(chunks, chunk)

		chunkNumber++
		start = end - f.opts.overlap
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

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
