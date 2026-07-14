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
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestFixedSizeChunking_Errors(t *testing.T) {
	fsc := NewFixedSizeChunking()

	// Nil document should return ErrNilDocument.
	chunks, err := fsc.Chunk(nil)
	require.ErrorIs(t, err, ErrNilDocument)
	require.Nil(t, chunks)

	// Empty document should return ErrEmptyDocument.
	emptyDoc := &document.Document{ID: "empty", Content: ""}
	_, err = fsc.Chunk(emptyDoc)
	require.ErrorIs(t, err, ErrEmptyDocument)
}

// TestFixedSizeChunking_OverlapValidation tests overlap >= chunkSize boundary condition.
func TestFixedSizeChunking_OverlapValidation(t *testing.T) {
	tests := []struct {
		name      string
		chunkSize int
		overlap   int
	}{
		{
			name:      "overlap greater than chunkSize",
			chunkSize: 10,
			overlap:   15, // overlap > chunkSize, should be adjusted
		},
		{
			name:      "overlap equal to chunkSize",
			chunkSize: 20,
			overlap:   20, // overlap == chunkSize, should be adjusted
		},
		{
			name:      "very large overlap",
			chunkSize: 5,
			overlap:   100, // much larger overlap
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsc := NewFixedSizeChunking(
				WithChunkSize(tt.chunkSize),
				WithOverlap(tt.overlap),
			)

			// The chunker should still work despite invalid overlap
			doc := &document.Document{ID: "test", Content: "This is a test content for chunking validation"}
			chunks, err := fsc.Chunk(doc)
			require.NoError(t, err)
			require.NotEmpty(t, chunks, "should produce at least one chunk")
		})
	}
}

func TestFixedSizeChunking_SplitOverlap(t *testing.T) {
	const (
		chunkSize = 8
		overlap   = 2
	)

	// Create content longer than chunkSize to trigger splitting.
	content := strings.Repeat("abcdefghij", 3) // 30 characters.
	doc := &document.Document{
		ID:      "doc-1",
		Content: content,
	}

	fsc := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithOverlap(overlap),
	)

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "expected multiple chunks due to small chunk size")
	contents := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		contents = append(contents, chunk.Content)
	}
	require.Equal(t, []string{"abcdefgh", "ghijabcd", "cdefghij", "ijabcdef", "efghij"}, contents)

	for i, chunk := range chunks {
		chunkRunes := utf8.RuneCountInString(chunk.Content)
		require.LessOrEqual(t, chunkRunes, chunkSize, "chunk %d exceeds chunk size", i)
		require.Equal(t, chunkRunes, chunk.Metadata[source.MetaChunkSize])
		if i == 0 {
			continue
		}

		// Ensure overlap between consecutive chunks.
		prev := chunks[i-1].Content
		curr := chunk.Content
		suffix := string([]rune(prev)[utf8.RuneCountInString(prev)-overlap:])
		prefix := string([]rune(curr)[:overlap])
		require.Equal(t, suffix, prefix, "chunks do not overlap as expected")
	}

	reconstructed := []rune(chunks[0].Content)
	for _, chunk := range chunks[1:] {
		reconstructed = append(reconstructed, []rune(chunk.Content)[overlap:]...)
	}
	require.Equal(t, content, string(reconstructed))
}

func TestFixedSizeChunking_UnicodeOverlapWithinChunkSize(t *testing.T) {
	const (
		chunkSize = 4
		overlap   = 1
	)
	doc := &document.Document{ID: "unicode", Content: "甲乙丙丁戊己庚辛"}
	fsc := NewFixedSizeChunking(WithChunkSize(chunkSize), WithOverlap(overlap))

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Equal(t, []string{"甲乙丙丁", "丁戊己庚", "庚辛"}, []string{
		chunks[0].Content,
		chunks[1].Content,
		chunks[2].Content,
	})
	for i, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk.Content))
		require.LessOrEqual(t, utf8.RuneCountInString(chunk.Content), chunkSize, "chunk %d exceeds chunk size", i)
	}
}

func TestFixedSizeChunking_WithoutOverlapUnchanged(t *testing.T) {
	doc := &document.Document{ID: "no-overlap", Content: "abcdefghij"}
	fsc := NewFixedSizeChunking(WithChunkSize(4), WithOverlap(0))

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Equal(t, []string{"abcd", "efgh", "ij"}, []string{
		chunks[0].Content,
		chunks[1].Content,
		chunks[2].Content,
	})
}
