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
	contentLength := encoding.RuneCount(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= f.chunkSize {
		chunk := createChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	var chunks []*document.Document
	if f.chunkSize <= 0 || f.overlap <= 0 {
		// Use UTF-8 safe splitting to ensure proper character boundaries.
		textChunks := encoding.SafeSplitBySize(content, f.chunkSize)
		for i, chunkText := range textChunks {
			chunks = append(chunks, createChunk(doc, chunkText, i+1))
		}
		return chunks, nil
	}

	// Build overlapping windows directly so overlap stays within chunkSize.
	contentRunes := []rune(content)
	step := f.chunkSize - f.overlap
	for start, chunkNumber := 0, 1; start < len(contentRunes); start, chunkNumber = start+step, chunkNumber+1 {
		end := min(start+f.chunkSize, len(contentRunes))
		chunks = append(chunks, createChunk(doc, string(contentRunes[start:end]), chunkNumber))
		if end == len(contentRunes) {
			break
		}
	}
	return chunks, nil
}
