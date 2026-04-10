//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package url

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
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

type mockContentExtractor struct{}

func (m *mockContentExtractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return &extractor.Result{
		Reader: strings.NewReader("# Extracted\n\ncontent"),
		Format: extractor.FormatMarkdown,
	}, nil
}

func (m *mockContentExtractor) ExtractFromReader(ctx context.Context, reader io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	return &extractor.Result{
		Reader: strings.NewReader("# Extracted\n\ncontent"),
		Format: extractor.FormatMarkdown,
	}, nil
}

func (m *mockContentExtractor) SupportedFormats() []string {
	return []string{".pdf"}
}

func (m *mockContentExtractor) Close() error {
	return nil
}

type pngContentExtractor struct {
	called bool
}

func (m *pngContentExtractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return m.ExtractFromReader(ctx, strings.NewReader(string(data)), opts...)
}

func (m *pngContentExtractor) ExtractFromReader(ctx context.Context, reader io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	m.called = true
	return &extractor.Result{
		Reader: strings.NewReader("# Extracted PNG\n\ncontent"),
		Format: extractor.FormatMarkdown,
	}, nil
}

func (m *pngContentExtractor) SupportedFormats() []string {
	return []string{".png"}
}

func (m *pngContentExtractor) Close() error {
	return nil
}

// TestReadDocuments verifies URL Source with and without custom chunk
// configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()

	content := strings.Repeat("0123456789", 5) // 50 chars
	// Create an HTTP test server returning plain text.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()

	rawURL := server.URL

	// Sanity check parsed URL so that test fails early if invalid.
	if _, err := neturl.Parse(rawURL); err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{rawURL})
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
			[]string{rawURL},
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
		for _, d := range docs {
			if sz, ok := d.Metadata[source.MetaChunkSize].(int); ok && sz > chunkSize {
				t.Fatalf("chunk size %d exceeds expected max %d", sz, chunkSize)
			}
		}
	})
}

// TestSource_getFileName ensures file name inference behaves as expected.
func TestSource_getFileName(t *testing.T) {
	s := &Source{}

	testCases := []struct {
		name        string
		rawURL      string
		contentType string
		wantSuffix  string
	}{
		{
			name:        "path-provides-name",
			rawURL:      "https://example.com/path/file.txt",
			contentType: "text/plain",
			wantSuffix:  "file.txt",
		},
		{
			name:        "html-content-type",
			rawURL:      "https://example.com/",
			contentType: "text/html; charset=utf-8",
			wantSuffix:  "index.html",
		},
		{
			name:        "host-fallback",
			rawURL:      "https://example.com/",
			contentType: "",
			wantSuffix:  "example.com.txt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := neturl.Parse(tc.rawURL)
			if err != nil {
				t.Fatalf("failed to parse url: %v", err)
			}
			got := s.getFileName(parsed, tc.contentType)
			if got != tc.wantSuffix {
				t.Fatalf("got %s want %s", got, tc.wantSuffix)
			}
		})
	}
}

func TestReadDocuments_InvalidURL(t *testing.T) {
	src := New([]string{"http://:@invalid"})
	if _, err := src.ReadDocuments(context.Background()); err == nil {
		t.Fatalf("expected error for invalid url")
	}
}

// TestWithMetadata verifies the WithMetadata option.
func TestWithMetadata(t *testing.T) {
	meta := map[string]any{
		"source":   "test-source",
		"priority": "high",
		"category": "documentation",
	}

	src := New([]string{"https://example.com"}, WithMetadata(meta))

	for k, expectedValue := range meta {
		if actualValue, ok := src.metadata[k]; !ok || actualValue != expectedValue {
			t.Fatalf("metadata[%s] not set correctly, expected %v, got %v", k, expectedValue, actualValue)
		}
	}
}

func TestWithMetadataCopiesInputMap(t *testing.T) {
	meta := map[string]any{"source": "test-source"}
	src := New([]string{"https://example.com"}, WithMetadata(meta))

	meta["source"] = "changed"
	meta["new"] = "value"

	if got := src.metadata["source"]; got != "test-source" {
		t.Fatalf("metadata should be copied, got %v", got)
	}
	if _, ok := src.metadata["new"]; ok {
		t.Fatal("source metadata should not observe new keys added to input map")
	}
}

