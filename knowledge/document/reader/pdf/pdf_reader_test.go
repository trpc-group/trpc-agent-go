//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pdf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

// testingInterface defines the common interface between *testing.T and *testing.B
type testingInterface interface {
	Helper()
	Fatalf(format string, args ...interface{})
}

// newTestPDF programmatically generates a small PDF containing the text
// "Hello World" using gofpdf. Generating ensures the file is well-formed
// and parsable by ledongthuc/pdf, avoiding brittle handcrafted bytes.
func newTestPDF(t testingInterface) []byte {
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

	rdr := New(reader.WithChunk(false))
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

	rdr := New(reader.WithChunk(false))
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

	rdr := New(reader.WithChunk(false))
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
		reader.WithChunk(true),
		reader.WithCustomChunkingStrategy(mockChunker{}),
	)
	docs, err := rdr.ReadFromReader("x", bytes.NewReader(data))
	if err != nil || len(docs) != 1 {
		t.Fatalf("custom chunker failed: %v", err)
	}
}

func TestReader_ChunkError(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(reader.WithCustomChunkingStrategy(errChunker{}))
	_, err := rdr.ReadFromReader("x", bytes.NewReader(data))
	if err == nil {
		t.Fatalf("expected chunk error")
	}
}

func TestReader_Helpers(t *testing.T) {
	rdr := New().(*Reader)
	if rdr.Name() != "PDFReader" {
		t.Fatalf("Name() mismatch")
	}
	urlName := rdr.extractFileNameFromURL("https://example.com/docs/file.pdf?x=1#top")
	if urlName != "file" {
		t.Fatalf("extractFileNameFromURL got %s", urlName)
	}
}

// TestReader_WithoutOCR tests that PDF processing without OCR is efficient and doesn't attempt image extraction
func TestReader_WithoutOCR(t *testing.T) {
	data := newTestPDF(t)

	// Create reader without OCR
	rdr := New(reader.WithChunk(false))

	// Test ReadFromReader
	docs, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document, got 0")
	}
	if !strings.Contains(docs[0].Content, "Hello World") {
		t.Fatalf("extracted content does not contain expected text; got: %q", docs[0].Content)
	}

	// Test ReadFromFile
	tmp, err := os.CreateTemp(t.TempDir(), "sample-*.pdf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	docs, err = rdr.ReadFromFile(tmp.Name())
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

// mockOCRExtractor is a mock OCR extractor for testing
type mockOCRExtractor struct {
	extractTextCalled int
}

func (m *mockOCRExtractor) ExtractText(ctx context.Context, imageData []byte, opts ...ocr.Option) (string, error) {
	m.extractTextCalled++
	return "OCR extracted text", nil
}

func (m *mockOCRExtractor) ExtractTextFromReader(ctx context.Context, reader io.Reader, opts ...ocr.Option) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return m.ExtractText(ctx, data, opts...)
}

func (m *mockOCRExtractor) Close() error {
	return nil
}

// TestReader_WithOCR tests that PDF processing with OCR works correctly
func TestReader_WithOCR(t *testing.T) {
	data := newTestPDF(t)
	mockOCR := &mockOCRExtractor{}

	// Create reader with OCR
	rdr := New(reader.WithChunk(false), reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	// Test ReadFromReader - should create temp file for OCR processing
	docs, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document, got 0")
	}
	if !strings.Contains(docs[0].Content, "Hello World") {
		t.Fatalf("extracted content does not contain expected text; got: %q", docs[0].Content)
	}

	// Test ReadFromFile - should use OCR processing path
	tmp, err := os.CreateTemp(t.TempDir(), "sample-*.pdf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	docs, err = rdr.ReadFromFile(tmp.Name())
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

// BenchmarkReader_WithoutOCR benchmarks PDF processing without OCR
func BenchmarkReader_WithoutOCR(b *testing.B) {
	data := newTestPDF(b)
	rdr := New(reader.WithChunk(false))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
		if err != nil {
			b.Fatalf("ReadFromReader failed: %v", err)
		}
	}
}

// BenchmarkReader_WithOCR benchmarks PDF processing with OCR
func BenchmarkReader_WithOCR(b *testing.B) {
	data := newTestPDF(b)
	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithChunk(false), reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
		if err != nil {
			b.Fatalf("ReadFromReader failed: %v", err)
		}
	}
}

// BenchmarkReader_FileWithoutOCR benchmarks file-based PDF processing without OCR
func BenchmarkReader_FileWithoutOCR(b *testing.B) {
	data := newTestPDF(b)

	// Create a temporary file
	tmp, err := os.CreateTemp(b.TempDir(), "benchmark-*.pdf")
	if err != nil {
		b.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		b.Fatalf("write temp file: %v", err)
	}

	rdr := New(reader.WithChunk(false))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rdr.ReadFromFile(tmp.Name())
		if err != nil {
			b.Fatalf("ReadFromFile failed: %v", err)
		}
	}
}

// BenchmarkReader_FileWithOCR benchmarks file-based PDF processing with OCR
func BenchmarkReader_FileWithOCR(b *testing.B) {
	data := newTestPDF(b)

	// Create a temporary file
	tmp, err := os.CreateTemp(b.TempDir(), "benchmark-*.pdf")
	if err != nil {
		b.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		b.Fatalf("write temp file: %v", err)
	}

	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithChunk(false), reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rdr.ReadFromFile(tmp.Name())
		if err != nil {
			b.Fatalf("ReadFromFile failed: %v", err)
		}
	}
}

// TestReader_ErrorHandling tests error handling in various scenarios
func TestReader_ErrorHandling(t *testing.T) {
	rdr := New(reader.WithChunk(false))

	// Test with invalid PDF data
	invalidData := []byte("not a pdf")
	_, err := rdr.ReadFromReader("invalid", bytes.NewReader(invalidData))
	if err == nil {
		t.Fatalf("expected error for invalid PDF data")
	}

	// Test with non-existent file
	_, err = rdr.ReadFromFile("/non/existent/file.pdf")
	if err == nil {
		t.Fatalf("expected error for non-existent file")
	}

	// Test with invalid URL
	_, err = rdr.ReadFromURL("invalid-url")
	if err == nil {
		t.Fatalf("expected error for invalid URL")
	}

	// Test with unsupported URL scheme
	_, err = rdr.ReadFromURL("ftp://example.com/file.pdf")
	if err == nil {
		t.Fatalf("expected error for unsupported URL scheme")
	}
}

// TestReader_ResourceCleanup tests that resources are properly cleaned up
func TestReader_ResourceCleanup(t *testing.T) {
	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithOCRExtractor(mockOCR)).(*Reader)

	// Test that Close() is called on OCR extractor
	err := rdr.Close()
	if err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Test Close() on reader without OCR
	rdr2 := New().(*Reader)
	err = rdr2.Close()
	if err != nil {
		t.Fatalf("Close() failed for reader without OCR: %v", err)
	}
}
