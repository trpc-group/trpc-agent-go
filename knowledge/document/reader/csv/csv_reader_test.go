//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package csv

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
)

func TestCSVReader_Read_NoChunk(t *testing.T) {
	data := "name,age\nAlice,30\nBob,25\n"

	rdr := New(
		reader.WithChunk(false),
	)

	docs, err := rdr.ReadFromReader("people", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	// csvToText should replace commas with " | ".
	if !strings.Contains(docs[0].Content, "Alice | 30") {
		t.Errorf("converted content mismatch: %s", docs[0].Content)
	}
	if rdr.Name() != "CSVReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestCSVReader_ChunkingAndHelpers(t *testing.T) {
	data := "a,b,c\n1,2,3\n4,5,6\n"
	rdr := New().(*Reader) // default chunking enabled

	docs, err := rdr.ReadFromReader("sheet", strings.NewReader(data))
	if err != nil {
		t.Fatalf("chunking read error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one chunked document")
	}

	// Test helper methods.
	txt := rdr.csvToText("foo,bar")
	if txt != "foo | bar" {
		t.Errorf("csvToText unexpected output: %s", txt)
	}

	name := rdr.extractFileNameFromURL("https://example.com/path/data.csv?version=1#top")
	if name != "data" {
		t.Errorf("extractFileNameFromURL got %s", name)
	}
}

// mockChunker implements chunking.Strategy returning doc unchanged to test custom strategy path.
type mockChunker struct{}

func (m mockChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return []*document.Document{doc}, nil
}

func TestCSVReader_ReadFromFileAndURL(t *testing.T) {
	data := "col1,col2\nfoo,bar\n"

	tmp, err := os.CreateTemp(t.TempDir(), "*.csv")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	_, _ = tmp.WriteString(data)
	tmp.Close()

	rdr := New(
		reader.WithCustomChunkingStrategy(mockChunker{}), // provided, ensures branch taken
	)

	// Test ReadFromFile
	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil || len(docs) != 1 {
		t.Fatalf("ReadFromFile error: %v len=%d", err, len(docs))
	}

	// Test ReadFromURL using httptest server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(data))
	}))
	defer srv.Close()

	docsURL, err := rdr.ReadFromURL(srv.URL + "/sample.csv")
	if err != nil || len(docsURL) != 1 {
		t.Fatalf("ReadFromURL error: %v len=%d", err, len(docsURL))
	}
	if docsURL[0].Name != "sample" {
		t.Errorf("expected extracted name 'sample', got %s", docsURL[0].Name)
	}
}

// TestCSVReader_SupportedExtensions verifies the list of supported extensions.
func TestCSVReader_SupportedExtensions(t *testing.T) {
	rdr := New()
	exts := rdr.SupportedExtensions()

	if len(exts) == 0 {
		t.Fatal("expected non-empty supported extensions")
	}

	// CSV reader should support .csv extension
	found := false
	for _, ext := range exts {
		if ext == ".csv" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected '.csv' in supported extensions")
	}
}

// TestCSVReader_ReadFromFileError verifies error handling for non-existent files.
func TestCSVReader_ReadFromFileError(t *testing.T) {
	rdr := New()
	_, err := rdr.ReadFromFile("/nonexistent/path/file.csv")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestCSVReader_ReadFromURLErrors verifies error handling for invalid URLs.
func TestCSVReader_ReadFromURLErrors(t *testing.T) {
	rdr := New()

	tests := []struct {
		name string
		url  string
	}{
		{"invalid_scheme_ftp", "ftp://example.com/data.csv"},
		{"invalid_scheme_file", "file:///local/data.csv"},
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

// TestCSVReader_ChunkDocumentDefaultStrategy verifies default chunking strategy initialization.
func TestCSVReader_ChunkDocumentDefaultStrategy(t *testing.T) {
	rdr := New(reader.WithChunk(true))

	csvData := "name,age\nAlice,30\nBob,25\n"
	docs, err := rdr.ReadFromReader("test", strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestCSVReader_ExtractFileNameFromURL tests URL filename extraction.
func TestCSVReader_ExtractFileNameFromURL(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"simple_filename", "https://example.com/data.csv", "data"},
		{"with_query_params", "https://example.com/export.csv?format=csv", "export"},
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
