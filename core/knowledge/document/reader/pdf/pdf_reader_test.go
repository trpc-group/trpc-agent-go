package pdf

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

// newTestPDF programmatically generates a small PDF containing the text
// "Hello World" using gofpdf. Generating ensures the file is well-formed
// and parsable by ledongthuc/pdf, avoiding brittle handcrafted bytes.
func newTestPDF(t *testing.T) []byte {
	t.Helper()

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetFont("Helvetica", "", 12)
	pdf.AddPage()
	pdf.Cell(40, 10, "Hello World")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("failed to generate test PDF: %v", err)
	}
	return buf.Bytes()
}

func TestReader_ReadFromReader(t *testing.T) {
	data := newTestPDF(t)
	r := bytes.NewReader(data)

	rdr := New(WithChunking(false))
	docs, err := rdr.ReadFromReader("sample", r)
	if err != nil {
		t.Fatalf("ReadFromReader failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document, got 0")
	}
	if !strings.Contains(docs[0].Content, "Hello World") {
		t.Fatalf("extracted content does not contain expected text; got: %q", docs[0].Content)
	}
}

func TestReader_ReadFromFile(t *testing.T) {
	data := newTestPDF(t)

	tmp, err := os.CreateTemp(t.TempDir(), "sample-*.pdf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	rdr := New(WithChunking(false))
	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil {
		t.Fatalf("ReadFromFile failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document, got 0")
	}
	if !strings.Contains(docs[0].Content, "Hello World") {
		t.Fatalf("extracted content does not contain expected text; got: %q", docs[0].Content)
	}
}
