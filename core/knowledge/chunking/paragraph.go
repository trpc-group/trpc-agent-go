package chunking

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// ParagraphChunking implements a chunking strategy based on document paragraph structure.
// It attempts to keep paragraphs intact and groups them into appropriately sized chunks.
type ParagraphChunking struct {
	opts *options
}

// NewParagraphChunking creates a new paragraph-based chunking strategy.
func NewParagraphChunking(opts ...Option) (*ParagraphChunking, error) {
	options := buildOptions(opts...)

	if err := options.validate(); err != nil {
		return nil, err
	}
	return &ParagraphChunking{
		opts: options,
	}, nil
}

// Chunk splits the document based on paragraph structure while respecting size constraints.
func (p *ParagraphChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if doc == nil {
		return nil, document.ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, document.ErrEmptyDocument
	}

	content := cleanText(doc.Content)
	contentLength := len(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= p.opts.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	// Split content into paragraphs (double newlines indicate paragraph breaks).
	paragraphs := p.splitIntoParagraphs(content)

	if len(paragraphs) == 0 {
		return nil, document.ErrEmptyDocument
	}

	// Group paragraphs into chunks.
	chunks := p.groupParagraphsIntoChunks(doc, paragraphs)

	// Apply overlap if specified.
	if p.opts.overlap > 0 {
		chunks = p.applyOverlap(doc, chunks)
	}
	return chunks, nil
}

// splitIntoParagraphs splits content into individual paragraphs.
func (p *ParagraphChunking) splitIntoParagraphs(content string) []string {
	// Split on double newlines (paragraph separators).
	paragraphs := strings.Split(content, document.ParagraphSeparator)

	// Filter out empty paragraphs and trim whitespace.
	var result []string
	for _, para := range paragraphs {
		trimmed := strings.TrimSpace(para)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// groupParagraphsIntoChunks groups paragraphs into chunks while respecting size limits.
func (p *ParagraphChunking) groupParagraphsIntoChunks(doc *document.Document, paragraphs []string) []*document.Document {
	var chunks []*document.Document
	var currentChunk []string
	currentSize := 0
	chunkNumber := 1
	separatorSize := len(document.ParagraphSeparator)

	for _, para := range paragraphs {
		paraSize := len(para)

		// If adding this paragraph would exceed chunk size, finalize current chunk.
		if len(currentChunk) > 0 && currentSize+paraSize+separatorSize > p.opts.chunkSize {
			chunkContent := strings.Join(currentChunk, document.ParagraphSeparator)
			chunk := createChunk(doc, chunkContent, chunkNumber)
			chunks = append(chunks, chunk)

			// Start new chunk.
			currentChunk = []string{para}
			currentSize = paraSize
			chunkNumber++
		} else {
			// Add paragraph to current chunk.
			currentChunk = append(currentChunk, para)
			if len(currentChunk) == 1 {
				currentSize = paraSize
			} else {
				currentSize += paraSize + separatorSize
			}
		}
	}

	// Don't forget the last chunk.
	if len(currentChunk) > 0 {
		chunkContent := strings.Join(currentChunk, document.ParagraphSeparator)
		chunk := createChunk(doc, chunkContent, chunkNumber)
		chunks = append(chunks, chunk)
	}
	return chunks
}

// applyOverlap adds overlap between consecutive chunks.
func (p *ParagraphChunking) applyOverlap(doc *document.Document, chunks []*document.Document) []*document.Document {
	if len(chunks) <= 1 {
		return chunks
	}

	var overlappedChunks []*document.Document

	for i, chunk := range chunks {
		if i == 0 {
			// First chunk doesn't need overlap from previous.
			overlappedChunks = append(overlappedChunks, chunk)
		} else {
			// Add overlap from previous chunk.
			prevContent := chunks[i-1].Content
			overlapContent := p.extractOverlap(prevContent, p.opts.overlap)

			var newContent string
			if overlapContent != "" {
				newContent = overlapContent + document.ParagraphSeparator + chunk.Content
			} else {
				newContent = chunk.Content
			}

			// Create new chunk with overlap.
			newChunk := createChunk(doc, newContent, i+1)
			overlappedChunks = append(overlappedChunks, newChunk)
		}
	}
	return overlappedChunks
}

// extractOverlap extracts the last `overlapSize` characters from content.
func (p *ParagraphChunking) extractOverlap(content string, overlapSize int) string {
	if len(content) <= overlapSize {
		return content
	}
	return content[len(content)-overlapSize:]
}
