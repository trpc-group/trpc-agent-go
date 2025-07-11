package file

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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

// stubReader is a minimal reader.Reader implementation used for testing
// SetReader and metadata attachment flows.
// It always returns a single empty document.
// The implementation is intentionally simple to avoid external deps.
type stubReader struct{}

func (stubReader) ReadFromReader(name string, r io.Reader) ([]*document.Document, error) {
	return []*document.Document{{}}, nil
}

func (stubReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	return []*document.Document{{}}, nil
}

func (stubReader) ReadFromURL(url string) ([]*document.Document, error) {
	return []*document.Document{{}}, nil
}

func (stubReader) Name() string { return "stub" }

// TestProcessFile_Directory ensures an error is returned when a directory path
// is passed to processFile.
func TestProcessFile_Directory(t *testing.T) {
	dir := t.TempDir()
	src := New([]string{})
	if _, err := src.processFile(dir); err == nil {
		t.Fatalf("expected error for directory path")
	}
}

// TestSetReaderAndMetadata verifies custom reader registration and metadata
// propagation.
func TestSetReaderAndMetadata(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	const fileName = "sample.txt"
	filePath := filepath.Join(tmpDir, fileName)
	if err := os.WriteFile(filePath, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	src := New([]string{filePath})

	// Register stub reader for .txt files so we do not depend on real reader.
	src.SetReader("text", stubReader{})

	// Inject custom metadata.
	const metaKey = "custom"
	const metaVal = "value"
	src.SetMetadata(metaKey, metaVal)

	docs, err := src.ReadDocuments(ctx)
	if err != nil {
		t.Fatalf("ReadDocuments returned error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if v, ok := docs[0].Metadata[metaKey]; !ok || v != metaVal {
		t.Fatalf("expected metadata %s=%s, got %v", metaKey, metaVal, v)
	}
}

// TestOptions verify functional options correctly modify Source fields.
func TestOptions(t *testing.T) {
	const (
		customName   = "my-source"
		chunkSize    = 8
		chunkOverlap = 2
		metaKey      = "k"
		metaValue    = "v"
	)

	src := New([]string{"dummy"},
		WithName(customName),
		WithChunkSize(chunkSize),
		WithChunkOverlap(chunkOverlap),
		WithMetadataValue(metaKey, metaValue),
	)

	if src.name != customName {
		t.Fatalf("expected name %s, got %s", customName, src.name)
	}
	if src.chunkSize != chunkSize {
		t.Fatalf("expected chunkSize %d, got %d", chunkSize, src.chunkSize)
	}
	if src.chunkOverlap != chunkOverlap {
		t.Fatalf("expected chunkOverlap %d, got %d", chunkOverlap, src.chunkOverlap)
	}
	if v, ok := src.metadata[metaKey]; !ok || v != metaValue {
		t.Fatalf("metadata not set correctly")
	}
}
func TestProcessFile_Unsupported(t *testing.T) {
	src := New([]string{})
	_, err := src.processFile("nonexistent.xyz")
	if err == nil {
		t.Fatalf("expected error for unsupported file")
	}
}