// TestWithMetadataValue verifies the WithMetadataValue option.
func TestWithMetadataValue(t *testing.T) {
	const metaKey = "url_key"
	const metaValue = "url_value"

	src := New([]string{"https://example.com"}, WithMetadataValue(metaKey, metaValue))

	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("WithMetadataValue not applied correctly, expected %s, got %v", metaValue, v)
	}
}

// TestSetMetadata verifies the SetMetadata method.
func TestSetMetadata(t *testing.T) {
	src := New([]string{"https://example.com"})

	const metaKey = "dynamic_url_key"
	const metaValue = "dynamic_url_value"

	src.SetMetadata(metaKey, metaValue)

	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("SetMetadata not applied correctly, expected %s, got %v", metaValue, v)
	}
}

// TestSetMetadataMultiple verifies setting multiple metadata values.
func TestSetMetadataMultiple(t *testing.T) {
	src := New([]string{"https://example.com"})

	metadata := map[string]any{
		"url_key1": "url_value1",
		"url_key2": "url_value2",
		"url_key3": 456,
		"url_key4": false,
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
			expectedName: "URL Source",
		},
		{
			name:         "custom_name",
			opts:         []Option{WithName("Custom URL")},
			expectedName: "Custom URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := New([]string{"https://example.com"}, tt.opts...)

			if src.Name() != tt.expectedName {
				t.Errorf("Name() = %s, want %s", src.Name(), tt.expectedName)
			}

			if src.Type() != source.TypeURL {
				t.Errorf("Type() = %s, want %s", src.Type(), source.TypeURL)
			}
		})
	}
}

