//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
