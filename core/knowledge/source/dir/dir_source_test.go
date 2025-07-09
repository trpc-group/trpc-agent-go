package dir

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestReadDocuments verifies Directory Source with and without
// custom chunk configuration.
func TestReadDocuments(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	// Create two small files to ensure multiple documents are produced.
	for i := 0; i < 2; i++ {
		filePath := filepath.Join(tmpDir, "file"+strconv.Itoa(i)+".txt")
		content := strings.Repeat("0123456789", 5) // 50 chars
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}
	}

	t.Run("default-config", func(t *testing.T) {
		src := New([]string{tmpDir}, WithRecursive(false))
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
			[]string{tmpDir},
			WithRecursive(false),
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
