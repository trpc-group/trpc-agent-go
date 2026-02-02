//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package source

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

type mockChunkingStrategy struct{}

// ... existing code ...
type mockTransformer struct{}

func (m *mockTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func (m *mockTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func (m *mockTransformer) Name() string {
	return "MockTransformer"
}

func TestWithTransformers(t *testing.T) {
	t1 := &mockTransformer{}
	t2 := &mockTransformer{}

	config := &ReaderConfig{}
	opt := WithTransformers(t1, t2)
	opt(config)

	require.Len(t, config.transformers, 2)
}

func (m *mockChunkingStrategy) Chunk(doc *document.Document) ([]*document.Document, error) {
	return []*document.Document{doc}, nil
}

type mockOCRExtractor struct{}

func (m *mockOCRExtractor) ExtractText(ctx context.Context, imageData []byte, opts ...ocr.Option) (string, error) {
	return "mock-text", nil
}

func (m *mockOCRExtractor) ExtractTextFromReader(ctx context.Context, reader io.Reader, opts ...ocr.Option) (string, error) {
	return "mock-text", nil
}

func (m *mockOCRExtractor) Close() error {
	return nil
}

func TestGetFileType(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"data.txt", "text"},
		{"foo.pdf", "pdf"},
		{"note.md", "markdown"},
		{"info.json", "json"},
		{"sheet.csv", "csv"},
		{"doc.docx", "docx"},
		{"unknown.bin", "text"},
	}

	for _, c := range cases {
		got := GetFileType(c.path)
		require.Equal(t, c.want, got, "path %s", c.path)
	}
}

func TestGetFileTypeUnknownExtension(t *testing.T) {
	// Test files with unknown extensions that should fallback to text
	unknownExtCases := []struct {
		name string
		path string
		want string
	}{
		{"log_with_numeric_suffix", "app.log.123213", "text"},
		{"log_with_date_suffix", "app.log.20250121", "text"},
		{"multiple_dots", "app.service.log.1", "text"},
		{"unknown_extension", "data.xyz", "text"},
		{"no_extension", "Makefile", "text"},
		{"empty_extension", "file.", "text"},
		{"log_file", "app.log", "text"},
	}

	for _, c := range unknownExtCases {
		t.Run(c.name, func(t *testing.T) {
			got := GetFileType(c.path)
			require.Equal(t, c.want, got, "path %s should be treated as text", c.path)
		})
	}
}

func TestGetFileTypeAllExtensions(t *testing.T) {
	// Test all supported extensions via GetFileType function
	cases := []struct {
		ext  string
		want string
	}{
		{".txt", "text"},
		{".text", "text"},
		{".html", "text"},
		{".htm", "text"},
		{".pdf", "pdf"},
		{".md", "markdown"},
		{".markdown", "markdown"},
		{".json", "json"},
		{".csv", "csv"},
		{".docx", "docx"},
		{".doc", "docx"},
	}

	for _, c := range cases {
		t.Run(c.ext, func(t *testing.T) {
			got := GetFileType("file" + c.ext)
			require.Equal(t, c.want, got, "extension %s should map to %s", c.ext, c.want)
		})
	}
}

func TestGetFileTypeFromContentType(t *testing.T) {
	cases := []struct {
		contentType string
		fileName    string
		want        string
	}{
		// Content type based detection
		{"text/html; charset=utf-8", "", "text"},
		{"text/plain", "", "text"},
		{"text/plain; charset=utf-8", "", "text"},
		{"application/json", "", "json"},
		{"application/json; charset=utf-8", "", "json"},
		{"text/csv", "", "csv"},
		{"application/pdf", "", "pdf"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "", "docx"},

		// File extension based detection
		{"", "file.md", "markdown"},
		{"", "file.markdown", "markdown"},
		{"", "file.txt", "text"},
		{"", "file.text", "text"},
		{"", "file.html", "text"},
		{"", "file.htm", "text"},
		{"", "file.json", "json"},
		{"", "file.csv", "csv"},
		{"", "file.pdf", "pdf"},
		{"", "file.docx", "docx"},
		{"", "file.doc", "docx"},
		{"", "fallback.unknown", "text"},

		// Content type takes precedence over file extension
		{"application/json", "file.txt", "json"},
		{"text/csv", "file.json", "csv"},
	}

	for _, c := range cases {
		got := GetFileTypeFromContentType(c.contentType, c.fileName)
		require.Equal(t, c.want, got, "ctype %s fname %s", c.contentType, c.fileName)
	}
}

func TestGetFileTypeFromContentTypeUnknownExtension(t *testing.T) {
	// Test files with unknown extensions when content type is not available
	unknownExtCases := []struct {
		name        string
		contentType string
		fileName    string
		want        string
	}{
		{"log_with_numeric_suffix", "", "app.log.123213", "text"},
		{"log_with_date_suffix", "", "app.log.20250121", "text"},
		{"unknown_extension", "", "data.xyz", "text"},
		{"no_extension", "", "Makefile", "text"},
		{"unknown_content_type_fallback", "application/octet-stream", "file.bin", "text"},
	}

	for _, c := range unknownExtCases {
		t.Run(c.name, func(t *testing.T) {
			got := GetFileTypeFromContentType(c.contentType, c.fileName)
			require.Equal(t, c.want, got)
		})
	}
}

