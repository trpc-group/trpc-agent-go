//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !race
// +build !race

package chunking

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// TestRecursiveChunking_Errors verifies that error conditions are handled
// correctly when nil or empty documents are provided.
func TestRecursiveChunking_Errors(t *testing.T) {
	rc := NewRecursiveChunking()

	if _, err := rc.Chunk(nil); !errors.Is(err, ErrNilDocument) {
		t.Fatalf("expected ErrNilDocument, got %v", err)
	}

	emptyDoc := &document.Document{}
	if _, err := rc.Chunk(emptyDoc); !errors.Is(err, ErrEmptyDocument) {
		t.Fatalf("expected ErrEmptyDocument, got %v", err)
	}
}

// TestRecursiveChunking_Basic ensures that a large document is split into
// multiple chunks that respect the configured chunk size with no overlap.
func TestRecursiveChunking_Basic(t *testing.T) {
	const chunkSize = 50                            // small to keep test data short
	longText := strings.Repeat("a", chunkSize*3+10) // => 160 bytes

	doc := &document.Document{
		Name:    "basic",
		Content: longText,
		Metadata: map[string]any{
			"source": "unit-test",
		},
	}

	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(chunkSize),
		WithRecursiveOverlap(0),
		WithRecursiveSeparators([]string{"\n\n", "\n", " ", ""}),
	)

	chunks, err := rc.Chunk(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) <= 1 {
		t.Fatalf("expected more than one chunk, got %d", len(chunks))
	}

	for i, c := range chunks {
		if len(c.Content) > chunkSize {
			t.Fatalf("chunk %d exceeds size limit: %d > %d", i, len(c.Content), chunkSize)
		}
		// All chunks should inherit metadata plus chunk specific ones.
		if c.Metadata["source"] != "unit-test" {
			t.Fatalf("metadata not propagated in chunk %d", i)
		}
		if c.Metadata[source.MetaChunkIndex].(int) != i+1 {
			t.Fatalf("wrong chunk index, expected %d got %v", i+1, c.Metadata[source.MetaChunkIndex])
		}
	}
}

// TestRecursiveChunking_Overlap confirms that overlap characters are correctly
// prefixed to all chunks except the first.
func TestRecursiveChunking_Overlap(t *testing.T) {
	const (
		size    = 30
		overlap = 10
	)

	// Create a string of 3×size characters to guarantee >1 chunk.
	builder := strings.Builder{}
	for i := 0; i < size*3; i++ {
		builder.WriteByte(byte('A' + (i % 26)))
	}
	doc := &document.Document{Content: builder.String()}

	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(size),
		WithRecursiveOverlap(overlap),
	)

	chunks, err := rc.Chunk(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) < 2 {
		t.Fatalf("need at least two chunks to test overlap, got %d", len(chunks))
	}

	firstTail := chunks[0].Content
	if len(firstTail) > overlap {
		firstTail = firstTail[len(firstTail)-overlap:]
	}

	// The second chunk must start with `firstTail`.
	if got := chunks[1].Content[:len(firstTail)]; got != firstTail {
		t.Fatalf("expected overlap prefix %q, got %q", firstTail, got)
	}
}

func TestRecursiveChunking_LargeOverlapWithinChunkSize(t *testing.T) {
	const (
		chunkSize = 120
		overlap   = 100
	)
	doc := &document.Document{
		Content: strings.Repeat("a", chunkSize-1) +
			" " +
			strings.Repeat("b", chunkSize-1),
	}
	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(chunkSize),
		WithRecursiveOverlap(overlap),
		WithRecursiveSeparators([]string{" ", ""}),
	)

	chunks, err := rc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 2)
	for i, chunk := range chunks {
		require.LessOrEqual(
			t,
			utf8.RuneCountInString(chunk.Content),
			chunkSize,
			"chunk %d exceeds the final size budget",
			i,
		)
	}
}

// TestRecursiveChunking_NoSeparators exercises the branch where the
// highest-priority separator is the empty string, triggering a character
// level split.
func TestRecursiveChunking_NoSeparators(t *testing.T) {
	doc := &document.Document{Content: strings.Repeat("x", 15)}

	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(5),
		WithRecursiveOverlap(0),
		WithRecursiveSeparators([]string{""}), // single empty separator
	)

	chunks, err := rc.Chunk(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Rune fallback fragments are merged back into full-size chunks.
	if got, want := len(chunks), 3; got != want {
		t.Fatalf("expected %d chunks, got %d", want, got)
	}

	// The first chunk must use the configured budget.
	if got := len(chunks[0].Content); got != 5 {
		t.Fatalf("expected first chunk size 5, got %d", got)
	}
}

