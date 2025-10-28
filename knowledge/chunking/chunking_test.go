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
			expected: "Hello World",
		},
		{
			name:     "text_with_extra_spaces_in_lines",
			input:    "Line1  \n  Line2  \n  Line3  ",
			expected: "Line1\nLine2\nLine3",
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

// TestDefaultConstants tests the default constants
func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 1024, defaultChunkSize)
	assert.Equal(t, 128, defaultOverlap)
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
