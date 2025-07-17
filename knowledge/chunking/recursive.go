//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package chunking provides document chunking strategies and utilities.
package chunking

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// RecursiveChunking implements a recursive chunking strategy that uses a hierarchy of separators.
type RecursiveChunking struct {
	chunkSize  int
	overlap    int
	separators []string
}

// RecursiveOption represents a functional option for configuring RecursiveChunking.
type RecursiveOption func(*RecursiveChunking)

// WithRecursiveChunkSize sets the maximum size of each chunk in characters.
func WithRecursiveChunkSize(size int) RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.chunkSize = size
	}
}

// WithRecursiveOverlap sets the number of characters to overlap between chunks.
func WithRecursiveOverlap(overlap int) RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.overlap = overlap
	}
}

// WithRecursiveSeparators sets the separators to use in priority order.
func WithRecursiveSeparators(separators []string) RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.separators = separators
	}
}

// NewRecursiveChunking creates a new recursive chunking strategy with options.
func NewRecursiveChunking(opts ...RecursiveOption) *RecursiveChunking {
	rc := &RecursiveChunking{
		chunkSize:  defaultChunkSize,
		overlap:    defaultOverlap,
		separators: []string{"\n\n", "\n", " ", ""}, // Default separators in priority order.
	}
	// Apply options.
	for _, opt := range opts {
		opt(rc)
	}
	// Validate parameters.
	if rc.overlap >= rc.chunkSize {
		rc.overlap = min(defaultOverlap, rc.chunkSize-1)
	}
	return rc
}

// Chunk splits the document using true recursive logic with separator hierarchy.
func (r *RecursiveChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)
	chunks := r.recursiveSplit(content, r.separators, doc, 1)

	// Apply overlap if specified.
	if r.overlap > 0 {
		chunks = r.applyOverlap(chunks)
	}
	return chunks, nil
}

// recursiveSplit is the core recursive function that splits text using separator hierarchy.
func (r *RecursiveChunking) recursiveSplit(
	text string, separators []string, originalDoc *document.Document, startChunkNumber int,
) []*document.Document {
	if len(text) <= r.chunkSize {
		chunk := createChunk(originalDoc, text, startChunkNumber)
		return []*document.Document{chunk}
	}

	if len(separators) == 0 {
		// No more separators, force split at chunk size.
		chunk := createChunk(originalDoc, text[:r.chunkSize], startChunkNumber)
		return []*document.Document{chunk}
	}

	// Try current separator.
	separator := separators[0]
	var splits []string

	if separator == "" {
		// Empty separator means split by character.
		splits = strings.Split(text, "")
	} else {
		splits = strings.Split(text, separator)
	}

	var chunks []*document.Document
	chunkNumber := startChunkNumber

	for _, split := range splits {
		if len(split) == 0 {
			continue
		}

		if len(split) <= r.chunkSize {
			// Split is small enough, create chunk.
			chunk := createChunk(originalDoc, split, chunkNumber)
			chunks = append(chunks, chunk)
			chunkNumber++
		} else {
			// Split is too large, recursively try next separator.
			if len(separators) > 1 {
				subChunks := r.recursiveSplit(split, separators[1:], originalDoc, chunkNumber)
				chunks = append(chunks, subChunks...)
				chunkNumber += len(subChunks)
			} else {
				// No more separators, force split at chunk size.
				for i := 0; i < len(split); i += r.chunkSize {
					end := min(i+r.chunkSize, len(split))
					chunk := createChunk(originalDoc, split[i:end], chunkNumber)
					chunks = append(chunks, chunk)
					chunkNumber++
				}
			}
		}
	}
	return chunks
}

// applyOverlap applies overlap between consecutive chunks.
func (r *RecursiveChunking) applyOverlap(chunks []*document.Document) []*document.Document {
	if len(chunks) <= 1 {
		return chunks
	}
	overlappedChunks := []*document.Document{chunks[0]}
	for i := 1; i < len(chunks); i++ {
		prevText := chunks[i-1].Content
		if len(prevText) > r.overlap {
			prevText = prevText[len(prevText)-r.overlap:]
		}

		// Create new metadata for overlapped chunk.
		metadata := make(map[string]interface{})
		for k, v := range chunks[i].Metadata {
			metadata[k] = v
		}

		overlappedContent := prevText + chunks[i].Content
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
