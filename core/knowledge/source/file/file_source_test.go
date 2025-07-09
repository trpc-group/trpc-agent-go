package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadDocuments verifies that File Source correctly reads documents with
// and without custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")

	// Prepare sample content about 50 characters to ensure multiple chunks.
	content := strings.Repeat("0123456789", 5) // 50 chars
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{filePath})
		docs, err := src.ReadDocuments(ctx)
		if err != nil {
			t.Fatalf("ReadDocuments returned error: %v", err)
		}
		if len(docs) == 0 {
			t.Fatalf("expected at least one document, got 0")
		}
	})

	t.Run("custom-chunk-config", func(t *testing.T) {
		const chunkSize = 10
		const overlap = 2
		src := New(
			[]string{filePath},
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
			sz, ok := d.Metadata["chunk_size"].(int)
			if !ok {
				t.Fatalf("chunk_size metadata missing or not int")
			}
			if sz > chunkSize {
				t.Fatalf("chunk size %d exceeds expected max %d", sz, chunkSize)
			}
		}
	})
}
