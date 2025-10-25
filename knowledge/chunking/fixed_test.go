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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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

	// Verify the first chunk does not exceed chunkSize.
	require.LessOrEqual(t, len(chunks[0].Content), chunkSize)

	// Ensure overlap between consecutive chunks.
	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1].Content
		curr := chunks[i].Content
		suffix := prev[len(prev)-overlap:]
		prefix := curr[:overlap]
		require.Equal(t, suffix, prefix, "chunks do not overlap as expected")
	}
}
