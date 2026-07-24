//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chunking

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// FixedSizeChunking implements a chunking strategy that splits text into fixed-size chunks.
type FixedSizeChunking struct {
	chunkSize     int
	overlap       int
	preserveLines bool
}

// Option represents a functional option for configuring FixedSizeChunking.
type Option func(*FixedSizeChunking)

// WithChunkSize sets the maximum size of each chunk in Unicode runes.
func WithChunkSize(size int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.chunkSize = size
	}
}

// WithOverlap sets the maximum number of Unicode runes to overlap between chunks.
func WithOverlap(overlap int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.overlap = overlap
	}
}

// WithPreserveLines keeps complete lines together whenever one line fits the
// chunk budget. Oversized lines still use natural text and rune boundaries.
func WithPreserveLines() Option {
	return func(fsc *FixedSizeChunking) {
		fsc.preserveLines = true
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
	contentLength := encoding.RuneCount(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= f.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	coreSize := f.chunkSize
	if f.overlap > 0 {
		coreSize = f.chunkSize - f.overlap
	}
	split := splitTextAtNaturalBoundary
	balanceTail := true
	if f.preserveLines {
		split = splitTextAtLineBoundary
		balanceTail = false
	}
	textChunks := splitFixedText(
		content,
		f.chunkSize,
		coreSize,
		split,
		balanceTail,
	)
	chunks := make([]*document.Document, 0, len(textChunks))
	for i, chunkText := range textChunks {
		finalContent := chunkText
		actualOverlap := 0
		if i > 0 {
			finalContent, actualOverlap = joinWithOverlap(
				chunks[i-1].Content,
				chunkText,
				f.overlap,
				f.chunkSize,
				" ",
			)
		}
		chunk := createChunk(doc, chunkText, i+1)
		chunk.Content = finalContent
		if actualOverlap > 0 {
			chunk.Metadata[source.MetaOverlappedContentSize] =
				encoding.RuneCount(finalContent)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func splitFixedText(
	content string,
	firstChunkSize int,
	nextChunkSize int,
	split func(string, int) (string, string),
	balanceTail bool,
) []string {
	if firstChunkSize <= 0 {
		return []string{content}
	}
	if nextChunkSize <= 0 {
		nextChunkSize = firstChunkSize
	}

	var chunks []string
	remaining := content
	for remaining != "" {
		chunkSize := nextChunkSize
		if len(chunks) == 0 {
			chunkSize = firstChunkSize
		}
		chunk, rest := split(remaining, chunkSize)
		if balanceTail {
			chunk, rest = splitTextWithBalancedTail(
				remaining,
				chunkSize,
				split,
			)
		}
		if chunk == "" {
			// The natural-boundary helper trims whitespace. Keep a hard-split
			// fallback to guarantee progress for whitespace-only input.
			hardChunks := encoding.SafeSplitBySize(remaining, chunkSize)
			chunk = hardChunks[0]
			rest = remaining[len(chunk):]
		}
		chunks = append(chunks, chunk)
		remaining = rest
	}
	return chunks
}
