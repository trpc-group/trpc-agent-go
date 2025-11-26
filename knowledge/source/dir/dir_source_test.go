//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dir

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

// TestReadDocuments verifies Directory Source with and without
// custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	// Create two small files to ensure multiple documents are produced.
	for i := 0; i < 2; i++ {
		filePath := filepath.Join(tmpDir, "file"+strconv.Itoa(i)+".txt")
		content := strings.Repeat("0123456789", 5) // 50 chars
		if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}
	}

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{tmpDir}, WithRecursive(false))
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
			[]string{tmpDir},
			WithRecursive(false),
			WithChunkSize(chunkSize),
			WithChunkOverlap(overlap),
		)
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) == 0 {
			t.Fatalf("expected documents, got 0")
		}
		_ = docs // ensure docs produced with custom chunk config.
	})
}

// TestGetFilePaths verifies recursive and non-recursive traversal as well as
// extension filtering.
func TestGetFilePaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Directory structure:
	// tmpDir/
	//   file1.txt
	//   file2.md
	//   sub/
	//     nested.txt

	mustWrite := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("failed to write file %s: %v", path, err)
		}
	}

	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.md")
	subDir := filepath.Join(tmpDir, "sub")
	_ = os.Mkdir(subDir, 0755)
	nested := filepath.Join(subDir, "nested.txt")

	mustWrite(file1, "hello")
	mustWrite(file2, "world")
	mustWrite(nested, strings.Repeat("x", 10))

	// Non-recursive: should only see root files.
	srcNonRec := New([]string{tmpDir}, WithRecursive(false))
	paths, err := srcNonRec.getFilePaths(tmpDir)
	if err != nil {
		t.Fatalf("getFilePaths returned error: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}

	// Recursive: should include nested file.
	srcRec := New([]string{tmpDir}, WithRecursive(true))
	paths, err = srcRec.getFilePaths(tmpDir)
	if err != nil {
		t.Fatalf("getFilePaths returned error: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths with recursion, got %d", len(paths))
	}

	// Extension filter: only *.md.
	srcFilter := New([]string{tmpDir}, WithFileExtensions([]string{".md"}))
	paths, err = srcFilter.getFilePaths(tmpDir)
	if err != nil {
		t.Fatalf("getFilePaths returned error: %v", err)
	}
	if len(paths) != 1 || filepath.Ext(paths[0]) != ".md" {
		t.Fatalf("extension filter failed, paths: %v", paths)
	}
}

// TestReadDocuments_Basic ensures documents are returned without error.
func TestReadDocuments_Basic(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(filePath, []byte("sample content"), 0600); err != nil {
		t.Fatalf("failed to write sample file: %v", err)
	}

	src := New([]string{tmpDir})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments returned error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document")
	}

	if docs[0].Metadata == nil {
		t.Fatalf("expected metadata to be set")
	}
}

// TestNameAndMetadata verifies functional options related to name and metadata.
func TestNameAndMetadata(t *testing.T) {
	const customName = "my-dir-src"
	meta := map[string]any{"k": "v"}
	src := New([]string{"dummy"}, WithName(customName), WithMetadata(meta))

	if src.Name() != customName {
		t.Fatalf("expected name %s, got %s", customName, src.Name())
	}
	if src.Type() != source.TypeDir {
		t.Fatalf("unexpected Type value %s", src.Type())
	}

	if v, ok := src.metadata["k"]; !ok || v != "v" {
		t.Fatalf("metadata not applied correctly")
	}
}

func TestSource_FileExtensionFilter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// create files .txt and .json
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "b.json"), []byte("{}"), 0o600)

	src := New([]string{root}, WithFileExtensions([]string{".txt"}))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 txt doc, got %d", len(docs))
	}
}

func TestSource_Recursive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "c.txt"), []byte("y"), 0o600)

	src := New([]string{root}, WithRecursive(true))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("recursive read failed")
	}
}

// TestWithMetadataValue verifies the WithMetadataValue option.
func TestWithMetadataValue(t *testing.T) {
	const metaKey = "test_key"
	const metaValue = "test_value"

	src := New([]string{"dummy"}, WithMetadataValue(metaKey, metaValue))

	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("WithMetadataValue not applied correctly, expected %s, got %v", metaValue, v)
	}
}

