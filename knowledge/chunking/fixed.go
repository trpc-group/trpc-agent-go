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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// FixedSizeChunking implements a chunking strategy that splits text into fixed-size chunks.
type FixedSizeChunking struct {
	chunkSize     int
	overlap       int
	preserveLines bool
	lengthFunc    func(string) (int, error)
}

// Option represents a functional option for configuring FixedSizeChunking.
type Option func(*FixedSizeChunking)

// WithChunkSize sets the maximum size of each chunk. The unit is Unicode runes
// unless WithLengthFunc is configured.
func WithChunkSize(size int) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.chunkSize = size
	}
}

// WithOverlap sets the maximum overlap between chunks. The unit is Unicode
// runes unless WithLengthFunc is configured.
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

// WithLengthFunc sets the function used to measure chunk size and overlap.
// By default, FixedSizeChunking measures Unicode runes. The function must
// return a deterministic, non-negative length that broadly grows with its
// input. Local non-monotonic behavior from tokenizers is supported.
func WithLengthFunc(lengthFunc func(string) (int, error)) Option {
	return func(fsc *FixedSizeChunking) {
		fsc.lengthFunc = lengthFunc
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
	return fsc
}

// Chunk splits the document into fixed-size chunks with optional overlap.
func (f *FixedSizeChunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if err := validateChunkConfig(f.chunkSize, f.overlap); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, ErrEmptyDocument
	}

	content := cleanText(doc.Content)
	if f.lengthFunc != nil {
		return f.chunkByLength(doc, content)
	}
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
	var textChunks []fixedTextChunk
	if f.preserveLines {
		textChunks = splitFixedLines(content, f.chunkSize, coreSize)
	} else {
		splitChunks := splitFixedText(
			content,
			f.chunkSize,
			coreSize,
			splitTextAtNaturalBoundary,
			true,
		)
		textChunks = make([]fixedTextChunk, 0, len(splitChunks))
		for _, chunk := range splitChunks {
			textChunks = append(textChunks, fixedTextChunk{content: chunk})
		}
	}
	chunks := make([]*document.Document, 0, len(textChunks))
	rawContents := make([]string, len(textChunks))
	for i, textChunk := range textChunks {
		rawContents[i] = textChunk.content
	}
	separators := sourceChunkSeparators(content, rawContents, " ")
	for i, textChunk := range textChunks {
		finalContent := textChunk.content
		actualOverlap := 0
		if i > 0 {
			separator := separators[i]
			preserveSeparator := false
			if f.preserveLines {
				separator = ""
				if textChunk.startsNewLine {
					separator = "\n"
					preserveSeparator = true
				}
			}
			finalContent, actualOverlap = joinWithOverlapSeparator(
				chunks[i-1].Content,
				textChunk.content,
				f.overlap,
				f.chunkSize,
				separator,
				preserveSeparator,
			)
		}
		chunk := createChunk(doc, textChunk.content, i+1)
		chunk.Content = finalContent
		if actualOverlap > 0 {
			chunk.Metadata[source.MetaOverlappedContentSize] =
				encoding.RuneCount(finalContent)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (f *FixedSizeChunking) chunkByLength(
	doc *document.Document,
	content string,
) ([]*document.Document, error) {
	contentLength, err := measureTextLength(f.lengthFunc, content)
	if err != nil {
		return nil, fmt.Errorf("fixed-size chunking: %w", err)
	}
	if contentLength <= f.chunkSize {
		return []*document.Document{createChunk(doc, content, 1)}, nil
	}

	coreSize := f.chunkSize
	if f.overlap > 0 {
		coreSize = f.chunkSize - f.overlap
	}
	var textChunks []fixedTextChunk
	if f.preserveLines {
		textChunks, err = splitFixedLinesByLength(
			content,
			f.chunkSize,
			coreSize,
			f.lengthFunc,
		)
	} else {
		var chunks []string
		chunks, err = splitFixedTextByLength(
			content,
			f.chunkSize,
			coreSize,
			f.lengthFunc,
		)
		textChunks = make([]fixedTextChunk, 0, len(chunks))
		for _, chunk := range chunks {
			textChunks = append(textChunks, fixedTextChunk{content: chunk})
		}
	}
	if err != nil {
		return nil, fmt.Errorf("fixed-size chunking: %w", err)
	}

	rawContents := make([]string, len(textChunks))
	for i, textChunk := range textChunks {
		rawContents[i] = textChunk.content
	}
	separators := sourceChunkSeparators(content, rawContents, " ")
	chunks := make([]*document.Document, 0, len(textChunks))
	for i, textChunk := range textChunks {
		finalContent := textChunk.content
		actualOverlap := 0
		if i > 0 {
			separator := separators[i]
			preserveSeparator := false
			if f.preserveLines {
				separator = ""
				if textChunk.startsNewLine {
					separator = "\n"
					preserveSeparator = true
				}
			}
			finalContent, actualOverlap, err =
				joinWithOverlapSeparatorByLength(
					chunks[i-1].Content,
					textChunk.content,
					f.overlap,
					f.chunkSize,
					separator,
					preserveSeparator,
					f.lengthFunc,
				)
			if err != nil {
				return nil, fmt.Errorf(
					"fixed-size chunking: add overlap: %w",
					err,
				)
			}
		}
		finalSize, err := measureTextLength(f.lengthFunc, finalContent)
		if err != nil {
			return nil, fmt.Errorf("fixed-size chunking: %w", err)
		}
		if finalSize > f.chunkSize {
			return nil, fmt.Errorf(
				"fixed-size chunking: final chunk %d has length %d, exceeds chunk size %d",
				i+1,
				finalSize,
				f.chunkSize,
			)
		}
		chunk := createChunk(doc, textChunk.content, i+1)
		chunk.Content = finalContent
		if actualOverlap > 0 {
			chunk.Metadata[source.MetaOverlappedContentSize] =
				encoding.RuneCount(finalContent)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

type fixedTextChunk struct {
	content       string
	startsNewLine bool
}

func splitFixedLines(
	content string,
	firstChunkSize int,
	nextChunkSize int,
) []fixedTextChunk {
	if nextChunkSize <= 0 {
		nextChunkSize = firstChunkSize
	}
	var chunks []fixedTextChunk
	var current string
	hasCurrent := false

	chunkSize := func() int {
		if len(chunks) == 0 {
			return firstChunkSize
		}
		return nextChunkSize
	}
	flush := func() {
		if !hasCurrent || strings.TrimSpace(current) == "" {
			current = ""
			hasCurrent = false
			return
		}
		chunks = append(chunks, fixedTextChunk{
			content:       current,
			startsNewLine: true,
		})
		current = ""
		hasCurrent = false
	}

	for _, line := range strings.Split(content, "\n") {
		if hasCurrent {
			candidate := current + "\n" + line
			if encoding.RuneCount(candidate) <= chunkSize() {
				current = candidate
				continue
			}
			flush()
		}

		if encoding.RuneCount(line) <= chunkSize() {
			current = line
			hasCurrent = true
			continue
		}

		pieces := splitFixedText(
			line,
			chunkSize(),
			nextChunkSize,
			splitTextAtNaturalBoundary,
			true,
		)
		for i, piece := range pieces {
			chunks = append(chunks, fixedTextChunk{
				content:       piece,
				startsNewLine: i == 0,
			})
		}
	}
	flush()
	return chunks
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

func splitFixedLinesByLength(
	content string,
	firstChunkSize int,
	nextChunkSize int,
	lengthFunc func(string) (int, error),
) ([]fixedTextChunk, error) {
	if nextChunkSize <= 0 {
		nextChunkSize = firstChunkSize
	}
	var chunks []fixedTextChunk
	var current string
	hasCurrent := false

	chunkSize := func() int {
		if len(chunks) == 0 {
			return firstChunkSize
		}
		return nextChunkSize
	}
	flush := func() {
		if !hasCurrent || strings.TrimSpace(current) == "" {
			current = ""
			hasCurrent = false
			return
		}
		chunks = append(chunks, fixedTextChunk{
			content:       current,
			startsNewLine: true,
		})
		current = ""
		hasCurrent = false
	}

	for _, line := range strings.Split(content, "\n") {
		if hasCurrent {
			candidate := current + "\n" + line
			candidateSize, err := measureTextLength(
				lengthFunc,
				candidate,
			)
			if err != nil {
				return nil, err
			}
			if candidateSize <= chunkSize() {
				current = candidate
				continue
			}
			flush()
		}

		lineSize, err := measureTextLength(lengthFunc, line)
		if err != nil {
			return nil, err
		}
		if lineSize <= chunkSize() {
			current = line
			hasCurrent = true
			continue
		}

		pieces, err := splitFixedTextByLength(
			line,
			chunkSize(),
			nextChunkSize,
			lengthFunc,
		)
		if err != nil {
			return nil, err
		}
		for i, piece := range pieces {
			chunks = append(chunks, fixedTextChunk{
				content:       piece,
				startsNewLine: i == 0,
			})
		}
	}
	flush()
	return chunks, nil
}

func splitFixedTextByLength(
	content string,
	firstChunkSize int,
	nextChunkSize int,
	lengthFunc func(string) (int, error),
) ([]string, error) {
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
		chunk, rest, err := splitTextWithBalancedTailByLength(
			remaining,
			chunkSize,
			lengthFunc,
		)
		if err != nil {
			return nil, err
		}
		if chunk == "" {
			return nil, fmt.Errorf(
				"unable to make progress with chunk size %d",
				chunkSize,
			)
		}
		chunks = append(chunks, chunk)
		remaining = rest
	}
	return chunks, nil
}
