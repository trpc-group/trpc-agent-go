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
	"errors"
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

func TestFixedSizeChunking_ConfigValidation(t *testing.T) {
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
			fsc := NewFixedSizeChunking(
				WithChunkSize(tt.chunkSize),
				WithOverlap(tt.overlap),
			)

			doc := &document.Document{ID: "test", Content: "This is a test content for chunking validation"}
			chunks, err := fsc.Chunk(doc)
			require.ErrorIs(t, err, tt.wantErr)
			require.Nil(t, chunks)
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
		if overlappedSize, ok := chunk.Metadata[source.MetaOverlappedContentSize]; ok {
			require.Equal(t, chunkRunes, overlappedSize)
		} else {
			require.Equal(t, chunkRunes, chunk.Metadata[source.MetaChunkSize])
		}
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

func TestFixedSizeChunking_LargeOverlapWithinChunkSize(t *testing.T) {
	const (
		chunkSize = 120
		overlap   = 100
	)
	doc := &document.Document{
		Content: strings.Repeat(
			"Natural overlap should preserve complete words and the final budget. ",
			12,
		),
	}
	fsc := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithOverlap(overlap),
	)

	chunks, err := fsc.Chunk(doc)
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

func TestFixedSizeChunking_DefaultWithoutOverlapBalancesTail(t *testing.T) {
	content := strings.Repeat("a", defaultChunkSize) + "b"
	doc := &document.Document{ID: "default-no-overlap", Content: content}
	fsc := NewFixedSizeChunking()

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Zero(t, fsc.overlap)
	require.Equal(t, []string{
		strings.Repeat("a", defaultChunkSize/2+1),
		strings.Repeat("a", defaultChunkSize/2-1) + "b",
	}, []string{
		chunks[0].Content,
		chunks[1].Content,
	})
}

func TestFixedSizeChunking_CustomSizeWithoutOverlap(t *testing.T) {
	doc := &document.Document{ID: "custom-size", Content: "abcdefghijklmnopqrstuvwxyz"}
	fsc := NewFixedSizeChunking(WithChunkSize(10))

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Zero(t, fsc.overlap)
	require.Equal(t, []string{"abcdefghij", "klmnopqrst", "uvwxyz"}, []string{
		chunks[0].Content,
		chunks[1].Content,
		chunks[2].Content,
	})
}

func TestFixedSizeChunking_PrefersNearbyWordBoundary(t *testing.T) {
	doc := &document.Document{
		ID:      "natural-boundary",
		Content: "alpha beta gamma delta",
	}
	fsc := NewFixedSizeChunking(WithChunkSize(12))

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha beta", "gamma delta"}, []string{
		chunks[0].Content,
		chunks[1].Content,
	})
}

func TestFixedSizeChunking_PrefersSentenceBoundary(t *testing.T) {
	content := "First sentence should remain complete here. " +
		"Second sentence continues with enough words to exceed the budget."
	fsc := NewFixedSizeChunking(WithChunkSize(60))

	chunks, err := fsc.Chunk(&document.Document{
		ID:      "sentence-boundary",
		Content: content,
	})

	require.NoError(t, err)
	require.Equal(t, "First sentence should remain complete here.",
		chunks[0].Content)
	contents := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		contents = append(contents, chunk.Content)
	}
	require.Equal(t, content, strings.Join(contents, " "))
}

func TestFixedSizeChunking_PreservesCompleteLines(t *testing.T) {
	const chunkSize = 120
	lines := []string{
		strings.Repeat("a", 75),
		strings.Repeat("b", 119),
		strings.Repeat("c", 50),
	}
	fsc := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithPreserveLines(),
	)

	chunks, err := fsc.Chunk(&document.Document{
		ID:      "complete-lines",
		Content: strings.Join(lines, "\n"),
	})

	require.NoError(t, err)
	require.Equal(t, lines, []string{
		chunks[0].Content,
		chunks[1].Content,
		chunks[2].Content,
	})
}