// TestSetMetadata verifies the SetMetadata method.
func TestSetMetadata(t *testing.T) {
	src := New([]string{"dummy"})

	const metaKey = "dynamic_key"
	const metaValue = "dynamic_value"

	src.SetMetadata(metaKey, metaValue)

	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("SetMetadata not applied correctly, expected %s, got %v", metaValue, v)
	}
}

// TestSetMetadataMultiple verifies setting multiple metadata values.
func TestSetMetadataMultiple(t *testing.T) {
	src := New([]string{"dummy"})

	metadata := map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": 123,
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

// TestGetMetadata verifies GetMetadata returns a copy of metadata.
func TestGetMetadata(t *testing.T) {
	meta := map[string]any{
		"key1": "value1",
		"key2": 789,
	}

	src := New([]string{"dummy"}, WithMetadata(meta))

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

// TestReadDocumentsWithEmptyDirPath verifies behavior with empty directory path.
func TestReadDocumentsWithEmptyDirPath(t *testing.T) {
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

// TestReadDocumentsWithEmptyStringInPaths verifies behavior with empty string in paths.
func TestReadDocumentsWithEmptyStringInPaths(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Mix empty string with valid path
	src := New([]string{"", tmpDir})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Errorf("ReadDocuments should skip empty strings, got error: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected documents from valid path")
	}
}

// TestReadDocumentsWithNonexistentDir verifies error handling for nonexistent directory.
func TestReadDocumentsWithNonexistentDir(t *testing.T) {
	ctx := context.Background()
	src := New([]string{"/nonexistent/directory/path"})

	_, err := src.ReadDocuments(ctx)
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

// TestReadDocumentsEmptyDirectory verifies behavior with empty directory.
func TestReadDocumentsEmptyDirectory(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	src := New([]string{tmpDir})
	_, err := src.ReadDocuments(ctx)
	if err == nil {
		t.Error("expected error for empty directory")
	}
}

// TestProcessFileError verifies error handling in processFile.
func TestProcessFileError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.unsupported")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir})
	// This should skip the unsupported file type and continue
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Logf("got expected error for unsupported file type: %v", err)
	}
	// Either error or empty docs is acceptable for unsupported files
	_ = docs
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

// TestReadDocumentsMultipleDirs verifies reading from multiple directories.
func TestReadDocumentsMultipleDirs(t *testing.T) {
	ctx := context.Background()
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	// Create files in both directories
	if err := os.WriteFile(filepath.Join(tmpDir1, "file1.txt"), []byte("content1"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir2, "file2.txt"), []byte("content2"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir1, tmpDir2})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}

	if len(docs) < 2 {
		t.Errorf("expected at least 2 documents from both dirs, got %d", len(docs))
	}
}

// TestReadDocumentsAbsolutePathInMetadata verifies absolute path is set in metadata.
func TestReadDocumentsAbsolutePathInMetadata(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Check URI has file:// scheme
	if uri, ok := docs[0].Metadata[source.MetaURI].(string); !ok || !strings.HasPrefix(uri, "file://") {
		t.Errorf("expected file:// URI, got %v", uri)
	}
}

// TestWithCustomChunkingStrategy verifies the WithCustomChunkingStrategy option.
func TestWithCustomChunkingStrategy(t *testing.T) {
	strategy := &mockChunkingStrategy{name: "test-strategy"}
	src := New([]string{"dummy"}, WithCustomChunkingStrategy(strategy))

	if src.customChunkingStrategy != strategy {
		t.Error("WithCustomChunkingStrategy did not set custom chunking strategy")
	}
}

// TestWithOCRExtractor verifies the WithOCRExtractor option.
func TestWithOCRExtractor(t *testing.T) {
	extractor := &mockOCRExtractor{}
	src := New([]string{"dummy"}, WithOCRExtractor(extractor))

	if src.ocrExtractor == nil {
		t.Error("WithOCRExtractor did not set OCR extractor")
	}
}

// TestProcessFileNotRegular verifies error handling when path is not a regular file.
func TestProcessFileNotRegular(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	os.Mkdir(subDir, 0755)

	src := New([]string{tmpDir})
	_, err := src.processFile(subDir)
	if err == nil {
		t.Error("expected error when processing directory as file")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("expected 'not a regular file' error, got: %v", err)
	}
}
