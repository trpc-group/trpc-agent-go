//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package markdown

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
)

func TestMarkdownReader_Read_NoChunk(t *testing.T) {
	data := "# Title\n\nThis is **markdown**."

	rdr := New(
		reader.WithChunk(false),
	)

	docs, err := rdr.ReadFromReader("doc", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if !strings.Contains(docs[0].Content, "# Title") {
		t.Errorf("content mismatch")
	}
	if rdr.Name() != "MarkdownReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestMarkdownReader_FileAndURL(t *testing.T) {
	data := "## H2 Heading"

	tmp, _ := os.CreateTemp(t.TempDir(), "*.md")
	tmp.WriteString(data)
	tmp.Close()

	rdr := New()

	d1, _ := rdr.ReadFromFile(tmp.Name())
	if len(d1) != 1 {
		t.Fatalf("file read len %d", len(d1))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(data)) }))
	defer srv.Close()

	d2, _ := rdr.ReadFromURL(srv.URL + "/m.md")
	if d2[0].Name != "m" {
		t.Fatalf("expected name m got %s", d2[0].Name)
	}
}

type failChunk struct{}

func (failChunk) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("chunking fail")
}

func TestMarkdownReader_ChunkError(t *testing.T) {
	rdr := New(reader.WithCustomChunkingStrategy(failChunk{}))
	_, err := rdr.ReadFromReader("n", strings.NewReader("content"))
	if err == nil {
		t.Fatalf("expected error")
	}
}

// TestMarkdownReader_SupportedExtensions verifies the list of supported extensions.
func TestMarkdownReader_SupportedExtensions(t *testing.T) {
	rdr := New()
	exts := rdr.SupportedExtensions()

	if len(exts) == 0 {
		t.Fatal("expected non-empty supported extensions")
	}

	// Markdown reader should support .md and .markdown extensions
	expectedExts := map[string]bool{
		".md":       false,
		".markdown": false,
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

// TestMarkdownReader_ReadFromFileError verifies error handling for non-existent files.
func TestMarkdownReader_ReadFromFileError(t *testing.T) {
	rdr := New()
	_, err := rdr.ReadFromFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestMarkdownReader_ReadFromURLErrors verifies error handling for invalid URLs.
func TestMarkdownReader_ReadFromURLErrors(t *testing.T) {
	rdr := New()

	tests := []struct {
		name string
		url  string
	}{
		{"invalid_scheme_ftp", "ftp://example.com/file.md"},
		{"invalid_scheme_file", "file:///local/file.md"},
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

// TestMarkdownReader_ChunkDocumentDefaultStrategy verifies default chunking strategy initialization.
func TestMarkdownReader_ChunkDocumentDefaultStrategy(t *testing.T) {
	rdr := New(reader.WithChunk(true))

	docs, err := rdr.ReadFromReader("test", strings.NewReader("# Test\n\nContent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestMarkdownReader_ExtractFileNameFromURL tests URL filename extraction.
func TestMarkdownReader_ExtractFileNameFromURL(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"simple_filename", "https://example.com/README.md", "README"},
		{"with_query_params", "https://example.com/doc.md?v=1", "doc"},
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
