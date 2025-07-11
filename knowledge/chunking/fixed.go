package chunking

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// FixedSizeChunking implements a chunking strategy that splits text into fixed-size chunks.
type FixedSizeChunking struct {
	chunkSize int
	overlap   int
}

// Option represents a functional option for configuring FixedSizeChunking.
type Option func(*FixedSizeChunking)

// WithChunkSize sets the maximum size of each chunk in characters.
func WithChunkSize(size int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.chunkSize = size
	}
}

// WithOverlap sets the number of characters to overlap between chunks.
func WithOverlap(overlap int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.overlap = overlap
	}
}

// NewFixedSizeChunking creates a new fixed-size chunking strategy with options.
func NewFixedSizeChunking(opts ...Option) *FixedSizeChunking {
	fsc := &FixedSizeChunking{
		chunkSize: defaultChunkSize,
		overlap:   defaultOverlap,
	}
	// Apply options.
	for _, opt := range opts {
		opt(fsc)
	}
	// Validate parameters.
	if fsc.overlap >= fsc.chunkSize {
		fsc.overlap = min(defaultOverlap, fsc.chunkSize-1)
	}
	return fsc
}

// Chunk splits the document into fixed-size chunks with optional overlap.
func (f *FixedSizeChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)
	contentLength := len(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= f.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	var chunks []*document.Document
	chunkNumber := 1
	start := 0

	for start+f.overlap < contentLength {
		end := min(start+f.chunkSize, contentLength)

		// Try to find a good break point (whitespace) to avoid splitting words.
		if end < contentLength {
			breakPoint := f.findBreakPoint(content, start, end)
			// Ensure the break point actually advances beyond the current
			// overlap window; otherwise, keep the original end to guarantee
			// forward progress and avoid an infinite loop.
			if breakPoint != -1 && breakPoint-start > f.overlap {
				end = breakPoint
			}
		}

		// Guard against pathological cases where the chosen end does not
		// advance the cursor sufficiently. This can happen when the first
		// whitespace is too close to the start (<= overlap), causing
		// start to remain unchanged and leading to an infinite loop.
		if end-start <= f.overlap {
			end = min(start+f.chunkSize, contentLength)
		}

		// If we still couldn't find a good break point, use the original end.
		if end == start {
			end = start + f.chunkSize
		}

		chunkContent := content[start:end]
		chunk := createChunk(doc, chunkContent, chunkNumber)
		chunks = append(chunks, chunk)

		chunkNumber++
		// If we've reached the end of the content, break to avoid an extra
		// iteration that would violate the loop condition.
		if end == contentLength {
			break
		}
		start = end - f.overlap
	}
	return chunks, nil
}

// findBreakPoint looks for a suitable break point near the target position.
func (f *FixedSizeChunking) findBreakPoint(content string, start, targetEnd int) int {
	// Search backwards from target end to find whitespace.
	for i := targetEnd - 1; i > start; i-- {
		if isWhitespace(rune(content[i])) {
			return i + 1 // Return position after the whitespace.
		}
	}
	return -1 // No suitable break point found.
}

// isWhitespace checks if a character is considered whitespace.
func isWhitespace(char rune) bool {
	return char == ' ' || char == '\n' || char == '\r' || char == '\t'
}
