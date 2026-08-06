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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// RecursiveChunking implements a recursive chunking strategy that uses a hierarchy of separators.
type RecursiveChunking struct {
	chunkSize      int
	overlap        int
	separators     []string
	trimWhitespace bool
}

// RecursiveOption represents a functional option for configuring RecursiveChunking.
type RecursiveOption func(*RecursiveChunking)

// WithRecursiveChunkSize sets the maximum size of each chunk in Unicode runes.
func WithRecursiveChunkSize(size int) RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.chunkSize = size
	}
}

// WithRecursiveOverlap sets the maximum number of Unicode runes to overlap between chunks.
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

// WithRecursiveWhitespaceTrimming enables the legacy behavior that trims
// leading and trailing whitespace from the document, every line, and chunk
// boundaries.
func WithRecursiveWhitespaceTrimming() RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.trimWhitespace = true
	}
}

// NewRecursiveChunking creates a new recursive chunking strategy with options.
func NewRecursiveChunking(opts ...RecursiveOption) *RecursiveChunking {
	rc := &RecursiveChunking{
		chunkSize: defaultChunkSize,
		overlap:   defaultOverlap,
		separators: []string{
			"\n\n",
			"\n",
			"。", "！", "？",
			". ", "! ", "? ",
			"; ", "；",
			", ", "，",
			" ",
			"",
		},
	}
	// Apply options.
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}

// Chunk splits the document using true recursive logic with separator hierarchy.
func (r *RecursiveChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if err := validateChunkConfig(r.chunkSize, r.overlap); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanTextWithWhitespaceTrimming(
		doc.Content,
		r.trimWhitespace,
	)
	if isBlankText(content) {
		return nil, ErrEmptyDocument
	}
	coreSize := r.chunkSize
	if r.overlap > 0 {
		coreSize = r.chunkSize - r.overlap
	}
	fragments := r.recursiveSplit(content, r.separators, coreSize)
	textChunks := r.mergeFragments(fragments, r.chunkSize, coreSize)
	chunks := make([]*document.Document, 0, len(textChunks))
	for i, chunkText := range textChunks {
		chunks = append(chunks, createChunk(doc, chunkText, i+1))
	}

	// Apply overlap if specified.
	if r.overlap > 0 {
		chunks = r.applyOverlap(content, chunks)
	}
	return chunks, nil
}

// recursiveSplit is the core recursive function that splits text using separator hierarchy.
func (r *RecursiveChunking) recursiveSplit(
	text string,
	separators []string,
	maxSize int,
) []string {
	if encoding.RuneCount(text) <= maxSize {
		return []string{text}
	}

	if len(separators) == 0 {
		return []string{text}
	}

	separator := separators[0]
	if separator == "" {
		// Keep the unbroken text intact here. mergeFragments applies the final
		// UTF-8-safe rune fallback while filling the active chunk budget.
		return []string{text}
	}
	if !strings.Contains(text, separator) {
		return r.recursiveSplit(text, separators[1:], maxSize)
	}

	// Keep separators attached while recursively refining oversized pieces.
	// This lets the merge step rebuild readable paragraphs and sentences.
	splits := strings.SplitAfter(text, separator)
	separatorRunes := []rune(separator)
	if len(separatorRunes) == 1 &&
		isSentencePunctuation(separatorRunes[0]) {
		lastNonEmpty := -1
		for i := range splits {
			splitRunes := []rune(splits[i])
			clusterEnd := 0
			for clusterEnd < len(splitRunes) &&
				isSentencePunctuation(splitRunes[clusterEnd]) {
				clusterEnd++
			}
			if lastNonEmpty >= 0 && clusterEnd > 0 {
				splits[lastNonEmpty] += string(splitRunes[:clusterEnd])
				splits[i] = string(splitRunes[clusterEnd:])
			}
			if splits[i] != "" {
				lastNonEmpty = i
			}
		}
	}
	var fragments []string
	for _, split := range splits {
		if split == "" {
			continue
		}
		if encoding.RuneCount(split) <= maxSize {
			fragments = append(fragments, split)
		} else {
			fragments = append(
				fragments,
				r.recursiveSplit(split, separators[1:], maxSize)...,
			)
		}
	}
	return fragments
}

func (r *RecursiveChunking) mergeFragments(
	fragments []string,
	firstChunkSize int,
	nextChunkSize int,
) []string {
	var chunks []string
	var current strings.Builder
	currentSize := 0

	flush := func() {
		content, ok := r.finalizeFragment(current.String())
		if ok {
			chunks = append(chunks, content)
		}
		current.Reset()
		currentSize = 0
	}

	for _, fragment := range fragments {
		if fragment == "" {
			continue
		}
		chunkSize := nextChunkSize
		if len(chunks) == 0 {
			chunkSize = firstChunkSize
		}
		fragmentSize := encoding.RuneCount(fragment)
		if currentSize+fragmentSize <= chunkSize {
			current.WriteString(fragment)
			currentSize += fragmentSize
			continue
		}
		if fragmentSize <= nextChunkSize {
			flush()
			current.WriteString(fragment)
			currentSize = fragmentSize
			continue
		}

		// No configured separator can refine this fragment. Fill the current
		// budget with rune-safe pieces and continue with the smaller overlap
		// core budget for subsequent chunks.
		remaining := []rune(fragment)
		for len(remaining) > 0 {
			chunkSize = nextChunkSize
			if len(chunks) == 0 {
				chunkSize = firstChunkSize
			}
			available := chunkSize - currentSize
			if available <= 0 {
				flush()
				continue
			}
			take := min(available, len(remaining))
			rebalanced := false
			if len(remaining) > available {
				minimumTailSize := max(1, available/2)
				tailSize := len(remaining) - take
				if tailSize < minimumTailSize {
					balancedTake := len(remaining) - minimumTailSize
					if balancedTake > 0 && balancedTake <= available {
						take = balancedTake
						rebalanced = true
					}
				}
			}
			if safeTake := safeTextSplitPosition(remaining, take); safeTake != take {
				take = safeTake
				rebalanced = true
			}
			current.WriteString(string(remaining[:take]))
			currentSize += take
			remaining = remaining[take:]
			if currentSize == chunkSize || rebalanced {
				flush()
			}
		}
	}
	flush()
	return chunks
}

func (r *RecursiveChunking) finalizeFragment(content string) (string, bool) {
	if r.trimWhitespace {
		content = strings.TrimSpace(content)
	}
	return content, content != ""
}

// applyOverlap applies overlap between consecutive chunks.
func (r *RecursiveChunking) applyOverlap(
	content string,
	chunks []*document.Document,
) []*document.Document {
	if len(chunks) <= 1 {
		return chunks
	}
	rawContents := make([]string, len(chunks))
	for i, chunk := range chunks {
		rawContents[i] = chunk.Content
	}
	separators := sourceChunkSeparators(
		content,
		rawContents,
		" ",
		r.trimWhitespace,
	)
	overlappedChunks := []*document.Document{chunks[0]}
	for i := 1; i < len(chunks); i++ {
		// Create new metadata for overlapped chunk.
		metadata := make(map[string]any)
		for k, v := range chunks[i].Metadata {
			metadata[k] = v
		}

		overlappedContent, actualOverlap := joinWithOverlapMode(
			overlappedChunks[len(overlappedChunks)-1].Content,
			chunks[i].Content,
			r.overlap,
			r.chunkSize,
			separators[i],
			r.trimWhitespace,
		)
		if actualOverlap > 0 {
			metadata[source.MetaOverlappedContentSize] =
				encoding.RuneCount(overlappedContent)
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
	return overlappedChunks
}
