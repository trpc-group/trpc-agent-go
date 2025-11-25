//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package auto

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

type mockChunkingStrategy struct{}

func (m *mockChunkingStrategy) Chunk(doc *document.Document) ([]*document.Document, error) {
	return []*document.Document{doc}, nil
}

// TestReadDocuments verifies Auto Source handles plain text input with and
// without custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()
	input := strings.Repeat("0123456789", 5) // 50 chars

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{input})
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) == 0 {
			t.Fatalf("expected documents, got 0")
		}
	})

	t.Run("custom-chunk-config", func(t *testing.T) {
		const chunkSize = 10
		const overlap = 2
		src := New(
			[]string{input},
			WithChunkSize(chunkSize),
			WithChunkOverlap(overlap),
		)
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) <= 1 {
			t.Fatalf("expected multiple chunks, got %d", len(docs))
		}
		for _, d := range docs {
			if sz, ok := d.Metadata[source.MetaChunkSize].(int); ok && sz > chunkSize {
				t.Fatalf("chunk size %d exceeds expected max %d", sz, chunkSize)
			}
		}
	})
}

// TestProcessAsURLError verifies error handling in processAsURL.
func TestProcessAsURLError(t *testing.T) {
	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	src := New([]string{})
	_, err := src.processAsURL(ctx, ts.URL)
	if err == nil {
		t.Error("expected error from failed URL processing")
	}
}

// TestProcessAsDirectoryError verifies error handling in processAsDirectory.
func TestProcessAsDirectoryError(t *testing.T) {
	ctx := context.Background()

	src := New([]string{})
	_, err := src.processAsDirectory(ctx, "/nonexistent/directory")
	if err == nil {
		t.Error("expected error when processing nonexistent directory")
	}
}

// TestProcessAsFileError verifies error handling in processAsFile.
func TestProcessAsFileError(t *testing.T) {
	ctx := context.Background()

	src := New([]string{})
	_, err := src.processAsFile(ctx, "/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error when processing nonexistent file")
	}
}

// TestProcessAsTextError verifies error handling in processAsText.
func TestProcessAsTextError(t *testing.T) {
	src := New([]string{})

	docs, err := src.processAsText("valid text content")
	if err != nil {
		t.Errorf("processAsText should not error for valid content, got: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document from text processing")
	}
}

// TestProcessAsDirectoryWithCustomChunking verifies processAsDirectory with custom chunking.
func TestProcessAsDirectoryWithCustomChunking(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("test content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	strategy := &mockChunkingStrategy{}
	src := New([]string{}, WithCustomChunkingStrategy(strategy))

	docs, err := src.processAsDirectory(ctx, tmpDir)
	if err != nil {
		t.Errorf("processAsDirectory failed: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestProcessAsFileWithCustomChunking verifies processAsFile with custom chunking.
func TestProcessAsFileWithCustomChunking(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("test content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	strategy := &mockChunkingStrategy{}
	src := New([]string{}, WithCustomChunkingStrategy(strategy))

	docs, err := src.processAsFile(ctx, filePath)
	if err != nil {
		t.Errorf("processAsFile failed: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestWithCustomChunkingStrategy verifies the WithCustomChunkingStrategy option.
func TestWithCustomChunkingStrategy(t *testing.T) {
	strategy := &mockChunkingStrategy{}
	src := New([]string{"test input"}, WithCustomChunkingStrategy(strategy))

	if src.customChunkingStrategy != strategy {
		t.Error("WithCustomChunkingStrategy did not set custom chunking strategy")
	}
}

func TestHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	src := New([]string{})

	require.True(t, src.isDirectory(tmpDir))
	require.True(t, src.isFile(tmpFile))

	u := &url.URL{Scheme: "https", Host: "example.com"}
	require.True(t, src.isURL(u.String()))
	require.False(t, src.isURL("not a url"))
}

func TestSource_ProcessInputVariants(t *testing.T) {
	ctx := context.Background()

	// 1. Text input variant.
	src := New([]string{"hello world"})
	docs, err := src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("text input failed, err=%v docs=%d", err, len(docs))
	}

	// 2. File input variant.
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(tmpFile, []byte("file content"), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	src = New([]string{tmpFile})
	docs, err = src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("file input failed, err=%v docs=%d", err, len(docs))
	}

	// 3. Directory variant.
	src = New([]string{tmpDir})
	docs, err = src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("directory input failed, err=%v docs=%d", err, len(docs))
	}

	// 4. URL variant using httptest server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("u", 20)))
	}))
	defer ts.Close()

	src = New([]string{ts.URL})
	// Set small chunk size to force processing path.
	src.chunkSize = 10
	docs, err = src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("url input failed, err=%v docs=%d", err, len(docs))
	}
}

// TestWithMetadata verifies the WithMetadata option.
func TestWithMetadata(t *testing.T) {
	meta := map[string]any{
		"author":      "test-author",
		"version":     "1.0",
		"environment": "test",
	}

	src := New([]string{"test input"}, WithMetadata(meta))

	for k, expectedValue := range meta {
		if actualValue, ok := src.metadata[k]; !ok || actualValue != expectedValue {
			t.Fatalf("metadata[%s] not set correctly, expected %v, got %v", k, expectedValue, actualValue)
		}
	}
}

// TestWithMetadataValue verifies the WithMetadataValue option.
func TestWithMetadataValue(t *testing.T) {
	const metaKey = "test_key"
	const metaValue = "test_value"

	src := New([]string{"test input"}, WithMetadataValue(metaKey, metaValue))

	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("WithMetadataValue not applied correctly, expected %s, got %v", metaValue, v)
	}
}

