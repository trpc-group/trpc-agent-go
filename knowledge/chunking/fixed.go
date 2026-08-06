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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// FixedSizeChunking implements a chunking strategy that splits text into fixed-size chunks.
type FixedSizeChunking struct {
	chunkSize      int
	overlap        int
	preserveLines  bool
	trimWhitespace bool
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

// WithWhitespaceTrimming enables the legacy behavior that trims leading and
// trailing whitespace from the document, every line, and chunk boundaries.
func WithWhitespaceTrimming() Option {
	return func(fsc *FixedSizeChunking) {
		fsc.trimWhitespace = true
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

	content := cleanTextWithWhitespaceTrimming(
		doc.Content,
		f.trimWhitespace,
	)
	if isBlankText(content) {
		return nil, ErrEmptyDocument
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
	split := func(content string, maxSize int) (string, string) {
		return splitTextAtNaturalBoundaryWithWhitespaceTrimming(
			content,
			maxSize,
			f.trimWhitespace,
		)
	}
	if f.preserveLines {
		textChunks = splitFixedLines(
			content,
			f.chunkSize,
			coreSize,
			split,
			f.trimWhitespace,
		)
	} else {
		splitChunks := splitFixedText(
			content,
			f.chunkSize,
			coreSize,
			split,
			true,
			f.trimWhitespace,
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
	separators := sourceChunkSeparators(
		content,
		rawContents,
		" ",
		f.trimWhitespace,
	)
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
			finalContent, actualOverlap = joinWithOverlapSeparatorMode(
				chunks[i-1].Content,
				textChunk.content,
				f.overlap,
				f.chunkSize,
				separator,
				preserveSeparator,
				f.trimWhitespace,
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
	split func(string, int) (string, string),
	trimWhitespace bool,
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
		if !hasCurrent {
			current = ""
			hasCurrent = false
			return
		}
		if trimWhitespace && strings.TrimSpace(current) == "" {
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
			split,
			true,
			trimWhitespace,
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
	trimWhitespace bool,
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
			chunk, rest = splitTextWithBalancedTailAndWhitespaceTrimming(
				remaining,
				chunkSize,
				split,
				trimWhitespace,
			)
		}
		if chunk == "" {
			// Keep a hard-split fallback to guarantee progress when a custom
			// splitter returns an empty prefix.
			hardChunks := encoding.SafeSplitBySize(remaining, chunkSize)
			chunk = hardChunks[0]
			rest = remaining[len(chunk):]
		}
		chunks = append(chunks, chunk)
		remaining = rest
	}
	return chunks
}