func TestRecursiveChunking_BalancesUnbrokenTail(t *testing.T) {
	const chunkSize = 50
	content := strings.Repeat("x", chunkSize*2+1)
	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(chunkSize),
		WithRecursiveOverlap(0),
		WithRecursiveSeparators([]string{""}),
	)

	chunks, err := rc.Chunk(&document.Document{Content: content})

	require.NoError(t, err)
	require.Equal(t, []int{50, 26, 25}, []int{
		utf8.RuneCountInString(chunks[0].Content),
		utf8.RuneCountInString(chunks[1].Content),
		utf8.RuneCountInString(chunks[2].Content),
	})
	require.Equal(t, content,
		chunks[0].Content+chunks[1].Content+chunks[2].Content)
}

// TestRecursiveChunking_ForceSplit ensures the fallback branch that forcibly
// splits text at the chunkSize is executed when no separators remain.
func TestRecursiveChunking_ForceSplit(t *testing.T) {
	const chunkSize = 10
	text := strings.Repeat("1234567890", 3) // 30 characters

	doc := &document.Document{Content: text}

	// Use a separator that is NOT present in the text so that after the first
	// recursion there are no separators left.
	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(chunkSize),
		WithRecursiveOverlap(0),
		WithRecursiveSeparators([]string{","}),
	)

	chunks, err := rc.Chunk(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := len(chunks), 3; got != want {
		t.Fatalf("expected %d forced chunks, got %d", want, got)
	}

	for i, c := range chunks {
		if len(c.Content) > chunkSize {
			t.Fatalf("chunk %d exceeds size limit after force split", i)
		}
	}
}

// TestRecursiveChunking_CustomSep tests recursive chunking with custom separators.
func TestRecursiveChunking_CustomSep(t *testing.T) {
	text := strings.Repeat("A B C D E F ", 10) // 70 chars
	doc := &document.Document{ID: "txt", Content: text}

	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(25),
		WithRecursiveOverlap(3),
		WithRecursiveSeparators([]string{" ", ""}),
	)

	chunks, err := rc.Chunk(doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) <= 2 {
		t.Fatalf("expected more than 2 chunks, got %d", len(chunks))
	}

	// Each chunk <= 25 and overlap is at most 3. A natural word boundary may
	// shorten it instead of cutting through a word.
	for i, c := range chunks {
		if len(c.Content) > 25 {
			t.Fatalf("chunk %d exceeds size limit: %d > 25", i, len(c.Content))
		}
		if i > 0 {
			prev := chunks[i-1].Content
			actualOverlap := boundaryOverlap(prev, c.Content, 3)
			if actualOverlap <= 0 || actualOverlap > 3 {
				t.Fatalf("invalid overlap %d at chunk %d", actualOverlap, i)
			}
		}
	}
}

func TestRecursiveChunking_MergesSmallSeparatorFragments(t *testing.T) {
	doc := &document.Document{
		Content: "alpha beta gamma delta epsilon zeta eta theta",
	}
	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(24),
		WithRecursiveSeparators([]string{" ", ""}),
	)

	chunks, err := rc.Chunk(doc)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Equal(t, "alpha beta gamma delta ", chunks[0].Content)
	require.Equal(t, "epsilon zeta eta theta", chunks[1].Content)

	legacyChunks, err := NewRecursiveChunking(
		WithRecursiveChunkSize(24),
		WithRecursiveSeparators([]string{" ", ""}),
		WithRecursiveWhitespaceTrimming(),
	).Chunk(doc)
	require.NoError(t, err)
	require.Len(t, legacyChunks, 2)
	require.Equal(t, "alpha beta gamma delta", legacyChunks[0].Content)
	require.Equal(t, "epsilon zeta eta theta", legacyChunks[1].Content)
}

func TestRecursiveChunking_WhitespaceModes(t *testing.T) {
	content := "def f():  \n\tif enabled:\n\t\treturn 1  "
	doc := &document.Document{ID: "python", Content: content}

	chunks, err := NewRecursiveChunking(
		WithRecursiveChunkSize(128),
	).Chunk(doc)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, content, chunks[0].Content)

	legacyChunks, err := NewRecursiveChunking(
		WithRecursiveChunkSize(128),
		WithRecursiveWhitespaceTrimming(),
	).Chunk(doc)
	require.NoError(t, err)
	require.Len(t, legacyChunks, 1)
	require.Equal(
		t,
		"def f():\nif enabled:\nreturn 1",
		legacyChunks[0].Content,
	)
}

