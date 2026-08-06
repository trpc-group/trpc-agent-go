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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// RecursiveChunking implements a recursive chunking strategy that uses a hierarchy of separators.
type RecursiveChunking struct {
	chunkSize  int
	overlap    int
	separators []string
	lengthFunc func(string) (int, error)
}

// RecursiveOption represents a functional option for configuring RecursiveChunking.
type RecursiveOption func(*RecursiveChunking)

// WithRecursiveChunkSize sets the maximum size of each chunk. The unit is
// Unicode runes unless WithRecursiveLengthFunc is configured.
func WithRecursiveChunkSize(size int) RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.chunkSize = size
	}
}

// WithRecursiveOverlap sets the maximum overlap between chunks. The unit is
// Unicode runes unless WithRecursiveLengthFunc is configured.
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

// WithRecursiveLengthFunc sets the function used to measure chunk size and
// overlap. By default, RecursiveChunking measures Unicode runes. The function
// must return a deterministic, non-negative length that broadly grows with its
// input. Local non-monotonic behavior from tokenizers is supported.
func WithRecursiveLengthFunc(
	lengthFunc func(string) (int, error),
) RecursiveOption {
	return func(rc *RecursiveChunking) {
		rc.lengthFunc = lengthFunc
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

	content := cleanText(doc.Content)
	if r.lengthFunc != nil {
		return r.chunkByLength(doc, content)
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

func (r *RecursiveChunking) chunkByLength(
	doc *document.Document,
	content string,
) ([]*document.Document, error) {
	coreSize := r.chunkSize
	if r.overlap > 0 {
		coreSize = r.chunkSize - r.overlap
	}
	fragments, err := r.recursiveSplitByLength(
		content,
		r.separators,
		coreSize,
	)
	if err != nil {
		return nil, fmt.Errorf("recursive chunking: %w", err)
	}
	textChunks, err := r.mergeFragmentsByLength(
		fragments,
		r.chunkSize,
		coreSize,
	)
	if err != nil {
		return nil, fmt.Errorf("recursive chunking: %w", err)
	}
	chunks := make([]*document.Document, 0, len(textChunks))
	for i, chunkText := range textChunks {
		chunks = append(chunks, createChunk(doc, chunkText, i+1))
	}
	if r.overlap > 0 {
		chunks, err = r.applyOverlapByLength(content, chunks)
		if err != nil {
			return nil, fmt.Errorf("recursive chunking: %w", err)
		}
	}
	for i, chunk := range chunks {
		size, err := measureTextLength(r.lengthFunc, chunk.Content)
		if err != nil {
			return nil, fmt.Errorf("recursive chunking: %w", err)
		}
		if size > r.chunkSize {
			return nil, fmt.Errorf(
				"recursive chunking: final chunk %d has length %d, exceeds chunk size %d",
				i+1,
				size,
				r.chunkSize,
			)
		}
	}
	return chunks, nil
}

func (r *RecursiveChunking) recursiveSplitByLength(
	text string,
	separators []string,
	maxSize int,
) ([]string, error) {
	textSize, err := measureTextLength(r.lengthFunc, text)
	if err != nil {
		return nil, err
	}
	if textSize <= maxSize {
		return []string{text}, nil
	}
	if len(separators) == 0 {
		return []string{text}, nil
	}

	separator := separators[0]
	if separator == "" {
		return []string{text}, nil
	}
	if !strings.Contains(text, separator) {
		return r.recursiveSplitByLength(
			text,
			separators[1:],
			maxSize,
		)
	}

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
		splitSize, err := measureTextLength(r.lengthFunc, split)
		if err != nil {
			return nil, err
		}
		if splitSize <= maxSize {
			fragments = append(fragments, split)
			continue
		}
		refined, err := r.recursiveSplitByLength(
			split,
			separators[1:],
			maxSize,
		)
		if err != nil {
			return nil, err
		}
		fragments = append(fragments, refined...)
	}
	return fragments, nil
}

func (r *RecursiveChunking) mergeFragmentsByLength(
	fragments []string,
	firstChunkSize int,
	nextChunkSize int,
) ([]string, error) {
	var chunks []string
	var current string

	flush := func() {
		content := strings.TrimSpace(current)
		if content != "" {
			chunks = append(chunks, content)
		}
		current = ""
	}

	for _, fragment := range fragments {
		remaining := fragment
		for remaining != "" {
			chunkSize := nextChunkSize
			if len(chunks) == 0 {
				chunkSize = firstChunkSize
			}
			candidate := current + remaining
			candidateSize, err := measureTextLength(
				r.lengthFunc,
				candidate,
			)
			if err != nil {
				return nil, err
			}
			if candidateSize <= chunkSize {
				current = candidate
				break
			}

			remainingSize, err := measureTextLength(
				r.lengthFunc,
				remaining,
			)
			if err != nil {
				return nil, err
			}
			if current != "" && remainingSize <= nextChunkSize {
				flush()
				continue
			}

			lengthFunc := r.lengthFunc
			if current != "" {
				currentContent := current
				firstRune := string([]rune(remaining)[:1])
				firstSize, err := measureTextLength(
					r.lengthFunc,
					currentContent+firstRune,
				)
				if err != nil {
					return nil, err
				}
				if firstSize > chunkSize {
					flush()
					continue
				}
				lengthFunc = func(text string) (int, error) {
					return measureTextLength(
						r.lengthFunc,
						currentContent+text,
					)
				}
			}

			var prefix, rest string
			if current == "" {
				prefix, rest, err = splitTextWithBalancedTailByLength(
					remaining,
					chunkSize,
					lengthFunc,
				)
			} else {
				prefix, rest, err = splitTextAtNaturalBoundaryByLength(
					remaining,
					chunkSize,
					lengthFunc,
				)
			}
			if err != nil {
				return nil, err
			}
			if prefix == "" {
				return nil, fmt.Errorf(
					"unable to split recursive fragment within chunk size %d",
					chunkSize,
				)
			}
			current += prefix
			remaining = rest
			if remaining != "" {
				flush()
			}
		}
	}
	flush()
	return chunks, nil
}

func (r *RecursiveChunking) applyOverlapByLength(
	content string,
	chunks []*document.Document,
) ([]*document.Document, error) {
	if len(chunks) <= 1 {
		return chunks, nil
	}
	rawContents := make([]string, len(chunks))
	for i, chunk := range chunks {
		rawContents[i] = chunk.Content
	}
	separators := sourceChunkSeparators(content, rawContents, " ")
	overlappedChunks := []*document.Document{chunks[0]}
	for i := 1; i < len(chunks); i++ {
		metadata := make(map[string]any)
		for key, value := range chunks[i].Metadata {
			metadata[key] = value
		}
		overlappedContent, actualOverlap, err :=
			joinWithOverlapSeparatorByLength(
				overlappedChunks[len(overlappedChunks)-1].Content,
				chunks[i].Content,
				r.overlap,
				r.chunkSize,
				separators[i],
				false,
				r.lengthFunc,
			)
		if err != nil {
			return nil, err
		}
		if actualOverlap > 0 {
			metadata[source.MetaOverlappedContentSize] =
				encoding.RuneCount(overlappedContent)
		}
		overlappedChunks = append(
			overlappedChunks,
			&document.Document{
				ID:        chunks[i].ID,
				Name:      chunks[i].Name,
				Content:   overlappedContent,
				Metadata:  metadata,
				CreatedAt: chunks[i].CreatedAt,
				UpdatedAt: chunks[i].UpdatedAt,
			},
		)
	}
	return overlappedChunks, nil
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
		content := strings.TrimSpace(current.String())
		if content != "" {
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
	separators := sourceChunkSeparators(content, rawContents, " ")
	overlappedChunks := []*document.Document{chunks[0]}
	for i := 1; i < len(chunks); i++ {
		// Create new metadata for overlapped chunk.
		metadata := make(map[string]any)
		for k, v := range chunks[i].Metadata {
			metadata[k] = v
		}

		overlappedContent, actualOverlap := joinWithOverlap(
			overlappedChunks[len(overlappedChunks)-1].Content,
			chunks[i].Content,
			r.overlap,
			r.chunkSize,
			separators[i],
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
