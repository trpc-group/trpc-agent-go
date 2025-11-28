//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"

	// Import readers to register them
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/csv"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/docx"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/json"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
)

type mockChunkingStrategy struct {
	name string
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

// TestReadDocuments verifies that File Source correctly reads documents with
// and without custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")

	// Prepare sample content about 50 characters to ensure multiple chunks.
	content := strings.Repeat("0123456789", 5) // 50 chars
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{filePath})
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) == 0 {
			t.Fatalf("expected at least one document, got 0")
		}
	})

	t.Run("custom-chunk-config", func(t *testing.T) {
		const chunkSize = 10
		const overlap = 2
		src := New(
			[]string{filePath},
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
			sz, ok := d.Metadata[source.MetaChunkSize].(int)
			if !ok {
				t.Fatalf("chunk_size metadata missing or not int")
			}
			if sz > chunkSize {
				t.Fatalf("chunk size %d exceeds expected max %d", sz, chunkSize)
			}
		}
	})
}

// stubReader is a minimal reader.Reader implementation used for testing
// SetReader and metadata attachment flows.
// It always returns a single empty document.
// The implementation is intentionally simple to avoid external deps.
type stubReader struct{}

func (stubReader) ReadFromReader(name string, r io.Reader) ([]*document.Document, error) {
	return []*document.Document{{}}, nil
}

func (stubReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	return []*document.Document{{}}, nil
}

func (stubReader) ReadFromURL(url string) ([]*document.Document, error) {
	return []*document.Document{{}}, nil
}

func (stubReader) Name() string { return "stub" }

func (stubReader) SupportedExtensions() []string {
	return []string{".txt"}
}

// TestProcessFile_Directory ensures an error is returned when a directory path
// is passed to processFile.
func TestProcessFile_Directory(t *testing.T) {
	dir := t.TempDir()
	src := New([]string{})
	if _, err := src.processFile(dir); err == nil {
		t.Fatalf("expected error for directory path")
	}
}

// TestSetReaderAndMetadata verifies custom reader registration and metadata
// propagation.
func TestSetReaderAndMetadata(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	const fileName = "sample.txt"
	filePath := filepath.Join(tmpDir, fileName)
	if err := os.WriteFile(filePath, []byte("hello world"), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	src := New([]string{filePath})

	// Register stub reader for .txt files so we do not depend on real reader.
	src.SetReader("text", stubReader{})

	// Inject custom metadata.
	const metaKey = "custom"
	const metaVal = "value"
	src.SetMetadata(metaKey, metaVal)

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments returned error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if v, ok := docs[0].Metadata[metaKey]; !ok || v != metaVal {
		t.Fatalf("expected metadata %s=%s, got %v", metaKey, metaVal, v)
	}
}

// TestOptions verify functional options correctly modify Source fields.
func TestOptions(t *testing.T) {
	const (
		customName   = "my-source"
		chunkSize    = 8
		chunkOverlap = 2
		metaKey      = "k"
		metaValue    = "v"
	)

	src := New([]string{"dummy"},
		WithName(customName),
		WithChunkSize(chunkSize),
		WithChunkOverlap(chunkOverlap),
		WithMetadataValue(metaKey, metaValue),
	)

	if src.name != customName {
		t.Fatalf("expected name %s, got %s", customName, src.name)
	}
	if src.chunkSize != chunkSize {
		t.Fatalf("expected chunkSize %d, got %d", chunkSize, src.chunkSize)
	}
	if src.chunkOverlap != chunkOverlap {
		t.Fatalf("expected chunkOverlap %d, got %d", chunkOverlap, src.chunkOverlap)
	}
	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("metadata not set correctly")
	}
}
func TestProcessFile_Unsupported(t *testing.T) {
	src := New([]string{})
	_, err := src.processFile("nonexistent.xyz")
	if err == nil {
		t.Fatalf("expected error for unsupported file")
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
			expectedName: "File Source",
		},
		{
			name:         "custom_name",
			opts:         []Option{WithName("Custom File")},
			expectedName: "Custom File",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := New([]string{"dummy.txt"}, tt.opts...)

			if src.Name() != tt.expectedName {
				t.Errorf("Name() = %s, want %s", src.Name(), tt.expectedName)
			}

			if src.Type() != source.TypeFile {
				t.Errorf("Type() = %s, want %s", src.Type(), source.TypeFile)
			}
		})
	}
}