func TestRecursiveChunking_WhitespaceOnlyDocument(t *testing.T) {
	chunks, err := NewRecursiveChunking().Chunk(
		&document.Document{Content: " \n\t "},
	)
	require.ErrorIs(t, err, ErrEmptyDocument)
	require.Nil(t, chunks)
}

func TestRecursiveChunking_PreservesWhitespaceBoundaryFragments(t *testing.T) {
	const chunkSize = 4
	content := "   aaa   "
	doc := &document.Document{ID: "boundary-whitespace", Content: content}

	chunks, err := NewRecursiveChunking(
		WithRecursiveChunkSize(chunkSize),
		WithRecursiveSeparators([]string{" "}),
	).Chunk(doc)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Equal(t, "   a", chunks[0].Content)
	require.Equal(t, "aa  ", chunks[1].Content)
	for _, chunk := range chunks {
		require.NotEmpty(t, strings.TrimSpace(chunk.Content))
		require.LessOrEqual(t, utf8.RuneCountInString(chunk.Content), chunkSize)
	}

	legacyChunks, err := NewRecursiveChunking(
		WithRecursiveChunkSize(chunkSize),
		WithRecursiveSeparators([]string{" "}),
		WithRecursiveWhitespaceTrimming(),
	).Chunk(doc)
	require.NoError(t, err)
	require.Len(t, legacyChunks, 1)
	require.Equal(t, "aaa", legacyChunks[0].Content)
}

func TestRecursiveChunking_PreservesSentenceAtoms(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		chunkSize int
		atom      string
	}{
		{
			name:      "decimal",
			content:   "prefix 12.6 suffix text",
			chunkSize: 10,
			atom:      "12.6",
		},
		{
			name:      "dotted section",
			content:   "prefix 2.8.12 suffix text",
			chunkSize: 11,
			atom:      "2.8.12",
		},
		{
			name:      "semantic version",
			content:   "prefix v1.2.3 suffix text",
			chunkSize: 11,
			atom:      "v1.2.3",
		},
		{
			name:      "CJK punctuation cluster",
			content:   "12345678？！ tail",
			chunkSize: 9,
			atom:      "？！",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := NewRecursiveChunking(
				WithRecursiveChunkSize(tt.chunkSize),
			).Chunk(&document.Document{Content: tt.content})

			require.NoError(t, err)
			require.Greater(t, len(chunks), 1)
			require.Condition(t, func() bool {
				for _, chunk := range chunks {
					if strings.Contains(chunk.Content, tt.atom) {
						return true
					}
				}
				return false
			}, "expected %q to remain in one chunk", tt.atom)
			for _, chunk := range chunks {
				require.LessOrEqual(
					t,
					utf8.RuneCountInString(chunk.Content),
					tt.chunkSize,
				)
			}
		})
	}
}

// BenchmarkRecursiveChunking provides a quick performance smoke-test to avoid
// accidental O(N²) behaviour regressions. It is intentionally lightweight so
// as not to bloat CI runtime.
func BenchmarkRecursiveChunking(b *testing.B) {
	text := strings.Repeat("0123456789", 500) // 5 KB of data
	doc := &document.Document{Content: text}
	rc := NewRecursiveChunking(
		WithRecursiveChunkSize(256),
		WithRecursiveOverlap(64),
	)

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rc.Chunk(doc); err != nil {
			b.Fatalf("chunking failed: %v", err)
		}
		// Reset context to avoid retaining cancelled contexts between runs.
		_ = ctx
	}
}

func TestRecursiveChunking_ConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		chunkSize int
		overlap   int
		wantErr   error
	}{
		{
			name:      "zero chunk size",
			chunkSize: 0,
			overlap:   0,
			wantErr:   ErrInvalidChunkSize,
		},
		{
			name:      "negative chunk size",
			chunkSize: -1,
			overlap:   0,
			wantErr:   ErrInvalidChunkSize,
		},
		{
			name:      "negative overlap",
			chunkSize: 10,
			overlap:   -1,
			wantErr:   ErrInvalidOverlap,
		},
		{
			name:      "overlap greater than chunk size",
			chunkSize: 10,
			overlap:   15,
			wantErr:   ErrOverlapTooLarge,
		},
		{
			name:      "overlap equal to chunk size",
			chunkSize: 20,
			overlap:   20,
			wantErr:   ErrOverlapTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := NewRecursiveChunking(
				WithRecursiveChunkSize(tt.chunkSize),
				WithRecursiveOverlap(tt.overlap),
			)

			doc := &document.Document{ID: "test", Content: "Test content for recursive chunking validation with some text"}
			chunks, err := rc.Chunk(doc)
			require.ErrorIs(t, err, tt.wantErr)
			require.Nil(t, chunks)
		})
	}
}
