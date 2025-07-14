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

func TestMarkdownChunking_BasicOverlap(t *testing.T) {
	md := `# Header 1

Paragraph one with some text to exceed size.

## Header 2

Second paragraph more text.`

	doc := &document.Document{ID: "md", Content: md}

	const size = 40
	const overlap = 5

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	// Validate each chunk size and overlap.
	for i, c := range chunks {
		// Ensure chunk size not huge (>2*size).
		require.LessOrEqual(t, len(c.Content), 2*size)
		if i > 0 {
			prev := chunks[i-1].Content
			suffix := prev[len(prev)-overlap:]
			prefix := c.Content[:overlap]
			require.Equal(t, suffix, prefix)
		}
	}
}

func TestMarkdownChunking_Errors(t *testing.T) {
	mc := NewMarkdownChunking()

	_, err := mc.Chunk(nil)
	require.ErrorIs(t, err, ErrNilDocument)

	empty := &document.Document{ID: "e", Content: ""}
	_, err = mc.Chunk(empty)
	require.ErrorIs(t, err, ErrEmptyDocument)
}

func TestRecursiveChunking_CustomSep(t *testing.T) {
	text := strings.Repeat("A B C D E F ", 10) // 70 chars
	doc := &document.Document{ID: "txt", Content: text}

	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(25),
		WithRecursiveOverlap(3),
		WithRecursiveSeparators([]string{" ", ""}),
	)

	chunks, err := rc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 2)

	// Each chunk <= 25 and overlap 3.
	for i, c := range chunks {
		require.LessOrEqual(t, len(c.Content), 25)
		if i > 0 {
			prev := chunks[i-1].Content
			if len(prev) >= 3 && len(c.Content) >= 3 {
				overlap := prev[len(prev)-3:]
				require.Equal(t, overlap, c.Content[:3])
			}
		}
	}
}