// TestGetMetadata verifies GetMetadata returns a copy of metadata.
func TestGetMetadata(t *testing.T) {
	meta := map[string]any{
		"key1": "value1",
		"key2": 999,
	}

	src := New([]string{"https://example.com"}, WithMetadata(meta))

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

// TestReadDocumentsWithEmptyURLs verifies behavior with empty URLs.
func TestReadDocumentsWithEmptyURLs(t *testing.T) {
	ctx := context.Background()
	src := New([]string{})

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Errorf("ReadDocuments with empty URLs should not error, got %v", err)
	}
	if docs != nil {
		t.Errorf("ReadDocuments with empty URLs should return nil, got %v", docs)
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

// TestWithHTTPClient verifies WithHTTPClient option.
func TestWithHTTPClient(t *testing.T) {
	customClient := &http.Client{Timeout: 5 * time.Second}
	src := New([]string{"https://example.com"}, WithHTTPClient(customClient))

	if src.httpClient != customClient {
		t.Error("WithHTTPClient did not set custom HTTP client")
	}
}

// TestGetFileNameVariants verifies getFileName with various edge cases.
func TestGetFileNameVariants(t *testing.T) {
	s := &Source{}

	tests := []struct {
		name        string
		rawURL      string
		contentType string
		want        string
	}{
		{
			name:        "csv_content_type",
			rawURL:      "https://example.com/",
			contentType: "text/csv",
			want:        "document.csv",
		},
		{
			name:        "pdf_content_type",
			rawURL:      "https://example.com/",
			contentType: "application/pdf",
			want:        "document.pdf",
		},
		{
			name:        "unknown_content_type",
			rawURL:      "https://example.com/",
			contentType: "application/octet-stream",
			want:        "document",
		},
		{
			name:        "empty_content_type_with_host",
			rawURL:      "https://example.com/",
			contentType: "",
			want:        "example.com.txt",
		},
		{
			name:        "json_content_type_root_path",
			rawURL:      "https://api.example.com/",
			contentType: "application/json",
			want:        "document.json",
		},
		{
			name:        "path_provides_filename",
			rawURL:      "https://example.com/path/data.json",
			contentType: "text/plain",
			want:        "data.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := neturl.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("failed to parse URL: %v", err)
			}
			got := s.getFileName(parsed, tt.contentType)
			if got != tt.want {
				t.Errorf("getFileName() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestProcessURLHTTPError verifies error handling for non-200 HTTP status.
func TestProcessURLHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	src := New([]string{server.URL})
	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Error("expected error for non-200 HTTP status")
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

// TestProcessURLMetadata verifies metadata is properly set for URL documents.
func TestProcessURLMetadata(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("test content"))
	}))
	defer server.Close()

	src := New([]string{server.URL}, WithMetadataValue("custom_key", "custom_value"))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Check custom metadata
	if v, ok := docs[0].Metadata["custom_key"]; !ok || v != "custom_value" {
		t.Errorf("custom metadata not set, got %v", docs[0].Metadata)
	}

	// Check URL metadata
	if v, ok := docs[0].Metadata[source.MetaURL]; !ok || v != server.URL {
		t.Errorf("URL metadata not set correctly, got %v", docs[0].Metadata[source.MetaURL])
	}

	// Check source type
	if v, ok := docs[0].Metadata[source.MetaSource]; !ok || v != source.TypeURL {
		t.Errorf("source type not set correctly, got %v", docs[0].Metadata[source.MetaSource])
	}
}

// TestWithCustomChunkingStrategy verifies the WithCustomChunkingStrategy option.
func TestWithCustomChunkingStrategy(t *testing.T) {
	strategy := &mockChunkingStrategy{name: "test-strategy"}
	src := New([]string{"https://example.com"}, WithCustomChunkingStrategy(strategy))

	if src.customChunkingStrategy != strategy {
		t.Error("WithCustomChunkingStrategy did not set custom chunking strategy")
	}
}

// TestWithContentFetchingURL verifies the WithContentFetchingURL option functionality.
func TestWithContentFetchingURL(t *testing.T) {
	ctx := context.Background()

	// Content for different servers
	identifierContent := "This is identifier content"
	fetchContent := "This is fetch content"

	// Create identifier server
	identifierServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(identifierContent))
	}))
	defer identifierServer.Close()

	// Create fetch server
	fetchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(fetchContent))
	}))
	defer fetchServer.Close()

	tests := []struct {
		name           string
		setupSource    func() *Source
		expectedError  bool
		validateResult func(t *testing.T, docs []*document.Document)
	}{
		{
			name: "basic_content_fetching_url",
			setupSource: func() *Source {
				return New(
					[]string{identifierServer.URL + "/doc.txt"},
					WithContentFetchingURL([]string{fetchServer.URL + "/doc.txt"}),
				)
			},
			expectedError: false,
			validateResult: func(t *testing.T, docs []*document.Document) {
				if len(docs) == 0 {
					t.Fatal("expected at least one document")
				}
				// Content should come from fetch server
				if !strings.Contains(docs[0].Content, fetchContent) {
					t.Errorf("expected content from fetch server, got: %s", docs[0].Content)
				}
				// Metadata should use identifier URL
				if metaURL, ok := docs[0].Metadata[source.MetaURL].(string); !ok || !strings.Contains(metaURL, identifierServer.URL) {
					t.Errorf("expected metadata URL to be identifier URL, got: %v", metaURL)
				}
			},
		},
		{
			name: "mismatched_url_count",
			setupSource: func() *Source {
				return New(
					[]string{identifierServer.URL + "/doc1.txt", identifierServer.URL + "/doc2.txt"},
					WithContentFetchingURL([]string{fetchServer.URL + "/doc1.txt"}), // Only one fetch URL for two identifier URLs
				)
			},
			expectedError: true,
			validateResult: func(t *testing.T, docs []*document.Document) {
				// Should not reach here due to error
			},
		},
		{
			name: "multiple_urls_with_fetching",
			setupSource: func() *Source {
				return New(
					[]string{identifierServer.URL + "/doc1.txt", identifierServer.URL + "/doc2.txt"},
					WithContentFetchingURL([]string{fetchServer.URL + "/doc1.txt", fetchServer.URL + "/doc2.txt"}),
				)
			},
			expectedError: false,
			validateResult: func(t *testing.T, docs []*document.Document) {
				if len(docs) < 2 {
					t.Fatal("expected at least two documents")
				}
				// All documents should have content from fetch server
				for _, doc := range docs {
					if !strings.Contains(doc.Content, fetchContent) {
						t.Errorf("expected content from fetch server, got: %s", doc.Content)
					}
					// Metadata should use identifier URL
					if metaURL, ok := doc.Metadata[source.MetaURL].(string); !ok || !strings.Contains(metaURL, identifierServer.URL) {
						t.Errorf("expected metadata URL to be identifier URL, got: %v", metaURL)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.setupSource()
			docs, err := src.ReadDocuments(ctx)

			if tt.expectedError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tt.validateResult(t, docs)
		})
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
			src := New([]string{"https://example.com"}, WithFileReaderType(tt.fileReaderType))

			if src.fileReaderType != tt.fileReaderType {
				t.Errorf("fileReaderType = %s, want %s", src.fileReaderType, tt.fileReaderType)
			}
		})
	}
}