// TestWithMetadataValueNilMetadata verifies WithMetadataValue initializes metadata map.
func TestWithMetadataValueNilMetadata(t *testing.T) {
	src := &Source{}
	opt := WithMetadataValue("key", "value")
	opt(src)

	if v, ok := src.metadata["key"]; !ok || v != "value" {
		t.Errorf("WithMetadataValue should initialize metadata map, got %v", src.metadata)
	}
}

// TestSetMetadata verifies the SetMetadata method.
func TestSetMetadata(t *testing.T) {
	src := New([]string{"test input"})

	const metaKey = "dynamic_key"
	const metaValue = "dynamic_value"

	src.SetMetadata(metaKey, metaValue)

	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("SetMetadata not applied correctly, expected %s, got %v", metaValue, v)
	}
}

// TestSetMetadataMultiple verifies setting multiple metadata values.
func TestSetMetadataMultiple(t *testing.T) {
	src := New([]string{"test input"})

	metadata := map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": 123,
		"key4": true,
	}

	for k, v := range metadata {
		src.SetMetadata(k, v)
	}

	for k, expectedValue := range metadata {
		if actualValue, ok := src.metadata[k]; !ok || actualValue != expectedValue {
			t.Fatalf("metadata[%s] not set correctly, expected %v, got %v", k, expectedValue, actualValue)
		}
	}
}

// TestNameAndType verifies Name() and Type() methods.
func TestNameAndType(t *testing.T) {
	tests := []struct {
		name         string
		opts         []Option
		expectedName string
	}{
		{
			name:         "default_name",
			opts:         nil,
			expectedName: "Auto Source",
		},
		{
			name:         "custom_name",
			opts:         []Option{WithName("Custom Auto")},
			expectedName: "Custom Auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := New([]string{"test"}, tt.opts...)

			if src.Name() != tt.expectedName {
				t.Errorf("Name() = %s, want %s", src.Name(), tt.expectedName)
			}

			if src.Type() != source.TypeAuto {
				t.Errorf("Type() = %s, want %s", src.Type(), source.TypeAuto)
			}
		})
	}
}

// TestGetMetadata verifies GetMetadata returns a copy of metadata.
func TestGetMetadata(t *testing.T) {
	meta := map[string]any{
		"key1": "value1",
		"key2": 123,
	}

	src := New([]string{"test"}, WithMetadata(meta))

	retrieved := src.GetMetadata()

	// Verify metadata values match
	for k, expectedValue := range meta {
		if actualValue, ok := retrieved[k]; !ok || actualValue != expectedValue {
			t.Errorf("GetMetadata()[%s] = %v, want %v", k, actualValue, expectedValue)
		}
	}

	// Verify modifying returned metadata doesn't affect original
	retrieved["new_key"] = "new_value"
	if _, ok := src.metadata["new_key"]; ok {
		t.Error("GetMetadata() should return a copy, not reference")
	}
}

// TestSetMetadataWithNilMap verifies SetMetadata works when metadata is nil.
func TestSetMetadataWithNilMap(t *testing.T) {
	src := &Source{}
	src.SetMetadata("key", "value")

	if v, ok := src.metadata["key"]; !ok || v != "value" {
		t.Errorf("SetMetadata with nil map failed, got %v", v)
	}
}

// TestReadDocumentsWithEmptyInputs verifies behavior with empty inputs.
func TestReadDocumentsWithEmptyInputs(t *testing.T) {
	ctx := context.Background()
	src := New([]string{})

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Errorf("ReadDocuments with empty inputs should not error, got %v", err)
	}
	if docs != nil {
		t.Errorf("ReadDocuments with empty inputs should return nil, got %v", docs)
	}
}

// TestProcessInputError verifies error handling in processInput.
func TestProcessInputError(t *testing.T) {
	ctx := context.Background()

	// Test with invalid URL that will fail processing
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	src := New([]string{ts.URL})
	_, err := src.ReadDocuments(ctx)
	if err == nil {
		t.Error("Expected error from failed URL processing")
	}
}

