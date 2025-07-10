package auto

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadDocuments verifies Auto Source handles plain text input with and
// without custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()
	input := strings.Repeat("0123456789", 5) // 50 chars

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{input})
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
			[]string{input},
			WithChunkSize(chunkSize),
			WithChunkOverlap(overlap),
		)
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) <= 1 {
			t.Fatalf("expected multiple chunks, got %d", len(docs))
		}
		for _, d := range docs {
			if sz, ok := d.Metadata["chunk_size"].(int); ok && sz > chunkSize {
				t.Fatalf("chunk size %d exceeds expected max %d", sz, chunkSize)
			}
		}
	})
}

func TestHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("content"), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	src := New([]string{})

	require.True(t, src.isDirectory(tmpDir))
	require.True(t, src.isFile(tmpFile))

	u := &url.URL{Scheme: "https", Host: "example.com"}
	require.True(t, src.isURL(u.String()))
	require.False(t, src.isURL("not a url"))
}

func TestSource_ProcessInputVariants(t *testing.T) {
	ctx := context.Background()

	// 1. Text input variant.
	src := New([]string{"hello world"})
	docs, err := src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("text input failed, err=%v docs=%d", err, len(docs))
	}

	// 2. File input variant.
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(tmpFile, []byte("file content"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	src = New([]string{tmpFile})
	docs, err = src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("file input failed, err=%v docs=%d", err, len(docs))
	}

	// 3. Directory variant.
	src = New([]string{tmpDir})
	docs, err = src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("directory input failed, err=%v docs=%d", err, len(docs))
	}

	// 4. URL variant using httptest server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("u", 20)))
	}))
	defer ts.Close()

	src = New([]string{ts.URL})
	// Set small chunk size to force processing path.
	src.chunkSize = 10
	docs, err = src.ReadDocuments(ctx)
	if err != nil || len(docs) == 0 {
		t.Fatalf("url input failed, err=%v docs=%d", err, len(docs))
	}
}
