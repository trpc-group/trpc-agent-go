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

package pdf

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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

// mockChunker returns a single chunk without modification.
type mockChunker struct{}

func (mockChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return []*document.Document{doc}, nil
}

// errChunker always fails, used to exercise error path.
type errChunker struct{}

func (errChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("chunk err")
}

func TestReader_ReadFromURL(t *testing.T) {
	data := newTestPDF(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	rdr := New(WithChunking(false))
	docs, err := rdr.ReadFromURL(server.URL + "/sample.pdf")
	if err != nil {
		t.Fatalf("ReadFromURL failed: %v", err)
	}
	if docs[0].Name != "sample" {
		t.Fatalf("unexpected extracted name: %s", docs[0].Name)
	}
}

func TestReader_CustomChunker(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(
		WithChunking(true),
		WithChunkingStrategy(mockChunker{}),
	)
	docs, err := rdr.ReadFromReader("x", bytes.NewReader(data))
	if err != nil || len(docs) != 1 {
		t.Fatalf("custom chunker failed: %v", err)
	}
}

func TestReader_ChunkError(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(WithChunkingStrategy(errChunker{}))
	_, err := rdr.ReadFromReader("x", bytes.NewReader(data))
	if err == nil {
		t.Fatalf("expected chunk error")
	}
}

func TestReader_Helpers(t *testing.T) {
	rdr := New()
	if rdr.Name() != "PDFReader" {
		t.Fatalf("Name() mismatch")
	}
	urlName := rdr.extractFileNameFromURL("https://example.com/docs/file.pdf?x=1#top")
	if urlName != "file" {
		t.Fatalf("extractFileNameFromURL got %s", urlName)
	}
}
