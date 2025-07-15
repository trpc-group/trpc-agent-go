//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
			if sz, ok := d.Metadata["chunk_size"].(int); ok && sz > chunkSize {
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
