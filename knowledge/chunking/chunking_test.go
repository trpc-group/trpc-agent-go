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
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// TestCleanText tests the cleanText function with various inputs
func TestCleanText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal_text",
			input:    "Hello World",
			expected: "Hello World",
		},
		{
			name:     "text_with_crlf",
			input:    "Line1\r\nLine2\r\nLine3",
			expected: "Line1\nLine2\nLine3",
		},
		{
			name:     "text_with_cr",
			input:    "Line1\rLine2\rLine3",
			expected: "Line1\nLine2\nLine3",
		},
		{
			name:     "text_with_leading_trailing_spaces",
			input:    "  Hello World  ",
			expected: "  Hello World  ",
		},
		{
			name:     "text_with_extra_spaces_in_lines",
			input:    "Line1  \n  Line2  \n  Line3  ",
			expected: "Line1  \n  Line2  \n  Line3  ",
		},
		{
			name:     "empty_string",
			input:    "",
			expected: "",
		},
		{
			name:     "unicode_text",
			input:    "中文测试\n日本語テスト\n한국어 테스트",
			expected: "中文测试\n日本語テスト\n한국어 테스트",
		},
		{
			name:     "text_with_mixed_newlines",
			input:    "Line1\n\n\nLine2\r\n\r\nLine3",
			expected: "Line1\n\n\nLine2\n\nLine3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanText(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCleanTextWithWhitespaceTrimming(t *testing.T) {
	input := "  def f():  \r\n\treturn 1\t\r\n"

	require.Equal(
		t,
		"def f():\nreturn 1",
		cleanTextWithWhitespaceTrimming(input, true),
	)
}

