package markdown

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestMarkdownReader_ReadFromReader_NoChunk(t *testing.T) {
	data := "# Title\n\nThis is **markdown**."

	rdr := New(
		WithChunking(false),
	)

	docs, err := rdr.ReadFromReader("doc", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if !strings.Contains(docs[0].Content, "# Title") {
		t.Errorf("content mismatch")
	}
	if rdr.Name() != "MarkdownReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestMarkdownReader_FileAndURL(t *testing.T) {
	data := "## H2 Heading"

	tmp, _ := os.CreateTemp(t.TempDir(), "*.md")
	tmp.WriteString(data)
	tmp.Close()

	rdr := New()

	d1, _ := rdr.ReadFromFile(tmp.Name())
	if len(d1) != 1 {
		t.Fatalf("file read len %d", len(d1))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(data)) }))
	defer srv.Close()

	d2, _ := rdr.ReadFromURL(srv.URL + "/m.md")
	if d2[0].Name != "m" {
		t.Fatalf("expected name m got %s", d2[0].Name)
	}
}

type failChunk struct{}

func (failChunk) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("chunking fail")
}

func TestMarkdownReader_ChunkError(t *testing.T) {
	rdr := New(WithChunkingStrategy(failChunk{}))
	_, err := rdr.ReadFromReader("n", strings.NewReader("content"))
	if err == nil {
		t.Fatalf("expected error")
	}
}
