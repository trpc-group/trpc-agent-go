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
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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