func TestFixedSizeChunking_PreserveLinesOverlapKeepsRecordBoundary(t *testing.T) {
	const (
		chunkSize = 60
		overlap   = 20
	)
	firstRecord := strings.Repeat("a", 50)
	secondRecord := "2 | " + strings.Repeat("b", 50)
	fsc := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithOverlap(overlap),
		WithPreserveLines(),
	)

	chunks, err := fsc.Chunk(&document.Document{
		ID:      "record-boundary",
		Content: firstRecord + "\n" + secondRecord,
	})

	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 3)
	require.Contains(t, chunks[1].Content,
		strings.Repeat("a", overlap-1)+"\n2 | ")
	require.NotContains(t, chunks[1].Content,
		strings.Repeat("a", overlap)+"2 | ")
	for i, chunk := range chunks {
		require.LessOrEqual(t,
			utf8.RuneCountInString(chunk.Content),
			chunkSize,
			"chunk %d exceeds the final budget",
			i,
		)
	}
}

func TestFixedSizeChunking_PreserveLinesOverlapDoesNotSplitOneRecord(t *testing.T) {
	fsc := NewFixedSizeChunking(
		WithChunkSize(60),
		WithOverlap(20),
		WithPreserveLines(),
	)

	chunks, err := fsc.Chunk(&document.Document{
		ID:      "record-continuation",
		Content: strings.Repeat("a", 100),
	})

	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)
	for _, chunk := range chunks {
		require.NotContains(t, chunk.Content, "\n")
	}
}

func TestFixedSizeChunking_BalancesOversizedLine(t *testing.T) {
	const chunkSize = 80
	content := strings.Repeat("x", 88)
	fsc := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithPreserveLines(),
	)

	chunks, err := fsc.Chunk(&document.Document{
		ID:      "oversized-line",
		Content: content,
	})

	require.NoError(t, err)
	require.Equal(t, []int{48, 40}, []int{
		utf8.RuneCountInString(chunks[0].Content),
		utf8.RuneCountInString(chunks[1].Content),
	})
	require.Equal(t, content, chunks[0].Content+chunks[1].Content)
}

func TestFixedSizeChunking_BalancesUnbrokenTail(t *testing.T) {
	const chunkSize = 120
	content := strings.Repeat("a", chunkSize*2+1)
	fsc := NewFixedSizeChunking(WithChunkSize(chunkSize))

	chunks, err := fsc.Chunk(&document.Document{
		ID:      "balanced-tail",
		Content: content,
	})
	require.NoError(t, err)
	require.Equal(t, []int{120, 61, 60}, []int{
		utf8.RuneCountInString(chunks[0].Content),
		utf8.RuneCountInString(chunks[1].Content),
		utf8.RuneCountInString(chunks[2].Content),
	})
	require.Equal(t, content,
		chunks[0].Content+chunks[1].Content+chunks[2].Content)
}

func TestFixedSizeChunking_OverlapStartsAtWordBoundary(t *testing.T) {
	doc := &document.Document{
		ID:      "natural-overlap",
		Content: "alpha beta gamma delta epsilon zeta",
	}
	const (
		chunkSize = 18
		overlap   = 8
	)
	fsc := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithOverlap(overlap),
	)

	chunks, err := fsc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)
	for i, chunk := range chunks {
		require.LessOrEqual(t, utf8.RuneCountInString(chunk.Content), chunkSize)
		if i == 0 {
			continue
		}
		actualOverlap := boundaryOverlap(chunks[i-1].Content, chunk.Content, overlap)
		require.Positive(t, actualOverlap)
		contentRunes := []rune(chunk.Content)
		require.True(t, actualOverlap == len(contentRunes) ||
			contentRunes[actualOverlap] == ' ')
	}
}

func TestFixedSizeChunking_CustomLengthFunc(t *testing.T) {
	lengthFunc := func(text string) (int, error) {
		return 2 * utf8.RuneCountInString(text), nil
	}
	const (
		chunkSize = 8
		overlap   = 2
	)
	chunker := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithOverlap(overlap),
		WithLengthFunc(lengthFunc),
	)

	chunks, err := chunker.Chunk(&document.Document{
		ID:      "custom-length",
		Content: "甲乙丙丁戊己庚辛",
	})

	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)
	for i, chunk := range chunks {
		size, err := lengthFunc(chunk.Content)
		require.NoError(t, err)
		require.LessOrEqual(t, size, chunkSize,
			"chunk %d exceeds the custom length budget", i)
		runeCount := utf8.RuneCountInString(chunk.Content)
		if overlappedSize, ok :=
			chunk.Metadata[source.MetaOverlappedContentSize]; ok {
			require.Equal(t, runeCount, overlappedSize)
		} else {
			require.Equal(t, runeCount,
				chunk.Metadata[source.MetaChunkSize])
		}
	}
}