// TestFileReaderTypeOverridesContentType verifies that WithFileReaderType overrides content-type detection.
func TestFileReaderTypeOverridesContentType(t *testing.T) {
	ctx := context.Background()

	t.Run("plain_text_server_with_json_reader", func(t *testing.T) {
		// Server returns text/plain content-type but content is JSON
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(`{"key": "value"}`))
		}))
		defer server.Close()

		// Force JSON reader
		src := New([]string{server.URL}, WithFileReaderType(source.FileReaderTypeJSON))
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
		if len(docs) == 0 {
			t.Fatal("expected at least one document")
		}
	})

	t.Run("html_server_with_markdown_reader", func(t *testing.T) {
		// Server returns text/html but we want markdown processing
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("# Title\n\nContent"))
		}))
		defer server.Close()

		src := New([]string{server.URL}, WithFileReaderType(source.FileReaderTypeMarkdown))
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments failed: %v", err)
		}
		if len(docs) == 0 {
			t.Fatal("expected at least one document")
		}
	})

	t.Run("default_detection_without_override", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("plain text"))
		}))
		defer server.Close()

		src := New([]string{server.URL})
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("word ", 100)))
	}))
	defer server.Close()

	src := New([]string{server.URL},
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

	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
}

func TestWithExtractor(t *testing.T) {
	src := New([]string{"https://example.com"}, WithExtractor(&mockContentExtractor{}))

	if src.contentExtractor == nil {
		t.Fatal("content extractor should be set")
	}
}

func TestReadDocuments_WithExtractor(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-test"))
	}))
	defer server.Close()

	src := New([]string{server.URL}, WithExtractor(&mockContentExtractor{}))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	if docs[0].Content == "" {
		t.Fatal("expected extracted content")
	}
}

func TestReadDocuments_WithExtractor_ContentTypeFallback(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-test"))
	}))
	defer server.Close()

	src := New([]string{server.URL + "/1706.03762"}, WithExtractor(&mockContentExtractor{}))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	if docs[0].Content == "" {
		t.Fatal("expected extracted content")
	}
}

func TestReadDocuments_WithUnsupportedExtractorExtensionFallsBackToReader(t *testing.T) {
	ctx := context.Background()
	ext := &mockContentExtractor{}
	content := "plain text fallback"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()

	src := New([]string{server.URL + "/notes.txt"}, WithExtractor(ext))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	if docs[0].Content != content {
		t.Fatalf("expected reader fallback content %q, got %q", content, docs[0].Content)
	}
}

func TestReadDocuments_WithExtractor_ContentTypeFallbackForImages(t *testing.T) {
	ctx := context.Background()
	ext := &pngContentExtractor{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-bytes"))
	}))
	defer server.Close()

	src := New([]string{server.URL + "/download"}, WithExtractor(ext))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	if !ext.called {
		t.Fatal("expected extractor to be used via content-type fallback")
	}
	if docs[0].Content == "" {
		t.Fatal("expected extracted content")
	}
}

// mockTransformer is a simple Transformer implementation for testing.
type mockTransformer struct {
	name      string
	preCalls  int
	postCalls int
}

func (m *mockTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	m.preCalls++
	return docs, nil
}

func (m *mockTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	m.postCalls++
	for _, doc := range docs {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		doc.Metadata["transformer"] = m.name
	}
	return docs, nil
}

func (m *mockTransformer) Name() string {
	return m.name
}

// TestWithTransformers verifies the WithTransformers option sets transformers correctly.
func TestWithTransformers(t *testing.T) {
	t1 := &mockTransformer{name: "t1"}
	t2 := &mockTransformer{name: "t2"}

	src := New([]string{"https://example.com"}, WithTransformers(t1, t2))

	if len(src.transformers) != 2 {
		t.Fatalf("expected 2 transformers, got %d", len(src.transformers))
	}
}