// TestInitializeReadersWithChunking verifies reader initialization with chunking options.
func TestInitializeReadersWithChunking(t *testing.T) {
	tests := []struct {
		name         string
		chunkSize    int
		chunkOverlap int
	}{
		{
			name:         "only_chunk_size",
			chunkSize:    100,
			chunkOverlap: 0,
		},
		{
			name:         "only_chunk_overlap",
			chunkSize:    0,
			chunkOverlap: 10,
		},
		{
			name:         "both_set",
			chunkSize:    100,
			chunkOverlap: 10,
		},
		{
			name:         "neither_set",
			chunkSize:    0,
			chunkOverlap: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{}
			if tt.chunkSize > 0 {
				opts = append(opts, WithChunkSize(tt.chunkSize))
			}
			if tt.chunkOverlap > 0 {
				opts = append(opts, WithChunkOverlap(tt.chunkOverlap))
			}

			src := New([]string{"test"}, opts...)

			if src.chunkSize != tt.chunkSize {
				t.Errorf("chunkSize = %d, want %d", src.chunkSize, tt.chunkSize)
			}
			if src.chunkOverlap != tt.chunkOverlap {
				t.Errorf("chunkOverlap = %d, want %d", src.chunkOverlap, tt.chunkOverlap)
			}
			if src.textReader == nil {
				t.Error("textReader should be initialized")
			}
		})
	}
}

// TestMetadataPropagationToSubSources verifies metadata is copied to sub-sources.
func TestMetadataPropagationToSubSources(t *testing.T) {
	ctx := context.Background()

	// Test metadata propagation through text processing
	src := New([]string{"test content"}, WithMetadataValue("custom_field", "custom_value"))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	if v, ok := docs[0].Metadata["custom_field"]; !ok || v != "custom_value" {
		t.Errorf("custom metadata not propagated to document, got %v", docs[0].Metadata)
	}
}

// TestProcessAsTextMetadata verifies processAsText sets correct metadata.
func TestProcessAsTextMetadata(t *testing.T) {
	src := New([]string{})
	src.metadata = map[string]any{"test_key": "test_value"}

	docs, err := src.processAsText("sample text")
	if err != nil {
		t.Fatalf("processAsText failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Check that metadata is properly set
	if v, ok := docs[0].Metadata["test_key"]; !ok || v != "test_value" {
		t.Errorf("custom metadata not set, got %v", docs[0].Metadata)
	}

	// Check that source metadata is set
	if v, ok := docs[0].Metadata[source.MetaSource]; !ok || v != source.TypeAuto {
		t.Errorf("source metadata not set correctly, got %v", docs[0].Metadata[source.MetaSource])
	}

	// Check that URI is set with text:// scheme
	if uri, ok := docs[0].Metadata[source.MetaURI].(string); !ok || !strings.HasPrefix(uri, "text://") {
		t.Errorf("URI not set correctly, got %v", uri)
	}
}

// mockOCRExtractor is a mock OCR extractor for testing.
type mockOCRExtractor struct{}

func (m *mockOCRExtractor) ExtractText(ctx context.Context, imageData []byte, opts ...ocr.Option) (string, error) {
	return "mocked ocr text", nil
}

func (m *mockOCRExtractor) ExtractTextFromReader(ctx context.Context, reader io.Reader, opts ...ocr.Option) (string, error) {
	return "mocked ocr text from reader", nil
}

func (m *mockOCRExtractor) Close() error {
	return nil
}

// TestWithOCRExtractor verifies the WithOCRExtractor option.
func TestWithOCRExtractor(t *testing.T) {
	var mockExtractor ocr.Extractor = &mockOCRExtractor{}
	src := New([]string{"test input"}, WithOCRExtractor(mockExtractor))

	if src.ocrExtractor == nil {
		t.Fatal("OCR extractor should be set")
	}
}

// TestOCRExtractorPropagation verifies OCR extractor is passed to sub-sources.
func TestOCRExtractorPropagation(t *testing.T) {
	ctx := context.Background()
	mockExtractor := &mockOCRExtractor{}

	// Create a temporary directory with a file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	// Test with directory input
	t.Run("directory_source", func(t *testing.T) {
		src := New([]string{tmpDir}, WithOCRExtractor(mockExtractor))
		if src.ocrExtractor == nil {
			t.Fatal("OCR extractor should be set")
		}

		// Process the directory - this should pass OCR extractor to dir source
		_, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
	})

	// Test with file input
	t.Run("file_source", func(t *testing.T) {
		src := New([]string{tmpFile}, WithOCRExtractor(mockExtractor))
		if src.ocrExtractor == nil {
			t.Fatal("OCR extractor should be set")
		}

		// Process the file - this should pass OCR extractor to file source
		_, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
	})
}
