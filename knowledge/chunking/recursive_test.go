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

//go:build !race
// +build !race

package chunking

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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
		if c.Metadata["chunk"].(int) != i+1 {
			t.Fatalf("wrong chunk index, expected %d got %v", i+1, c.Metadata["chunk"])
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

	// Expect 15 one-character chunks because we split by character.
	if got, want := len(chunks), 15; got != want {
		t.Fatalf("expected %d chunks, got %d", want, got)
	}

	// The first chunk must be exactly 1 character.
	if got := len(chunks[0].Content); got != 1 {
		t.Fatalf("expected first chunk size 1, got %d", got)
	}
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
