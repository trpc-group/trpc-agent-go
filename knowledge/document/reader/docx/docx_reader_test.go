//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package docx

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	godocx "github.com/gomutex/godocx"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
)

// createDocx creates a simple DOCX file with given text and returns its bytes.
func createDocx(t *testing.T, text string) []byte {
	t.Helper()

	doc, err := godocx.NewDocument()
	require.NoError(t, err)

	doc.AddParagraph(text)

	var buf bytes.Buffer
	_, err = doc.WriteTo(&buf)
	require.NoError(t, err)

	return buf.Bytes()
}

func TestReader_ReadFromReader_NoChunk(t *testing.T) {
	data := createDocx(t, "Hello Docx")
	rdr := New(reader.WithChunk(false))

	docs, err := rdr.ReadFromReader("example", bytes.NewReader(data))
	require.NoError(t, err)
	require.NotEmpty(t, docs)
	require.Contains(t, docs[0].Content, "Hello Docx")
}

type errChunker struct{}

func (errChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("chunk fail")
}

func TestReader_ReadFile_ChunkError(t *testing.T) {
	data := createDocx(t, "File Mode")
	tmp, _ := os.CreateTemp(t.TempDir(), "*.docx")
	tmp.Write(data)
	tmp.Close()

	// No chunking.
	rdr := New(reader.WithChunk(false))
	docs, err := rdr.ReadFromFile(tmp.Name())
	require.NoError(t, err)
	require.Len(t, docs, 1)

	// Trigger chunk error path.
	rdrErr := New(reader.WithCustomChunkingStrategy(errChunker{}))
	_, err = rdrErr.ReadFromFile(tmp.Name())
	if err == nil {
		t.Fatalf("expected chunk error")
	}
}

func TestReader_ReadFromURL(t *testing.T) {
	data := createDocx(t, "URL Mode")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Write(data)
	}))
	defer srv.Close()

	rdr := New(reader.WithChunk(false))
	docs, err := rdr.ReadFromURL(srv.URL + "/my.docx")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "my", docs[0].Name)
}

// TestDOCXReader_SupportedExtensions verifies the list of supported extensions.
func TestDOCXReader_SupportedExtensions(t *testing.T) {
	rdr := New()
	exts := rdr.SupportedExtensions()

	require.NotEmpty(t, exts, "expected non-empty supported extensions")

	// DOCX reader should support .docx and .doc extensions
	expectedExts := map[string]bool{
		".docx": false,
		".doc":  false,
	}

	for _, ext := range exts {
		if _, ok := expectedExts[ext]; ok {
			expectedExts[ext] = true
		}
	}

	for ext, found := range expectedExts {
		if !found {
			t.Errorf("expected extension %q in supported extensions", ext)
		}
	}
}

// TestDOCXReader_ReadFromFileError verifies error handling for non-existent files.
func TestDOCXReader_ReadFromFileError(t *testing.T) {
	rdr := New()
	_, err := rdr.ReadFromFile("/nonexistent/path/file.docx")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestDOCXReader_ReadFromURLErrors verifies error handling for invalid URLs.
func TestDOCXReader_ReadFromURLErrors(t *testing.T) {
	rdr := New()

	tests := []struct {
		name string
		url  string
	}{
		{"invalid_scheme_ftp", "ftp://example.com/document.docx"},
		{"invalid_scheme_file", "file:///local/document.docx"},
		{"malformed_url", "://invalid-url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rdr.ReadFromURL(tt.url)
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

// TestDOCXReader_ChunkDocumentDefaultStrategy verifies default chunking strategy initialization.
func TestDOCXReader_ChunkDocumentDefaultStrategy(t *testing.T) {
	rdr := New(reader.WithChunk(true))

	// Create a temporary DOCX file
	tmpFile := filepath.Join(t.TempDir(), "test.docx")
	docxBytes := createDocx(t, "Test content for chunking")
	if err := os.WriteFile(tmpFile, docxBytes, 0644); err != nil {
		t.Fatalf("failed to write DOCX file: %v", err)
	}

	docs, err := rdr.ReadFromFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestDOCXReader_ExtractFileNameFromURL tests URL filename extraction.
func TestDOCXReader_ExtractFileNameFromURL(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"simple_filename", "https://example.com/document.docx", "document"},
		{"with_query_params", "https://example.com/report.docx?v=1", "report"},
		{"root_path", "https://example.com/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rdr.extractFileNameFromURL(tt.url)
			if result != tt.expected {
				t.Errorf("extractFileNameFromURL(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// TestDOCXReader_ReadFromReaderErrors tests error handling for invalid data.
func TestDOCXReader_ReadFromReaderErrors(t *testing.T) {
	rdr := New()

	tests := []struct {
		name string
		data []byte
	}{
		{"invalid_data", []byte("not a valid docx file")},
		{"empty_content", createDocx(t, "")},
		{"whitespace_only", createDocx(t, "   \n\t  ")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rdr.ReadFromReader(tt.name, bytes.NewReader(tt.data))
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

// TestDOCXReader_ExtractTextFromDocWithMultipleParagraphs tests extracting text from complex documents.
func TestDOCXReader_ExtractTextFromDocWithMultipleParagraphs(t *testing.T) {
	rdr := New()

	// Create a DOCX with multiple paragraphs
	tmpFile := filepath.Join(t.TempDir(), "multi.docx")
	docxBytes := createDocxWithMultipleParagraphs(t, []string{
		"First paragraph",
		"Second paragraph",
		"Third paragraph",
	})
	if err := os.WriteFile(tmpFile, docxBytes, 0644); err != nil {
		t.Fatalf("failed to write DOCX file: %v", err)
	}

	docs, err := rdr.ReadFromFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	require.Len(t, docs, 1)

	// Check that content contains all paragraphs
	content := docs[0].Content
	if !strings.Contains(content, "First paragraph") ||
		!strings.Contains(content, "Second paragraph") ||
		!strings.Contains(content, "Third paragraph") {
		t.Errorf("expected content to contain all paragraphs, got: %s", content)
	}
}

// TestDOCXReader_Name tests the Name method.
func TestDOCXReader_Name(t *testing.T) {
	reader := &Reader{}
	name := reader.Name()
	if name != "DOCXReader" {
		t.Fatalf("expected 'DOCXReader', got %s", name)
	}
}

// createDocxWithMultipleParagraphs creates a DOCX with multiple paragraphs for testing.
func createDocxWithMultipleParagraphs(t *testing.T, paragraphs []string) []byte {
	t.Helper()

	doc, err := godocx.NewDocument()
	require.NoError(t, err)

	for _, text := range paragraphs {
		doc.AddParagraph(text)
	}

	var buf bytes.Buffer
	_, err = doc.WriteTo(&buf)
	require.NoError(t, err)

	return buf.Bytes()
}