// TestGetMetadata verifies GetMetadata returns a copy of metadata.
func TestGetMetadata(t *testing.T) {
	meta := map[string]any{
		"key1": "value1",
		"key2": 456,
	}

	src := New([]string{"test.txt"}, WithMetadataValue("key1", "value1"), WithMetadataValue("key2", 456))

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

// TestWithMetadata verifies WithMetadata option.
func TestWithMetadata(t *testing.T) {
	meta := map[string]any{
		"author":  "test-author",
		"version": 1,
	}

	src := New([]string{"test.txt"}, WithMetadata(meta))

	for k, expectedValue := range meta {
		if actualValue, ok := src.metadata[k]; !ok || actualValue != expectedValue {
			t.Errorf("metadata[%s] = %v, want %v", k, actualValue, expectedValue)
		}
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

// TestReadDocumentsWithEmptyPaths verifies behavior with empty file paths.
func TestReadDocumentsWithEmptyPaths(t *testing.T) {
	ctx := context.Background()
	src := New([]string{})

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Errorf("ReadDocuments with empty paths should not error, got %v", err)
	}
	if docs != nil {
		t.Errorf("ReadDocuments with empty paths should return nil, got %v", docs)
	}
}

// TestProcessFileAbsolutePath verifies absolute path handling in metadata.
func TestProcessFileAbsolutePath(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("test content"), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	src := New([]string{filePath})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments returned error: %v", err)
	}

	if len(docs) == 0 {
		t.Fatalf("expected at least one document")
	}

	// Check that URI metadata contains file:// scheme
	if uri, ok := docs[0].Metadata[source.MetaURI].(string); !ok || !strings.HasPrefix(uri, "file://") {
		t.Errorf("expected URI to have file:// scheme, got %v", uri)
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

// TestReadDocumentsMultipleFiles verifies reading multiple files.
func TestReadDocumentsMultipleFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	files := []string{"file1.txt", "file2.txt"}
	for _, fname := range files {
		fpath := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(fpath, []byte("content"), 0600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
	}

	paths := []string{
		filepath.Join(tmpDir, "file1.txt"),
		filepath.Join(tmpDir, "file2.txt"),
	}

	src := New(paths)
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}

	if len(docs) < 2 {
		t.Errorf("expected at least 2 documents, got %d", len(docs))
	}
}

// TestWithCustomChunkingStrategy verifies the WithCustomChunkingStrategy option.
func TestWithCustomChunkingStrategy(t *testing.T) {
	strategy := &mockChunkingStrategy{name: "test-strategy"}
	src := New([]string{"dummy.txt"}, WithCustomChunkingStrategy(strategy))

	if src.customChunkingStrategy != strategy {
		t.Error("WithCustomChunkingStrategy did not set custom chunking strategy")
	}
}

// TestWithOCRExtractor verifies the WithOCRExtractor option.
func TestWithOCRExtractor(t *testing.T) {
	extractor := &mockOCRExtractor{}
	src := New([]string{"dummy.txt"}, WithOCRExtractor(extractor))

	if src.ocrExtractor == nil {
		t.Error("WithOCRExtractor did not set OCR extractor")
	}
}

// TestProcessFileNotRegular verifies error handling when path is not a regular file.
func TestProcessFileNotRegular(t *testing.T) {
	tmpDir := t.TempDir()
	src := New([]string{tmpDir})

	_, err := src.processFile(tmpDir)
	if err == nil {
		t.Error("expected error when processing directory as file")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("expected 'not a regular file' error, got: %v", err)
	}
}

// TestProcessFileAbsPathError verifies error handling when getting absolute path fails.
func TestProcessFileAbsPathError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{filePath})
	docs, err := src.processFile(filePath)
	if err != nil {
		t.Fatalf("processFile failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
}