func TestGetReadersWithChunkConfig(t *testing.T) {
	readersDefault := GetReaders()
	readers := GetReadersWithChunkConfig(128, 16)

	// Ensure reader keys match.
	require.Equal(t, len(readersDefault), len(readers))

	// Verify that requesting zero config returns default map object count.
	readersZero := GetReadersWithChunkConfig(0, 0)
	require.Equal(t, len(readersDefault), len(readersZero))
}

func TestWithChunkSize(t *testing.T) {
	config := &ReaderConfig{}
	opt := WithChunkSize(100)
	opt(config)

	require.Equal(t, 100, config.chunkSize)
}

func TestWithChunkOverlap(t *testing.T) {
	config := &ReaderConfig{}
	opt := WithChunkOverlap(20)
	opt(config)

	require.Equal(t, 20, config.chunkOverlap)
}

func TestWithCustomChunkingStrategy(t *testing.T) {
	var _ chunking.Strategy = (*mockChunkingStrategy)(nil)

	strategy := &mockChunkingStrategy{}
	config := &ReaderConfig{}
	opt := WithCustomChunkingStrategy(strategy)
	opt(config)

	require.Equal(t, strategy, config.customChunkingStrategy)
}

func TestWithOCRExtractor(t *testing.T) {
	extractor := &mockOCRExtractor{}
	config := &ReaderConfig{}
	opt := WithOCRExtractor(extractor)
	opt(config)

	require.Equal(t, extractor, config.ocrExtractor)
}

func TestGetReaders_WithOptions(t *testing.T) {
	t.Run("with chunk size", func(t *testing.T) {
		readers := GetReaders(WithChunkSize(200))
		require.NotNil(t, readers)
	})

	t.Run("with chunk overlap", func(t *testing.T) {
		readers := GetReaders(WithChunkOverlap(50))
		require.NotNil(t, readers)
	})

	t.Run("with custom strategy", func(t *testing.T) {
		strategy := &mockChunkingStrategy{}
		readers := GetReaders(WithCustomChunkingStrategy(strategy))
		require.NotNil(t, readers)
	})

	t.Run("with OCR extractor", func(t *testing.T) {
		extractor := &mockOCRExtractor{}
		readers := GetReaders(WithOCRExtractor(extractor))
		require.NotNil(t, readers)
	})

	t.Run("with transformers", func(t *testing.T) {
		t1 := &mockTransformer{}
		readers := GetReaders(WithTransformers(t1))
		require.NotNil(t, readers)
	})

	t.Run("with multiple options", func(t *testing.T) {
		readers := GetReaders(
			WithChunkSize(300),
			WithChunkOverlap(30),
			WithCustomChunkingStrategy(&mockChunkingStrategy{}),
			WithOCRExtractor(&mockOCRExtractor{}),
			WithTransformers(&mockTransformer{}),
		)
		require.NotNil(t, readers)
	})
}

func TestBuildReaderOptions(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		config := &ReaderConfig{}
		opts := buildReaderOptions(config)
		require.Empty(t, opts)
	})

	t.Run("with chunk size", func(t *testing.T) {
		config := &ReaderConfig{chunkSize: 100}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})

	t.Run("with chunk overlap", func(t *testing.T) {
		config := &ReaderConfig{chunkOverlap: 20}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})

	t.Run("with custom strategy", func(t *testing.T) {
		config := &ReaderConfig{customChunkingStrategy: &mockChunkingStrategy{}}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})

	t.Run("with OCR extractor", func(t *testing.T) {
		config := &ReaderConfig{ocrExtractor: &mockOCRExtractor{}}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})

	t.Run("with transformers", func(t *testing.T) {
		config := &ReaderConfig{transformers: []transform.Transformer{&mockTransformer{}}}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})

	t.Run("with all options", func(t *testing.T) {
		config := &ReaderConfig{
			chunkSize:              100,
			chunkOverlap:           20,
			customChunkingStrategy: &mockChunkingStrategy{},
			ocrExtractor:           &mockOCRExtractor{},
			transformers:           []transform.Transformer{&mockTransformer{}},
		}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 5)
	})

	t.Run("zero chunk size not included", func(t *testing.T) {
		config := &ReaderConfig{chunkSize: 0, chunkOverlap: 20}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})

	t.Run("zero chunk overlap not included", func(t *testing.T) {
		config := &ReaderConfig{chunkSize: 100, chunkOverlap: 0}
		opts := buildReaderOptions(config)
		require.Len(t, opts, 1)
	})
}

func TestResolveFileType(t *testing.T) {
	tests := []struct {
		name         string
		overrideType string
		detectedType string
		expected     string
	}{
		{"override takes precedence", "json", "text", "json"},
		{"empty override uses detected", "", "markdown", "markdown"},
		{"both empty returns empty", "", "", ""},
		{"override with empty detected", "csv", "", "csv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveFileType(tt.overrideType, tt.detectedType)
			require.Equal(t, tt.expected, result)
		})
	}
}