func TestFixedSizeChunking_CustomLengthFuncMeasuresWholeCandidate(t *testing.T) {
	lengthFunc := func(text string) (int, error) {
		if text == "ab" {
			return 1, nil
		}
		return utf8.RuneCountInString(text), nil
	}
	chunker := NewFixedSizeChunking(
		WithChunkSize(1),
		WithLengthFunc(lengthFunc),
	)

	chunks, err := chunker.Chunk(&document.Document{
		ID:      "whole-candidate",
		Content: "abc",
	})

	require.NoError(t, err)
	require.Equal(t, []string{"ab", "c"}, []string{
		chunks[0].Content,
		chunks[1].Content,
	})
}

func TestFixedSizeChunking_CustomLengthFuncError(t *testing.T) {
	wantErr := errors.New("length failed")
	chunker := NewFixedSizeChunking(
		WithChunkSize(4),
		WithLengthFunc(func(string) (int, error) {
			return 0, wantErr
		}),
	)

	chunks, err := chunker.Chunk(&document.Document{
		ID:      "length-error",
		Content: "content",
	})

	require.ErrorIs(t, err, wantErr)
	require.Nil(t, chunks)
}

func TestFixedSizeChunking_CustomLengthPreservesRecordBoundary(t *testing.T) {
	lengthFunc := func(text string) (int, error) {
		return utf8.RuneCountInString(text), nil
	}
	const (
		chunkSize = 60
		overlap   = 20
	)
	firstRecord := strings.Repeat("a", 50)
	secondRecord := "2 | " + strings.Repeat("b", 50)
	chunker := NewFixedSizeChunking(
		WithChunkSize(chunkSize),
		WithOverlap(overlap),
		WithPreserveLines(),
		WithLengthFunc(lengthFunc),
	)

	chunks, err := chunker.Chunk(&document.Document{
		ID:      "custom-record-boundary",
		Content: firstRecord + "\n" + secondRecord,
	})

	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 3)
	require.Contains(t, chunks[1].Content,
		strings.Repeat("a", overlap-1)+"\n2 | ")
	require.NotContains(t, chunks[1].Content,
		strings.Repeat("a", overlap)+"2 | ")
	for i, chunk := range chunks {
		size, err := lengthFunc(chunk.Content)
		require.NoError(t, err)
		require.LessOrEqual(t, size, chunkSize,
			"chunk %d exceeds the custom length budget", i)
	}
}

func TestFixedSizeChunking_CustomRuneLengthMatchesDefault(t *testing.T) {
	content := "Alpha beta gamma delta epsilon zeta eta theta. " +
		"Second sentence keeps the boundary readable."
	doc := &document.Document{ID: "rune-parity", Content: content}
	defaultChunks, err := NewFixedSizeChunking(
		WithChunkSize(32),
		WithOverlap(6),
	).Chunk(doc)
	require.NoError(t, err)
	customChunks, err := NewFixedSizeChunking(
		WithChunkSize(32),
		WithOverlap(6),
		WithLengthFunc(func(text string) (int, error) {
			return utf8.RuneCountInString(text), nil
		}),
	).Chunk(doc)
	require.NoError(t, err)

	require.Equal(t, documentContents(defaultChunks),
		documentContents(customChunks))
}

func TestFixedSizeChunking_CustomLengthUsesBoundedInput(t *testing.T) {
	content := strings.Repeat("abcdefghij", 5000)
	sourceRunes := utf8.RuneCountInString(content)
	measuredRunes := 0
	lengthFunc := func(text string) (int, error) {
		length := utf8.RuneCountInString(text)
		measuredRunes += length
		return length, nil
	}
	chunker := NewFixedSizeChunking(
		WithChunkSize(64),
		WithLengthFunc(lengthFunc),
	)

	chunks, err := chunker.Chunk(&document.Document{
		ID:      "bounded-length-input",
		Content: content,
	})

	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)
	require.Less(t, measuredRunes, sourceRunes*100,
		"splitting should not repeatedly measure the full remaining text")
}