// TestWithTransformers_AppliedToReaders verifies transformers are passed to readers via New().
func TestWithTransformers_AppliedToReaders(t *testing.T) {
	ctx := context.Background()
	transformer := &mockTransformer{name: "test-transformer"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()

	src := New([]string{server.URL}, WithTransformers(transformer))
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments with transformer failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	if transformer.preCalls == 0 {
		t.Fatal("expected transformer Preprocess to be called")
	}
	if transformer.postCalls == 0 {
		t.Fatal("expected transformer Postprocess to be called")
	}
	if got := docs[0].Metadata["transformer"]; got != transformer.name {
		t.Fatalf("expected transformer metadata %q, got %v", transformer.name, got)
	}
}

// TestExtractorExtFromContentType verifies all branches of extractorExtFromContentType.
func TestExtractorExtFromContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{name: "empty", contentType: "", want: ""},
		{name: "text/html", contentType: "text/html; charset=utf-8", want: ".html"},
		{name: "application/pdf", contentType: "application/pdf", want: ".pdf"},
		{name: "docx", contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", want: ".docx"},
		{name: "pptx", contentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", want: ".pptx"},
		{name: "xlsx", contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", want: ".xlsx"},
		{name: "text/csv", contentType: "text/csv", want: ".csv"},
		{name: "image/png", contentType: "image/png", want: ".png"},
		{name: "image/jpeg", contentType: "image/jpeg", want: ".jpg"},
		{name: "image/tiff", contentType: "image/tiff", want: ".tiff"},
		{name: "image/bmp", contentType: "image/bmp", want: ".bmp"},
		{name: "unknown", contentType: "application/octet-stream", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractorExtFromContentType(tt.contentType)
			if got != tt.want {
				t.Errorf("extractorExtFromContentType(%q) = %q, want %q", tt.contentType, got, tt.want)
			}
		})
	}
}

// errorExtractor simulates an extractor that always fails extraction.
type errorExtractor struct{}

func (e *errorExtractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return nil, fmt.Errorf("extract error")
}

func (e *errorExtractor) ExtractFromReader(ctx context.Context, reader io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	return nil, fmt.Errorf("extract from reader error")
}

func (e *errorExtractor) SupportedFormats() []string {
	return []string{".pdf"}
}

func (e *errorExtractor) Close() error {
	return nil
}

// unknownFormatExtractor returns an unknown format so no reader is found.
type unknownFormatExtractor struct{}

func (e *unknownFormatExtractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return &extractor.Result{Reader: strings.NewReader("data"), Format: "unknown_format_xyz"}, nil
}

func (e *unknownFormatExtractor) ExtractFromReader(ctx context.Context, reader io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	return &extractor.Result{Reader: strings.NewReader("data"), Format: "unknown_format_xyz"}, nil
}

func (e *unknownFormatExtractor) SupportedFormats() []string {
	return []string{".pdf"}
}

func (e *unknownFormatExtractor) Close() error {
	return nil
}

// TestReadDocuments_ExtractorError verifies error propagation when ExtractFromReader fails.
func TestReadDocuments_ExtractorError(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-test"))
	}))
	defer server.Close()

	src := New([]string{server.URL + "/doc.pdf"}, WithExtractor(&errorExtractor{}))
	_, err := src.ReadDocuments(ctx)
	if err == nil {
		t.Fatal("expected error when ExtractFromReader fails")
	}
}

// TestReadDocuments_ExtractorUnknownFormat verifies error when ExtractFromReader returns unknown format.
func TestReadDocuments_ExtractorUnknownFormat(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-test"))
	}))
	defer server.Close()

	src := New([]string{server.URL + "/doc.pdf"}, WithExtractor(&unknownFormatExtractor{}))
	_, err := src.ReadDocuments(ctx)
	if err == nil {
		t.Fatal("expected error when no reader for extracted format")
	}
}

// TestReadDocuments_UnknownContentTypeFallsBackToText verifies that unknown content-type falls back to text reader.
func TestReadDocuments_UnknownContentTypeFallsBackToText(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-unknown-type-xyz")
		_, _ = w.Write([]byte("some data"))
	}))
	defer server.Close()

	// Unknown content-type falls back to text reader, so no error expected.
	src := New([]string{server.URL})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("unexpected error for unknown content-type: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document from text fallback reader")
	}
}

// TestGetFileName_TextPlain verifies getFileName returns document.txt for text/plain.
func TestGetFileName_TextPlain(t *testing.T) {
	s := &Source{}
	parsed, _ := neturl.Parse("https://example.com/")
	got := s.getFileName(parsed, "text/plain")
	if got != "document.txt" {
		t.Errorf("getFileName() = %q, want %q", got, "document.txt")
	}
}

// TestGetFileName_NoHostNoPath verifies getFileName fallback when no host and no path.
func TestGetFileName_NoHostNoPath(t *testing.T) {
	s := &Source{}
	parsed := &neturl.URL{}
	got := s.getFileName(parsed, "")
	if got != "document.txt" {
		t.Errorf("getFileName() = %q, want %q", got, "document.txt")
	}
}