// TestCreateChunk tests the createChunk function
func TestCreateChunk(t *testing.T) {
	tests := []struct {
		name        string
		originalDoc *document.Document
		content     string
		chunkNumber int
		validate    func(*testing.T, *document.Document)
	}{
		{
			name: "with_doc_id",
			originalDoc: &document.Document{
				ID:       "doc123",
				Name:     "test.txt",
				Metadata: map[string]any{"key": "value"},
			},
			content:     "Chunk content",
			chunkNumber: 1,
			validate: func(t *testing.T, chunk *document.Document) {
				assert.Equal(t, "doc123_1", chunk.ID)
				assert.Equal(t, "test.txt", chunk.Name)
				assert.Equal(t, "Chunk content", chunk.Content)
				assert.Equal(t, 1, chunk.Metadata[source.MetaChunkIndex])
				assert.Equal(t, 13, chunk.Metadata[source.MetaChunkSize]) // "Chunk content" = 13 runes
				assert.Equal(t, "value", chunk.Metadata["key"])
				assert.False(t, chunk.CreatedAt.IsZero())
				assert.False(t, chunk.UpdatedAt.IsZero())
			},
		},
		{
			name: "without_id_with_name",
			originalDoc: &document.Document{
				Name:     "document.md",
				Metadata: map[string]any{},
			},
			content:     "Test",
			chunkNumber: 2,
			validate: func(t *testing.T, chunk *document.Document) {
				assert.Equal(t, "document.md_2", chunk.ID)
				assert.Equal(t, 2, chunk.Metadata[source.MetaChunkIndex])
			},
		},
		{
			name: "without_id_and_name",
			originalDoc: &document.Document{
				Metadata: map[string]any{},
			},
			content:     "Content",
			chunkNumber: 5,
			validate: func(t *testing.T, chunk *document.Document) {
				assert.Equal(t, "chunk_5", chunk.ID)
				assert.Equal(t, 5, chunk.Metadata[source.MetaChunkIndex])
			},
		},
		{
			name: "with_unicode_content",
			originalDoc: &document.Document{
				ID:       "unicode_doc",
				Metadata: map[string]any{},
			},
			content:     "中文内容测试",
			chunkNumber: 1,
			validate: func(t *testing.T, chunk *document.Document) {
				assert.Equal(t, 6, chunk.Metadata[source.MetaChunkSize]) // 6 Chinese characters
			},
		},
		{
			name: "preserve_existing_metadata",
			originalDoc: &document.Document{
				ID: "test",
				Metadata: map[string]any{
					"author":  "John",
					"version": 2,
					"tags":    []string{"tag1", "tag2"},
					"nested":  map[string]string{"key": "val"},
				},
			},
			content:     "Test",
			chunkNumber: 1,
			validate: func(t *testing.T, chunk *document.Document) {
				assert.Equal(t, "John", chunk.Metadata["author"])
				assert.Equal(t, 2, chunk.Metadata["version"])
				assert.Equal(t, []string{"tag1", "tag2"}, chunk.Metadata["tags"])
				assert.Equal(t, map[string]string{"key": "val"}, chunk.Metadata["nested"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := createChunk(tt.originalDoc, tt.content, tt.chunkNumber)
			require.NotNil(t, chunk)
			tt.validate(t, chunk)
		})
	}
}

func TestJoinWithOverlap(t *testing.T) {
	tests := []struct {
		name            string
		previous        string
		current         string
		maxOverlap      int
		maxSize         int
		separator       string
		wantContent     string
		wantOverlapSize int
	}{
		{
			name:            "full overlap",
			previous:        "abcdef",
			current:         "ghij",
			maxOverlap:      3,
			maxSize:         7,
			wantContent:     "defghij",
			wantOverlapSize: 3,
		},
		{
			name:            "overlap capped by budget",
			previous:        "abcdef",
			current:         "ghij",
			maxOverlap:      5,
			maxSize:         6,
			wantContent:     "efghij",
			wantOverlapSize: 2,
		},
		{
			name:            "separator included in budget",
			previous:        "ab c",
			current:         "wxyz",
			maxOverlap:      3,
			maxSize:         7,
			separator:       "\n\n",
			wantContent:     "c\n\nwxyz",
			wantOverlapSize: 1,
		},
		{
			name:        "no remaining budget",
			previous:    "abcdef",
			current:     "ghij",
			maxOverlap:  3,
			maxSize:     4,
			wantContent: "ghij",
		},
		{
			name:            "unicode runes",
			previous:        "甲乙丙丁",
			current:         "戊己",
			maxOverlap:      2,
			maxSize:         4,
			wantContent:     "丙丁戊己",
			wantOverlapSize: 2,
		},
		{
			name:            "prefer complete word",
			previous:        "alpha beta gamma",
			current:         "next",
			maxOverlap:      8,
			maxSize:         20,
			separator:       " ",
			wantContent:     "gamma next",
			wantOverlapSize: 5,
		},
		{
			name:            "prefer Chinese sentence boundary",
			previous:        "第一句。第二句很完整",
			current:         "后文",
			maxOverlap:      7,
			maxSize:         9,
			separator:       " ",
			wantContent:     "第二句很完整 后文",
			wantOverlapSize: 6,
		},
		{
			name:            "unbroken token does not add separator",
			previous:        "abcdef",
			current:         "ghij",
			maxOverlap:      3,
			maxSize:         7,
			separator:       " ",
			wantContent:     "defghij",
			wantOverlapSize: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, overlapSize := joinWithOverlap(
				tt.previous,
				tt.current,
				tt.maxOverlap,
				tt.maxSize,
				tt.separator,
			)
			assert.Equal(t, tt.wantContent, content)
			assert.Equal(t, tt.wantOverlapSize, overlapSize)
			assert.LessOrEqual(t, utf8.RuneCountInString(content), tt.maxSize)
		})
	}
}

func TestSplitTextAtNaturalBoundaryPreservesSentenceAtoms(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		maxSize       int
		wantPrefix    string
		wantRemaining string
	}{
		{
			name:          "decimal",
			content:       "prefix 12.6 suffix",
			maxSize:       10,
			wantPrefix:    "prefix",
			wantRemaining: " 12.6 suffix",
		},
		{
			name:          "dotted section",
			content:       "prefix 2.8.12 suffix",
			maxSize:       11,
			wantPrefix:    "prefix",
			wantRemaining: " 2.8.12 suffix",
		},
		{
			name:          "semantic version",
			content:       "prefix v1.2.3 suffix",
			maxSize:       11,
			wantPrefix:    "prefix",
			wantRemaining: " v1.2.3 suffix",
		},
		{
			name:          "CJK punctuation cluster",
			content:       "12345678？！ tail",
			maxSize:       9,
			wantPrefix:    "12345678",
			wantRemaining: "？！ tail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, remaining := splitTextAtNaturalBoundary(
				tt.content,
				tt.maxSize,
			)
			require.Equal(t, tt.wantPrefix, prefix)
			require.Equal(t, tt.wantRemaining, remaining)
		})
	}
}

