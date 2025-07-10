package csv

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestCSVReader_ReadFromReader_NoChunk(t *testing.T) {
	data := "name,age\nAlice,30\nBob,25\n"

	rdr := New(
		WithChunking(false),
	)

	docs, err := rdr.ReadFromReader("people", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	// csvToText should replace commas with " | ".
	if !strings.Contains(docs[0].Content, "Alice | 30") {
		t.Errorf("converted content mismatch: %s", docs[0].Content)
	}
	if rdr.Name() != "CSVReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestCSVReader_ChunkingAndHelpers(t *testing.T) {
	data := "a,b,c\n1,2,3\n4,5,6\n"
	rdr := New() // default chunking enabled

	docs, err := rdr.ReadFromReader("sheet", strings.NewReader(data))
	if err != nil {
		t.Fatalf("chunking read error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one chunked document")
	}

	// Test helper methods.
	txt := rdr.csvToText("foo,bar")
	if txt != "foo | bar" {
		t.Errorf("csvToText unexpected output: %s", txt)
	}

	name := rdr.extractFileNameFromURL("https://example.com/path/data.csv?version=1#top")
	if name != "data" {
		t.Errorf("extractFileNameFromURL got %s", name)
	}
}

// mockChunker implements chunking.Strategy returning doc unchanged to test custom strategy path.
type mockChunker struct{}

func (m mockChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return []*document.Document{doc}, nil
}

func TestCSVReader_ReadFromFileAndURL(t *testing.T) {
	data := "col1,col2\nfoo,bar\n"

	tmp, err := os.CreateTemp(t.TempDir(), "*.csv")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	_, _ = tmp.WriteString(data)
	tmp.Close()

	rdr := New(
		WithChunkingStrategy(mockChunker{}), // provided, ensures branch taken
	)

	// Test ReadFromFile
	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil || len(docs) != 1 {
		t.Fatalf("ReadFromFile error: %v len=%d", err, len(docs))
	}

	// Test ReadFromURL using httptest server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(data))
	}))
	defer srv.Close()

	docsURL, err := rdr.ReadFromURL(srv.URL + "/sample.csv")
	if err != nil || len(docsURL) != 1 {
		t.Fatalf("ReadFromURL error: %v len=%d", err, len(docsURL))
	}
	if docsURL[0].Name != "sample" {
		t.Errorf("expected extracted name 'sample', got %s", docsURL[0].Name)
	}
}
