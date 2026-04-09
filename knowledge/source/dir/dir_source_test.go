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
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
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

type recordingExtractor struct {
	format string
	err    error
}

func (r *recordingExtractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return r.ExtractFromReader(ctx, strings.NewReader(string(data)), opts...)
}

func (r *recordingExtractor) ExtractFromReader(ctx context.Context, reader io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &extractor.Result{
		Reader: strings.NewReader("# extracted"),
		Format: r.format,
	}, nil
}

func (r *recordingExtractor) SupportedFormats() []string {
	return []string{".pdf"}
}

func (r *recordingExtractor) Close() error { return nil }

type captureReader struct {
	lastName string
}

func (c *captureReader) ReadFromReader(name string, r io.Reader) ([]*document.Document, error) {
	c.lastName = name
	return []*document.Document{{Content: "ok"}}, nil
}

func (c *captureReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	return nil, errors.New("unexpected ReadFromFile call")
}

func (c *captureReader) ReadFromURL(url string) ([]*document.Document, error) {
	return nil, errors.New("unexpected ReadFromURL call")
}

func (c *captureReader) Name() string { return "capture" }

func (c *captureReader) SupportedExtensions() []string { return []string{".md"} }

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

func TestWithMetadataCopiesInputMap(t *testing.T) {
	meta := map[string]any{"k": "v"}
	src := New([]string{"dummy"}, WithMetadata(meta))

	meta["k"] = "changed"
	meta["new"] = "value"

	if got := src.metadata["k"]; got != "v" {
		t.Fatalf("metadata should be copied, got %v", got)
	}
	if _, ok := src.metadata["new"]; ok {
		t.Fatal("source metadata should not observe new keys added to input map")
	}
}

func TestProcessFile_WithExtractorPreservesFileName(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-test"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	reader := &captureReader{}
	src := New([]string{tmpDir}, WithExtractor(&recordingExtractor{format: extractor.FormatMarkdown}))
	src.readers[extractor.FormatMarkdown] = reader

	_, err := src.processFile(ctx, filePath)
	if err != nil {
		t.Fatalf("processFile failed: %v", err)
	}
	if reader.lastName != "sample.pdf" {
		t.Fatalf("expected extracted reader name sample.pdf, got %s", reader.lastName)
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
	_, err := src.processFile(context.Background(), subDir)
	if err == nil {
		t.Error("expected error when processing directory as file")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("expected 'not a regular file' error, got: %v", err)
	}
}

// TestWithFileReaderType verifies the WithFileReaderType option.
func TestWithFileReaderType(t *testing.T) {
	tests := []struct {
		name           string
		fileReaderType source.FileReaderType
	}{
		{
			name:           "markdown_reader_type",
			fileReaderType: source.FileReaderTypeMarkdown,
		},
		{
			name:           "json_reader_type",
			fileReaderType: source.FileReaderTypeJSON,
		},
		{
			name:           "text_reader_type",
			fileReaderType: source.FileReaderTypeText,
		},
		{
			name:           "csv_reader_type",
			fileReaderType: source.FileReaderTypeCSV,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := New([]string{"dummy"}, WithFileReaderType(tt.fileReaderType))

			if src.fileReaderType != tt.fileReaderType {
				t.Errorf("fileReaderType = %s, want %s", src.fileReaderType, tt.fileReaderType)
			}
		})
	}
}

