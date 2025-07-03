package document

import "strings"

// NaturalBreakChunking implements a chunking strategy that finds natural break points.
// It prioritizes splitting at newlines and sentence endings to maintain semantic coherence.
type NaturalBreakChunking struct {
	opts *options
}

// NewNaturalBreakChunking creates a new natural break chunking strategy.
func NewNaturalBreakChunking(opts ...Option) (*NaturalBreakChunking, error) {
	options := buildOptions(opts...)

	if err := options.validate(); err != nil {
		return nil, err
	}
	return &NaturalBreakChunking{
		opts: options,
	}, nil
}

// Chunk splits the document by finding natural break points like newlines and sentence endings.
func (n *NaturalBreakChunking) Chunk(doc *Document) ([]*Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)
	contentLength := len(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= n.opts.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*Document{chunk}, nil
	}

	var chunks []*Document
	chunkNumber := 1
	start := 0

	for start < contentLength {
		end := min(start+n.opts.chunkSize, contentLength)

		// Try to find a natural break point if we're not at the end.
		if end < contentLength {
			naturalBreak := n.findNaturalBreakPoint(content, start, end)
			if naturalBreak != -1 {
				end = naturalBreak
			}
		}

		chunkContent := content[start:end]
		chunk := createChunk(doc, chunkContent, chunkNumber)
		chunks = append(chunks, chunk)

		chunkNumber++

		// Calculate next start position with overlap.
		newStart := end - n.opts.overlap

		// Prevent infinite loop by ensuring we make progress.
		if newStart <= start {
			minProgress := max(1, int(float64(n.opts.chunkSize)*MinProgressRatio))
			newStart = min(contentLength, start+minProgress)
		}

		start = newStart
	}
	return chunks, nil
}

// findNaturalBreakPoint searches for the best natural break point within the target range.
// Priority order: newline > period.
func (n *NaturalBreakChunking) findNaturalBreakPoint(content string, start, targetEnd int) int {
	// Search for high priority break characters first.
	for _, separator := range HighPriorityBreaks {
		// Find the last occurrence of this separator in the range.
		searchRange := content[start:targetEnd]
		lastIndex := strings.LastIndex(searchRange, separator)

		if lastIndex != -1 {
			// Return the position after the separator.
			return start + lastIndex + len(separator)
		}
	}

	// Search for medium priority break characters.
	for _, separator := range MediumPriorityBreaks {
		// Find the last occurrence of this separator in the range.
		searchRange := content[start:targetEnd]
		lastIndex := strings.LastIndex(searchRange, separator)

		if lastIndex != -1 {
			// Return the position after the separator.
			return start + lastIndex + len(separator)
		}
	}
	return -1 // No natural break point found.
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
