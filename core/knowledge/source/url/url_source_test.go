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