// TestWithMetadata_NilMetadataInit verifies WithMetadata initializes nil metadata map.
func TestWithMetadata_NilMetadataInit(t *testing.T) {
	s := &Source{}
	opt := WithMetadata(map[string]any{"key": "value"})
	opt(s)

	if v, ok := s.metadata["key"]; !ok || v != "value" {
		t.Errorf("WithMetadata should initialize nil metadata map, got %v", s.metadata)
	}
}

// TestReadDocuments_WithExtractor_WithMetadata verifies metadata is preserved when using a fetch URL.
func TestReadDocuments_WithExtractor_WithMetadata(t *testing.T) {
	ctx := context.Background()
	ext := &mockContentExtractor{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fetch/test.pdf" {
			t.Fatalf("expected fetch path %q, got %q", "/fetch/test.pdf", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-test"))
	}))
	defer server.Close()
	fetchURL := server.URL + "/fetch/test.pdf"
	identifierURL := server.URL + "/source/original%2Bdoc.pdf?download=1"

	src := New(
		[]string{identifierURL},
		WithContentFetchingURL([]string{fetchURL}),
		WithExtractor(ext),
		WithMetadataValue("custom_key", "custom_value"),
		WithName("test-source"),
	)
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	// Verify custom metadata is propagated
	if v, ok := docs[0].Metadata["custom_key"]; !ok || v != "custom_value" {
		t.Errorf("custom metadata not propagated in readExtractedResult, got %v", docs[0].Metadata)
	}
	// Verify source metadata
	if v, ok := docs[0].Metadata[source.MetaSource]; !ok || v != source.TypeURL {
		t.Errorf("source type not set in readExtractedResult, got %v", docs[0].Metadata[source.MetaSource])
	}
	// Verify source name
	if v, ok := docs[0].Metadata[source.MetaSourceName]; !ok || v != "test-source" {
		t.Errorf("source name not set in readExtractedResult, got %v", docs[0].Metadata[source.MetaSourceName])
	}
	if got := docs[0].Metadata[source.MetaURL]; got != identifierURL {
		t.Errorf("expected MetaURL %q, got %v", identifierURL, got)
	}
	if got := docs[0].Metadata[source.MetaURI]; got != identifierURL {
		t.Errorf("expected MetaURI %q, got %v", identifierURL, got)
	}
	if got := docs[0].Metadata[source.MetaURLPath]; got != "/source/original+doc.pdf" {
		t.Errorf("expected MetaURLPath %q, got %v", "/source/original+doc.pdf", got)
	}
}

// TestReadDocuments_DocumentWithNilMetadata verifies that documents with nil Metadata are handled.
func TestReadDocuments_DocumentWithNilMetadata(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	src := New([]string{server.URL})
	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
	// All documents should have metadata set
	for _, doc := range docs {
		if doc.Metadata == nil {
			t.Error("document metadata should not be nil after ReadDocuments")
		}
		if _, ok := doc.Metadata[source.MetaSource]; !ok {
			t.Error("document should have MetaSource set")
		}
	}
}

// TestExtractFromResponse_UnknownFormat verifies error when ExtractFromReader returns unknown format.
func TestExtractFromResponse_UnknownFormat(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-test"))
	}))
	defer server.Close()

	// unknownFormatExtractor returns an unknown format so no reader is found
	src := New([]string{server.URL + "/doc.pdf"}, WithExtractor(&unknownFormatExtractor{}))
	_, err := src.ReadDocuments(ctx)
	if err == nil {
		t.Fatal("expected error when no reader for extracted format")
	}
}

// failReadExtractor returns markdown format but the reader will fail.
type failReadExtractor struct{}

func (e *failReadExtractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return e.ExtractFromReader(ctx, strings.NewReader(string(data)), opts...)
}

func (e *failReadExtractor) ExtractFromReader(ctx context.Context, reader io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	return &extractor.Result{
		Reader: strings.NewReader("data"),
		Format: extractor.FormatMarkdown,
	}, nil
}

func (e *failReadExtractor) SupportedFormats() []string {
	return []string{".pdf"}
}

func (e *failReadExtractor) Close() error { return nil }

// TestFetchAndRead_CreateRequestError verifies error when request creation fails.
func TestFetchAndRead_CreateRequestError(t *testing.T) {
	src := New([]string{"http://\x00invalid"})
	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