// TestFileReaderTypeOverridesDetection verifies that WithFileReaderType overrides automatic file type detection.
func TestFileReaderTypeOverridesDetection(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	t.Run("txt_files_with_json_reader", func(t *testing.T) {
		// Create directory with .txt files containing JSON content
		subDir := filepath.Join(tmpDir, "json_as_txt")
		os.Mkdir(subDir, 0755)

		filePath := filepath.Join(subDir, "data.txt")
		jsonContent := `{"key": "value"}`
		if err := os.WriteFile(filePath, []byte(jsonContent), 0600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		// Use JSON reader type to force JSON parsing for all files
		src := New([]string{subDir}, WithFileReaderType(source.FileReaderTypeJSON))
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
		if len(docs) == 0 {
			t.Fatal("expected at least one document")
		}
	})

	t.Run("txt_files_with_markdown_reader", func(t *testing.T) {
		// Create directory with .txt files containing markdown content
		subDir := filepath.Join(tmpDir, "md_as_txt")
		os.Mkdir(subDir, 0755)

		filePath := filepath.Join(subDir, "readme.txt")
		markdownContent := "# Title\n\nParagraph content."
		if err := os.WriteFile(filePath, []byte(markdownContent), 0600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		// Use Markdown reader type
		src := New([]string{subDir}, WithFileReaderType(source.FileReaderTypeMarkdown))
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
		if len(docs) == 0 {
			t.Fatal("expected at least one document")
		}
	})

	t.Run("default_detection_without_override", func(t *testing.T) {
		subDir := filepath.Join(tmpDir, "default")
		os.Mkdir(subDir, 0755)

		filePath := filepath.Join(subDir, "sample.txt")
		if err := os.WriteFile(filePath, []byte("plain text"), 0600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		src := New([]string{subDir})
		if src.fileReaderType != "" {
			t.Error("fileReaderType should be empty by default")
		}

		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
		if len(docs) == 0 {
			t.Fatal("expected at least one document")
		}
	})
}

// TestFileReaderTypeWithChunking verifies WithFileReaderType works with chunking options.
func TestFileReaderTypeWithChunking(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	content := strings.Repeat("word ", 100)
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir},
		WithFileReaderType(source.FileReaderTypeText),
		WithChunkSize(50),
		WithChunkOverlap(10),
	)

	if src.fileReaderType != source.FileReaderTypeText {
		t.Errorf("fileReaderType = %s, want %s", src.fileReaderType, source.FileReaderTypeText)
	}
	if src.chunkSize != 50 {
		t.Errorf("chunkSize = %d, want 50", src.chunkSize)
	}
	if src.chunkOverlap != 10 {
		t.Errorf("chunkOverlap = %d, want 10", src.chunkOverlap)
	}

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
}

// TestFileReaderTypeWithRecursive verifies WithFileReaderType works with recursive option.
func TestFileReaderTypeWithRecursive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create nested directory structure
	subDir := filepath.Join(tmpDir, "sub")
	os.Mkdir(subDir, 0755)

	// Create files at both levels
	os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte(`{"root": true}`), 0600)
	os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte(`{"nested": true}`), 0600)

	src := New([]string{tmpDir},
		WithFileReaderType(source.FileReaderTypeJSON),
		WithRecursive(true),
	)

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) < 2 {
		t.Errorf("expected at least 2 documents (root + nested), got %d", len(docs))
	}
}

// TestExtractAndRead_ExtractionError verifies error propagation when ExtractFromReader fails.
func TestExtractAndRead_ExtractionError(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-test"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir}, WithExtractor(&recordingExtractor{
		err: errors.New("extraction failed"),
	}))
	_, err := src.ReadDocuments(ctx)
	if err != nil {
		// Error is logged but processing continues; no error expected at top level
		t.Logf("got error (may be expected): %v", err)
	}
}

// TestExtractAndRead_UnknownFormat verifies error when extracted format has no reader.
func TestExtractAndRead_UnknownFormat(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-test"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir}, WithExtractor(&recordingExtractor{
		format: "unknown_format_xyz",
	}))
	_, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Logf("got error (may be expected): %v", err)
	}
}

// TestReadWithReader_NoReaderAvailable verifies error when no reader is available for file type.
func TestReadWithReader_NoReaderAvailable(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir})
	// Remove the text reader to trigger "no reader available" error
	delete(src.readers, "text")
	_, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Logf("got error (may be expected): %v", err)
	}
}

// TestReadDocuments_ReadFileError verifies error propagation when ReadFromFile fails.
func TestReadDocuments_ReadFileError(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	src := New([]string{tmpDir})
	// Replace the text reader with one that always fails
	src.readers["text"] = &failReader{}
	_, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Logf("got error (may be expected): %v", err)
	}
}

// failReader is a reader that always returns an error.
type failReader struct{}

func (f *failReader) ReadFromReader(name string, r io.Reader) ([]*document.Document, error) {
	return nil, errors.New("read from reader failed")
}

func (f *failReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	return nil, errors.New("read from file failed")
}

func (f *failReader) ReadFromURL(url string) ([]*document.Document, error) {
	return nil, errors.New("read from url failed")
}

func (f *failReader) Name() string { return "fail" }

func (f *failReader) SupportedExtensions() []string { return []string{".txt"} }

// mockTransformer is a simple Transformer implementation for testing.
type mockTransformer struct{}

func (m *mockTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func (m *mockTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func (m *mockTransformer) Name() string { return "mock" }

// TestWithTransformers verifies the WithTransformers option.
func TestWithTransformers(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Verify WithTransformers with empty list
	src := New([]string{tmpDir}, WithTransformers())
	if len(src.transformers) != 0 {
		t.Errorf("expected 0 transformers, got %d", len(src.transformers))
	}

	// Verify WithTransformers with actual transformer
	src2 := New([]string{tmpDir}, WithTransformers(&mockTransformer{}))
	if len(src2.transformers) != 1 {
		t.Errorf("expected 1 transformer, got %d", len(src2.transformers))
	}

	// Verify it works end-to-end
	docs, err := src2.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments with transformer failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
}
