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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

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
