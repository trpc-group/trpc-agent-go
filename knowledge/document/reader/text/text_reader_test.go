//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package text

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

type errorTransformer struct {
	preprocessErr  error
	postprocessErr error
}

func (e *errorTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	if e.preprocessErr != nil {
		return nil, e.preprocessErr
	}
	return docs, nil
}

func (e *errorTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	if e.postprocessErr != nil {
		return nil, e.postprocessErr
	}
	return docs, nil
}

func (e *errorTransformer) Name() string { return "ErrorTransformer" }

func TestTextReader_TransformerErrors(t *testing.T) {
	tests := []struct {
		name        string
		transformer *errorTransformer
		wantErr     string
	}{
		{
			name:        "preprocess error",
			transformer: &errorTransformer{preprocessErr: errors.New("preprocess failed")},
			wantErr:     "failed to apply preprocess",
		},
		{
			name:        "postprocess error",
			transformer: &errorTransformer{postprocessErr: errors.New("postprocess failed")},
			wantErr:     "failed to apply postprocess",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdr := New(reader.WithTransformers(tt.transformer))

			// Test ReadFromReader
			_, err := rdr.ReadFromReader("test", strings.NewReader("content"))
			if err == nil {
				t.Error("ReadFromReader expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ReadFromReader expected error containing %q, got %q", tt.wantErr, err.Error())
			}

			// Test ReadFromFile
			tmp, _ := os.CreateTemp(t.TempDir(), "*.txt")
			tmp.WriteString("content")
			tmp.Close()
			_, err = rdr.ReadFromFile(tmp.Name())
			if err == nil {
				t.Error("ReadFromFile expected error, got nil")
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ReadFromFile expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestTextReader_WithTransformers(t *testing.T) {
	data := "hello\nworld"

	// Create a simple char filter
	filter := transform.NewCharFilter("\n")

	rdr := New(
		reader.WithChunk(false),
		reader.WithTransformers(filter),
	)

	docs, err := rdr.ReadFromReader("test", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 document")
	}

	// Expect "helloworld" because newline is removed
	if docs[0].Content != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", docs[0].Content)
	}
}

type mockErrorTransformer struct{}

func (m *mockErrorTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return nil, errors.New("preprocess error")
}

func (m *mockErrorTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return nil, errors.New("postprocess error")
}

func (m *mockErrorTransformer) Name() string {
	return "MockErrorTransformer"
}

func TestTextReader_WithTransformers_Error(t *testing.T) {
	t.Run("Preprocess Error", func(t *testing.T) {
		rdr := New(reader.WithTransformers(&mockErrorTransformer{}))
		_, err := rdr.ReadFromReader("test", strings.NewReader("test"))
		if err == nil {
			t.Error("expected error from preprocess")
		}
		if !strings.Contains(err.Error(), "preprocess error") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// For Postprocess error, we need a transformer that passes Preprocess but fails Postprocess.
	// We can reuse mockErrorTransformer but we need to control when it fails.
	// Or just create a struct with fields.
}

type mockSpecificErrorTransformer struct {
	failPre  bool
	failPost bool
}

func (m *mockSpecificErrorTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	if m.failPre {
		return nil, errors.New("preprocess error")
	}
	return docs, nil
}

func (m *mockSpecificErrorTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	if m.failPost {
		return nil, errors.New("postprocess error")
	}
	return docs, nil
}

func (m *mockSpecificErrorTransformer) Name() string {
	return "MockSpecificErrorTransformer"
}

func TestTextReader_WithTransformers_PostprocessError(t *testing.T) {
	rdr := New(reader.WithTransformers(&mockSpecificErrorTransformer{failPost: true}))
	_, err := rdr.ReadFromReader("test", strings.NewReader("test"))
	if err == nil {
		t.Error("expected error from postprocess")
	}
	if !strings.Contains(err.Error(), "postprocess error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTextReader_Read_NoChunk(t *testing.T) {
	data := "Hello world!"

	rdr := New(
		reader.WithChunk(false),
	)

	docs, err := rdr.ReadFromReader("greeting", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Content != data {
		t.Errorf("content mismatch")
	}
	if rdr.Name() != "TextReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestTextReader_FileAndURL(t *testing.T) {
	data := "sample content"

	tmp, _ := os.CreateTemp(t.TempDir(), "*.txt")
	tmp.WriteString(data)
	tmp.Close()

	rdr := New()

	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil || len(docs) != 1 {
		t.Fatalf("ReadFromFile err %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(data)) }))
	defer srv.Close()

	docsURL, err := rdr.ReadFromURL(srv.URL + "/a.txt")
	if err != nil || len(docsURL) != 1 {
		t.Fatalf("ReadFromURL err %v", err)
	}
	if docsURL[0].Name != "a" {
		t.Errorf("expected name a got %s", docsURL[0].Name)
	}
}

type failChunker struct{}

func (failChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("fail")
}

func TestTextReader_ChunkError(t *testing.T) {
	rdr := New(reader.WithCustomChunkingStrategy(failChunker{}))
	_, err := rdr.ReadFromReader("x", strings.NewReader("abc"))
	if err == nil {
		t.Fatalf("want error")
	}
}

// TestTextReader_SupportedExtensions verifies the list of supported extensions.
func TestTextReader_SupportedExtensions(t *testing.T) {
	rdr := New()
	exts := rdr.SupportedExtensions()

	if len(exts) == 0 {
		t.Fatal("expected non-empty supported extensions")
	}

	// Check for common text extensions
	expectedExts := map[string]bool{
		".txt":  false,
		".text": false,
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

// TestTextReader_ReadFromFileError verifies error handling for non-existent files.
func TestTextReader_ReadFromFileError(t *testing.T) {
	rdr := New()
	_, err := rdr.ReadFromFile("/nonexistent/path/file.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestTextReader_ReadFromURLErrors verifies error handling for invalid URLs.
func TestTextReader_ReadFromURLErrors(t *testing.T) {
	rdr := New()

	tests := []struct {
		name string
		url  string
	}{
		{"invalid_scheme_ftp", "ftp://example.com/file.txt"},
		{"invalid_scheme_file", "file:///local/file.txt"},
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

// TestTextReader_ChunkDocumentDefaultStrategy verifies default chunking strategy initialization.
func TestTextReader_ChunkDocumentDefaultStrategy(t *testing.T) {
	// Create reader with chunking enabled but no strategy provided
	rdr := New(reader.WithChunk(true))

	// Read from reader should trigger chunkDocument with default strategy
	docs, err := rdr.ReadFromReader("test", strings.NewReader("test content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Error("expected at least one document")
	}
}

// TestTextReader_ExtractFileNameFromURL tests URL filename extraction.
func TestTextReader_ExtractFileNameFromURL(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"simple_filename", "https://example.com/document.txt", "document"},
		{"with_query_params", "https://example.com/file.txt?v=1", "file"},
		{"with_fragment", "https://example.com/file.txt#section", "file"},
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
