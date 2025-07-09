package auto

import (
	"context"
	"strings"
	"testing"
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