func TestSplitTextAtNaturalBoundaryPreservesIndentation(t *testing.T) {
	prefix, remaining := splitTextAtNaturalBoundary(
		"header line\n\treturn value",
		12,
	)

	require.Equal(t, "header line\n", prefix)
	require.Equal(t, "\treturn value", remaining)
}

func TestNaturalTextSuffixPreservesIndentation(t *testing.T) {
	suffix, natural := naturalTextSuffix("header\n\treturn 1", 9)
	require.True(t, natural)
	require.Equal(t, "\treturn 1", suffix)

	legacySuffix, natural := naturalTextSuffixWithWhitespaceTrimming(
		"header\n\treturn 1",
		9,
		true,
	)
	require.True(t, natural)
	require.Equal(t, "return 1", legacySuffix)
}

func TestJoinWithOverlapPreservesIndentedSuffix(t *testing.T) {
	content, overlapSize := joinWithOverlapMode(
		"header\n\treturn 1",
		"next",
		9,
		14,
		"\n",
		false,
	)
	require.Equal(t, "\treturn 1\nnext", content)
	require.Equal(t, 9, overlapSize)

	legacyContent, legacyOverlapSize := joinWithOverlapMode(
		"header\n\treturn 1",
		"next",
		9,
		14,
		"\n",
		true,
	)
	require.Equal(t, "return 1\nnext", legacyContent)
	require.Equal(t, 8, legacyOverlapSize)
}

func TestSourceChunkSeparatorsWhitespaceModes(t *testing.T) {
	content := "before\n \t\nafter"
	chunks := []string{"before", "after"}

	separators := sourceChunkSeparators(content, chunks, "\n\n", false)
	require.Equal(t, []string{"", "\n \t\n"}, separators)

	legacySeparators := sourceChunkSeparators(
		content,
		chunks,
		"\n\n",
		true,
	)
	require.Equal(t, []string{"", "\n"}, legacySeparators)
}

// TestDefaultConstants tests the default constants
func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 1024, defaultChunkSize)
	assert.Equal(t, 0, defaultOverlap)
}

// TestErrors tests error constants
func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{
			name: "invalid_chunk_size",
			err:  ErrInvalidChunkSize,
			msg:  "chunk size must be greater than 0",
		},
		{
			name: "invalid_overlap",
			err:  ErrInvalidOverlap,
			msg:  "overlap must be non-negative",
		},
		{
			name: "overlap_too_large",
			err:  ErrOverlapTooLarge,
			msg:  "overlap must be less than chunk size",
		},
		{
			name: "empty_document",
			err:  ErrEmptyDocument,
			msg:  "document content is empty",
		},
		{
			name: "nil_document",
			err:  ErrNilDocument,
			msg:  "document cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.msg, tt.err.Error())
		})
	}
}

func boundaryOverlap(previous, current string, limit int) int {
	previousRunes := []rune(previous)
	currentRunes := []rune(current)
	limit = min(limit, min(len(previousRunes), len(currentRunes)))
	for size := limit; size > 0; size-- {
		if string(previousRunes[len(previousRunes)-size:]) ==
			string(currentRunes[:size]) {
			return size
		}
	}
	return 0
}
