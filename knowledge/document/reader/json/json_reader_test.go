//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

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

func TestJSONReadFromReaderNoChunk(t *testing.T) {
	data := `{"name":"Alice","age":30}`

	rdr := New(
		reader.WithChunk(false),
	)

	docs, err := rdr.ReadFromReader("person", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	// jsonToText should pretty-print JSON, so content will have newlines and spaces.
	if !strings.Contains(docs[0].Content, "\"name\": \"Alice\"") {
		t.Errorf("pretty JSON content mismatch: %s", docs[0].Content)
	}
	if rdr.Name() != "JSONReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestJSONReader_FileAndURL(t *testing.T) {
	data := `{"k":"v"}`

	tmp, err := os.CreateTemp(t.TempDir(), "*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString(data)
	tmp.Close()

	rdr := New()

	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil || len(docs) != 1 {
		t.Fatalf("ReadFromFile error: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(data))
	}))
	defer srv.Close()

	docsURL, err := rdr.ReadFromURL(srv.URL + "/conf.json")
	if err != nil || len(docsURL) != 1 {
		t.Fatalf("ReadFromURL error: %v", err)
	}
	if docsURL[0].Name != "conf" {
		t.Errorf("expected extracted name conf, got %s", docsURL[0].Name)
	}
}

// mockChunker returns err to ensure chunkDocument path executed.
type errChunker struct{}

func (e errChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("fail")
}

func TestJSONReader_jsonToTextError(t *testing.T) {
	rdr := New().(*Reader)
	_, err := rdr.jsonToText("{invalid json}")
	if err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func TestJSONReader_CustomChunkerError(t *testing.T) {
	rdr := New(reader.WithCustomChunkingStrategy(errChunker{}))
	// use simple valid json
	docs, err := rdr.ReadFromReader("n", strings.NewReader(`{"a":1}`))
	if err == nil {
		t.Fatalf("expected chunker error, got nil")
	}
	if docs != nil {
		t.Fatalf("expected nil docs on error")
	}
	_ = http.MethodGet // silence unused import due to build tags
}

// TestJSONReader_SupportedExtensions verifies the list of supported extensions.
func TestJSONReader_SupportedExtensions(t *testing.T) {
	rdr := New()
	exts := rdr.SupportedExtensions()

	if len(exts) == 0 {
		t.Fatal("expected non-empty supported extensions")
	}

	// JSON reader should support .json extension
	found := false
	for _, ext := range exts {
		if ext == ".json" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected '.json' in supported extensions")
	}
}

// TestJSONReader_ReadFromFileError verifies error handling for non-existent files.
func TestJSONReader_ReadFromFileError(t *testing.T) {
	rdr := New()
	_, err := rdr.ReadFromFile("/nonexistent/path/file.json")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestJSONReader_ReadFromURLErrors verifies error handling for invalid URLs.
func TestJSONReader_ReadFromURLErrors(t *testing.T) {
	rdr := New()

	tests := []struct {
		name string
		url  string
	}{
		{"invalid_scheme_ftp", "ftp://example.com/data.json"},
		{"invalid_scheme_file", "file:///local/data.json"},
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

// TestJSONReader_ChunkDocumentDefaultStrategy verifies default chunking strategy initialization.
func TestJSONReader_ChunkDocumentDefaultStrategy(t *testing.T) {
	rdr := New(reader.WithChunk(true))

	jsonData := `{"name":"test","value":123}`
	docs, err := rdr.ReadFromReader("test", strings.NewReader(jsonData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestJSONReader_ExtractFileNameFromURL tests URL filename extraction.
func TestJSONReader_ExtractFileNameFromURL(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"simple_filename", "https://api.example.com/data.json", "data"},
		{"with_query_params", "https://api.example.com/api.json?key=value", "api"},
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
