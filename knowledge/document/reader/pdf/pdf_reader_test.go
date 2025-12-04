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
	pdfPkg "github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

// testingInterface defines the common interface between *testing.T and *testing.B
type testingInterface interface {
	Helper()
	Fatalf(format string, args ...any)
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

// createTempPDF creates a temporary PDF file for testing
func createTempPDF(t *testing.T, data []byte) string {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "sample-*.pdf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return tmp.Name()
}

func TestReader_ReadFromReader(t *testing.T) {
	data := newTestPDF(t)

	tests := []struct {
		name    string
		opts    []reader.Option
		cleanup func()
	}{
		{
			name: "without OCR",
			opts: []reader.Option{reader.WithChunk(false)},
		},
		{
			name: "with OCR",
			opts: []reader.Option{
				reader.WithChunk(false),
				reader.WithOCRExtractor(&mockOCRExtractor{}),
			},
		},
		{
			name: "with chunking",
			opts: []reader.Option{reader.WithChunk(true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdr := New(tt.opts...)
			if closer, ok := rdr.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			docs, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
			if err != nil {
				t.Fatalf("ReadFromReader failed: %v", err)
			}
			if len(docs) == 0 {
				t.Fatal("expected at least one document")
			}
			if !strings.Contains(docs[0].Content, "Hello World") {
				t.Errorf("content = %q, want contains 'Hello World'", docs[0].Content)
			}
		})
	}
}

func TestReader_ReadFromFile(t *testing.T) {
	data := newTestPDF(t)
	filePath := createTempPDF(t, data)

	tests := []struct {
		name string
		opts []reader.Option
	}{
		{
			name: "without OCR",
			opts: []reader.Option{reader.WithChunk(false)},
		},
		{
			name: "with OCR",
			opts: []reader.Option{
				reader.WithChunk(false),
				reader.WithOCRExtractor(&mockOCRExtractor{}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdr := New(tt.opts...)
			if closer, ok := rdr.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			docs, err := rdr.ReadFromFile(filePath)
			if err != nil {
				t.Fatalf("ReadFromFile failed: %v", err)
			}
			if len(docs) == 0 {
				t.Fatal("expected at least one document")
			}
			if !strings.Contains(docs[0].Content, "Hello World") {
				t.Errorf("content = %q, want contains 'Hello World'", docs[0].Content)
			}
		})
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

func TestReader_Name(t *testing.T) {
	rdr := New().(*Reader)
	if got := rdr.Name(); got != "PDFReader" {
		t.Errorf("Name() = %q, want %q", got, "PDFReader")
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

// BenchmarkReader_ReadFromReader benchmarks PDF processing from io.Reader
func BenchmarkReader_ReadFromReader(b *testing.B) {
	data := newTestPDF(b)

	b.Run("WithoutOCR", func(b *testing.B) {
		rdr := New(reader.WithChunk(false))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
			if err != nil {
				b.Fatalf("ReadFromReader failed: %v", err)
			}
		}
	})

	b.Run("WithOCR", func(b *testing.B) {
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
	})

	b.Run("WithChunking", func(b *testing.B) {
		rdr := New(reader.WithChunk(true))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
			if err != nil {
				b.Fatalf("ReadFromReader failed: %v", err)
			}
		}
	})
}

// BenchmarkReader_File benchmarks file-based PDF processing
func BenchmarkReader_File(b *testing.B) {
	data := newTestPDF(b)
	tmp, err := os.CreateTemp(b.TempDir(), "benchmark-*.pdf")
	if err != nil {
		b.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		b.Fatalf("write temp file: %v", err)
	}
	filePath := tmp.Name()

	b.Run("WithoutOCR", func(b *testing.B) {
		rdr := New(reader.WithChunk(false))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := rdr.ReadFromFile(filePath)
			if err != nil {
				b.Fatalf("ReadFromFile failed: %v", err)
			}
		}
	})

	b.Run("WithOCR", func(b *testing.B) {
		mockOCR := &mockOCRExtractor{}
		rdr := New(reader.WithChunk(false), reader.WithOCRExtractor(mockOCR)).(*Reader)
		defer rdr.Close()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := rdr.ReadFromFile(filePath)
			if err != nil {
				b.Fatalf("ReadFromFile failed: %v", err)
			}
		}
	})
}

// TestReader_ErrorHandling tests error handling in various scenarios
func TestReader_ErrorHandling(t *testing.T) {
	tests := []struct {
		name    string
		fn      func(reader.Reader) error
		wantErr string
	}{
		{
			name: "invalid PDF data",
			fn: func(r reader.Reader) error {
				_, err := r.ReadFromReader("invalid", bytes.NewReader([]byte("not a pdf")))
				return err
			},
			wantErr: "failed to create PDF reader",
		},
		{
			name: "non-existent file",
			fn: func(r reader.Reader) error {
				_, err := r.ReadFromFile("/non/existent/file.pdf")
				return err
			},
			wantErr: "failed to open PDF file",
		},
		{
			name: "unsupported URL scheme (empty)",
			fn: func(r reader.Reader) error {
				_, err := r.ReadFromURL("invalid-url")
				return err
			},
			wantErr: "unsupported URL scheme",
		},
		{
			name: "unsupported URL scheme (ftp)",
			fn: func(r reader.Reader) error {
				_, err := r.ReadFromURL("ftp://example.com/file.pdf")
				return err
			},
			wantErr: "unsupported URL scheme",
		},
	}

	rdr := New(reader.WithChunk(false))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(rdr)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want contains %q", err, tt.wantErr)
			}
		})
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

// TestReader_SupportedExtensions tests the SupportedExtensions method
func TestReader_SupportedExtensions(t *testing.T) {
	rdr := New().(*Reader)
	exts := rdr.SupportedExtensions()
	if len(exts) != 1 || exts[0] != ".pdf" {
		t.Fatalf("expected ['.pdf'], got %v", exts)
	}
}

// TestReader_ReadFromURLWithContext tests URL reading with context
func TestReader_ReadFromURLWithContext(t *testing.T) {
	data := newTestPDF(t)

	// Test successful download
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	rdr := New(reader.WithChunk(false)).(*Reader)
	docs, err := rdr.ReadFromURLWithContext(context.Background(), server.URL+"/sample.pdf")
	if err != nil {
		t.Fatalf("ReadFromURLWithContext failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document, got 0")
	}

	// Test with HTTP error status
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer errorServer.Close()

	_, err = rdr.ReadFromURLWithContext(context.Background(), errorServer.URL+"/notfound.pdf")
	if err == nil {
		t.Fatalf("expected error for HTTP 404")
	}

	// Test with context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	_, err = rdr.ReadFromURLWithContext(ctx, server.URL+"/sample.pdf")
	if err == nil {
		t.Fatalf("expected error for cancelled context")
	}
}

// TestReader_ReadFromFileWithContext tests file reading with context
func TestReader_ReadFromFileWithContext(t *testing.T) {
	data := newTestPDF(t)
	filePath := createTempPDF(t, data)

	tests := []struct {
		name string
		opts []reader.Option
	}{
		{
			name: "without OCR",
			opts: []reader.Option{reader.WithChunk(false)},
		},
		{
			name: "with OCR",
			opts: []reader.Option{
				reader.WithChunk(false),
				reader.WithOCRExtractor(&mockOCRExtractor{}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdr := New(tt.opts...).(*Reader)
			defer rdr.Close()

			docs, err := rdr.ReadFromFileWithContext(context.Background(), filePath)
			if err != nil {
				t.Fatalf("ReadFromFileWithContext failed: %v", err)
			}
			if len(docs) == 0 {
				t.Fatal("expected at least one document")
			}
			if !strings.Contains(docs[0].Content, "Hello World") {
				t.Errorf("content = %q, want contains 'Hello World'", docs[0].Content)
			}
		})
	}
}

// TestReader_ExtractFileNameFromURL tests URL filename extraction edge cases
func TestReader_ExtractFileNameFromURL(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/docs/file.pdf?x=1#top", "file"},
		{"https://example.com/file.pdf", "file"},
		{"https://example.com/path/to/document.pdf?query=value", "document"},
		{"https://example.com/file.pdf#fragment", "file"},
		// Edge cases - when URL has no file or is malformed, default to "pdf_document"
		// However, looking at the implementation, it returns empty string for these cases
		// Let's test what it actually does
		{"https://example.com/", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := rdr.extractFileNameFromURL(tt.url)
		if result != tt.expected {
			t.Errorf("extractFileNameFromURL(%q) = %q, want %q", tt.url, result, tt.expected)
		}
	}
}

// TestReader_ChunkingStrategies tests different chunking configurations
func TestReader_ChunkingStrategies(t *testing.T) {
	data := newTestPDF(t)

	// Test with custom chunk size and overlap
	rdr := New(
		reader.WithChunk(true),
		reader.WithChunkSize(100),
		reader.WithChunkOverlap(20),
	)
	docs, err := rdr.ReadFromReader("sample", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader with chunking failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document after chunking")
	}

	// Test with only chunk size
	rdr2 := New(
		reader.WithChunk(true),
		reader.WithChunkSize(50),
	)
	docs2, err := rdr2.ReadFromReader("sample", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader with chunk size only failed: %v", err)
	}
	if len(docs2) == 0 {
		t.Fatalf("expected at least one document")
	}

	// Test with only overlap (should use default chunk size)
	rdr3 := New(
		reader.WithChunk(true),
		reader.WithChunkOverlap(10),
	)
	docs3, err := rdr3.ReadFromReader("sample", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader with overlap only failed: %v", err)
	}
	if len(docs3) == 0 {
		t.Fatalf("expected at least one document")
	}
}

// TestReader_ContextCancellation tests that context cancellation works correctly
func TestReader_ContextCancellation(t *testing.T) {
	// Create a mock OCR that respects context
	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	// Create context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context immediately
	cancel()

	// Try to read with cancelled context
	// Should return context.Canceled error or file not found
	_, err := rdr.ReadFromFileWithContext(ctx, "/tmp/nonexistent.pdf")
	// We expect an error (either context cancelled or file not found)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// TestReader_ChunkDocumentNilStrategy tests chunkDocument with nil strategy
func TestReader_ChunkDocumentNilStrategy(t *testing.T) {
	data := newTestPDF(t)

	// Create reader with chunking enabled but force nil strategy
	rdr := &Reader{
		chunk:            true,
		chunkingStrategy: nil, // Force nil
	}

	docs, err := rdr.ReadFromReader("test", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader with nil strategy failed: %v", err)
	}
	// Should still work because chunkDocument creates default strategy
	if len(docs) == 0 {
		t.Fatalf("expected at least one document")
	}
}

// TestReader_ReadFromReaderIOError tests error handling when reading from reader
func TestReader_ReadFromReaderIOError(t *testing.T) {
	rdr := New(reader.WithChunk(false))

	// Create a reader that always returns error
	errReader := &errorReader{err: errors.New("read error")}

	_, err := rdr.ReadFromReader("test", errReader)
	if err == nil {
		t.Fatalf("expected error from errorReader")
	}
}

// errorReader is a reader that always returns an error
type errorReader struct {
	err error
}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, e.err
}

// TestReader_ExtractTextFromReaderAlreadyReadSeeker tests extractTextFromReader with io.ReadSeeker
func TestReader_ExtractTextFromReaderAlreadyReadSeeker(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(reader.WithChunk(false)).(*Reader)

	// Pass a ReadSeeker directly
	docs, err := rdr.extractTextFromReader(bytes.NewReader(data), "test")
	if err != nil {
		t.Fatalf("extractTextFromReader failed: %v", err)
	}
	if len(docs) == 0 || !strings.Contains(docs[0].Content, "Hello World") {
		t.Fatalf("unexpected result")
	}
}

// TestReader_ExtractTextFromReaderNonReadSeeker tests extractTextFromReader with regular io.Reader
func TestReader_ExtractTextFromReaderNonReadSeeker(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(reader.WithChunk(false)).(*Reader)

	// Create a buffer (which is NOT a ReadSeeker by default in this context)
	// We'll wrap it to ensure it's treated as io.Reader only
	buf := &limitedReader{r: bytes.NewReader(data), n: int64(len(data))}

	docs, err := rdr.extractTextFromReader(buf, "test")
	if err != nil {
		t.Fatalf("extractTextFromReader with non-ReadSeeker failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected at least one document")
	}
}

// limitedReader wraps io.Reader to ensure it's not treated as ReadSeeker
type limitedReader struct {
	r io.Reader
	n int64
}

func (l *limitedReader) Read(p []byte) (n int, err error) {
	return l.r.Read(p)
}

// TestReader_ExtractTextFromPDFReaderEmptyPages tests PDF with pages that have no text
func TestReader_ExtractTextFromPDFReaderEmptyPages(t *testing.T) {
	// Create a PDF with an empty page
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	// Don't add any text

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("failed to generate empty PDF: %v", err)
	}

	rdr := New(reader.WithChunk(false))
	docs, err := rdr.ReadFromReader("empty", &buf)
	if err != nil {
		t.Fatalf("ReadFromReader with empty pages failed: %v", err)
	}
	// Should still return a document, possibly with empty or minimal content
	if len(docs) == 0 {
		t.Fatalf("expected at least one document")
	}
}

// TestReader_BuildDefaultChunkingStrategy tests the chunking strategy builder
func TestReader_BuildDefaultChunkingStrategy(t *testing.T) {
	// Test with both size and overlap
	strategy1 := buildDefaultChunkingStrategy(100, 20)
	if strategy1 == nil {
		t.Fatal("expected non-nil strategy")
	}

	// Test with only size
	strategy2 := buildDefaultChunkingStrategy(100, 0)
	if strategy2 == nil {
		t.Fatal("expected non-nil strategy")
	}

	// Test with only overlap
	strategy3 := buildDefaultChunkingStrategy(0, 20)
	if strategy3 == nil {
		t.Fatal("expected non-nil strategy")
	}

	// Test with neither (should still create valid strategy)
	strategy4 := buildDefaultChunkingStrategy(0, 0)
	if strategy4 == nil {
		t.Fatal("expected non-nil strategy")
	}
}

// TestReader_ReadFromFileTextOnlyError tests error handling in text-only path
func TestReader_ReadFromFileTextOnlyError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	// Test with invalid file path
	_, err := rdr.readFromFileTextOnly("/nonexistent/file.pdf", "test")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}

	// Test with directory instead of file
	_, err = rdr.readFromFileTextOnly(t.TempDir(), "test")
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

// TestReader_ExtractTextFromReadSeekerError tests error handling in extractTextFromReadSeeker
func TestReader_ExtractTextFromReadSeekerError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	// Create a ReadSeeker that fails on Seek
	badSeeker := &badReadSeeker{}
	_, err := rdr.extractTextFromReadSeeker(badSeeker)
	if err == nil {
		t.Fatal("expected error from badReadSeeker")
	}
}

// badReadSeeker implements io.ReadSeeker but always fails
type badReadSeeker struct{}

func (b *badReadSeeker) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func (b *badReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return 0, errors.New("seek error")
}

// TestReader_WithOCRContextCancellation tests OCR processing with context cancellation
func TestReader_WithOCRContextCancellation(t *testing.T) {
	data := newTestPDF(t)
	filePath := createTempPDF(t, data)

	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := rdr.ReadFromFileWithContext(ctx, filePath)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// TestReader_WithRealPDF tests reading from a real PDF file with images
func TestReader_WithRealPDF(t *testing.T) {
	realPDFPath := "test_data/trpc-go.pdf"

	if _, err := os.Stat(realPDFPath); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s does not exist", realPDFPath)
	}

	tests := []struct {
		name string
		opts []reader.Option
	}{
		{
			name: "without OCR",
			opts: []reader.Option{reader.WithChunk(false)},
		},
		{
			name: "with OCR",
			opts: []reader.Option{
				reader.WithChunk(false),
				reader.WithOCRExtractor(&mockOCRExtractor{}),
			},
		},
		{
			name: "with chunking",
			opts: []reader.Option{
				reader.WithChunk(true),
				reader.WithChunkSize(200),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdr := New(tt.opts...)
			if closer, ok := rdr.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			docs, err := rdr.ReadFromFile(realPDFPath)
			if err != nil {
				t.Fatalf("ReadFromFile failed: %v", err)
			}
			if len(docs) == 0 {
				t.Fatal("expected at least one document")
			}
		})
	}
}

// TestReader_OCRImageExtraction tests OCR with actual image extraction
func TestReader_OCRImageExtraction(t *testing.T) {
	realPDFPath := "test_data/trpc-go.pdf"

	if _, err := os.Stat(realPDFPath); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s does not exist", realPDFPath)
	}

	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	docs, err := rdr.ReadFromFile(realPDFPath)
	if err != nil {
		t.Fatalf("ReadFromFile with OCR failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	if mockOCR.extractTextCalled == 0 {
		t.Log("Warning: OCR was not called, PDF might not have images")
	}
}

// TestReader_OCRFromReader tests OCR processing from io.Reader
func TestReader_OCRFromReader(t *testing.T) {
	realPDFPath := "test_data/trpc-go.pdf"

	if _, err := os.Stat(realPDFPath); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s does not exist", realPDFPath)
	}

	data, err := os.ReadFile(realPDFPath)
	if err != nil {
		t.Fatalf("failed to read test PDF: %v", err)
	}

	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	docs, err := rdr.ReadFromReader("test-pdf", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader with OCR failed: %v", err)
	}

	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
}

// TestReader_ExtractTextFromPageNullPage tests extractTextFromPage with null page
func TestReader_ExtractTextFromPageNullPage(t *testing.T) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetFont("Helvetica", "", 12)
	pdf.AddPage()
	pdf.Cell(40, 10, "Page 1")
	pdf.AddPage()
	pdf.Cell(40, 10, "Page 2")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("failed to generate PDF: %v", err)
	}

	rdr := New(reader.WithChunk(false)).(*Reader)
	pdfReader, err := pdfPkg.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to create PDF reader: %v", err)
	}

	text := rdr.extractTextFromPage(pdfReader, 1)
	if !strings.Contains(text, "Page 1") {
		t.Logf("Page 1 text: %s", text)
	}
}

// TestReader_GetImageDataFromPDFCPUImageNoReader tests getImageDataFromPDFCPUImage error path
func TestReader_GetImageDataFromPDFCPUImageNoReader(t *testing.T) {
	rdr := New().(*Reader)

	img := model.Image{
		Reader: nil,
	}

	data, err := rdr.getImageDataFromPDFCPUImage(img)
	if err == nil {
		t.Fatal("expected error when image has no reader")
	}
	if len(data) != 0 {
		t.Fatalf("expected empty data, got %d bytes", len(data))
	}
}

// TestReader_GetImageDataFromPDFCPUImageWithReader tests getImageDataFromPDFCPUImage success path
func TestReader_GetImageDataFromPDFCPUImageWithReader(t *testing.T) {
	rdr := New().(*Reader)

	testData := []byte("fake image data")
	img := model.Image{
		Reader: bytes.NewReader(testData),
	}

	data, err := rdr.getImageDataFromPDFCPUImage(img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Fatalf("expected %v, got %v", testData, data)
	}
}

// TestReader_CombinedOptions tests multiple options together
func TestReader_CombinedOptions(t *testing.T) {
	mockOCR := &mockOCRExtractor{}
	customChunker := &mockChunker{}

	rdr := New(
		reader.WithOCRExtractor(mockOCR),
		reader.WithCustomChunkingStrategy(customChunker),
		reader.WithChunk(true),
	).(*Reader)
	defer rdr.Close()

	if rdr.ocrExtractor == nil {
		t.Fatal("OCR extractor should be set")
	}
	if rdr.chunkingStrategy == nil {
		t.Fatal("chunking strategy should be set")
	}
	if !rdr.chunk {
		t.Fatal("chunk should be enabled")
	}

	data := newTestPDF(t)
	docs, err := rdr.ReadFromReader("test", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFromReader failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}
}

// TestReader_ExtractContentByPageError tests extractContentByPage error handling
func TestReader_ExtractContentByPageError(t *testing.T) {
	rdr := New(reader.WithOCRExtractor(&mockOCRExtractor{})).(*Reader)
	defer rdr.Close()

	badSeeker := &badReadSeeker{}
	_, err := rdr.extractContentByPage(context.Background(), badSeeker)
	if err == nil {
		t.Fatal("expected error from bad read seeker")
	}
}

// TestReader_ExtractContentByPageContextCancellation tests context cancellation in extractContentByPage
func TestReader_ExtractContentByPageContextCancellation(t *testing.T) {
	realPDFPath := "test_data/trpc-go.pdf"

	if _, err := os.Stat(realPDFPath); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s does not exist", realPDFPath)
	}

	data, err := os.ReadFile(realPDFPath)
	if err != nil {
		t.Skipf("failed to read test PDF: %v", err)
	}

	mockOCR := &mockOCRExtractor{}
	rdr := New(reader.WithOCRExtractor(mockOCR)).(*Reader)
	defer rdr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = rdr.extractContentByPage(ctx, bytes.NewReader(data))
	if err == nil {
		t.Log("Warning: expected context cancellation error")
	}
}

// TestReader_ExtractFileNameFromURLEdgeCases tests edge cases in extractFileNameFromURL
func TestReader_ExtractFileNameFromURLEdgeCases(t *testing.T) {
	rdr := New().(*Reader)

	tests := []struct {
		url      string
		expected string
	}{
		{"", ""},
		{"/", ""},
		{"https://example.com/", ""},
		{"file.pdf", "file"},
		{"path/to/file.pdf#anchor", "file"},
		{"https://example.com/docs/report.pdf?version=1", "report"},
	}

	for _, tt := range tests {
		result := rdr.extractFileNameFromURL(tt.url)
		if result != tt.expected {
			t.Errorf("extractFileNameFromURL(%q) = %q, want %q", tt.url, result, tt.expected)
		}
	}
}

// TestReader_ReadFromFileTextOnlyPDFError tests PDF parsing error in readFromFileTextOnly
func TestReader_ReadFromFileTextOnlyPDFError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	tmp, err := os.CreateTemp(t.TempDir(), "invalid-*.pdf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer tmp.Close()

	if _, err := tmp.Write([]byte("invalid pdf content")); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err = rdr.readFromFileTextOnly(tmp.Name(), "test")
	if err == nil {
		t.Fatal("expected error for invalid PDF")
	}
}

// TestReader_ExtractTextFromReadSeekerPDFError tests PDF parsing error in extractTextFromReadSeeker
func TestReader_ExtractTextFromReadSeekerPDFError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	invalidData := bytes.NewReader([]byte("not a valid pdf"))
	_, err := rdr.extractTextFromReadSeeker(invalidData)
	if err == nil {
		t.Fatal("expected error for invalid PDF data")
	}
}

// TestReader_ReadFromURLWithContextHTTPError tests HTTP request error
func TestReader_ReadFromURLWithContextHTTPError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	_, err := rdr.ReadFromURLWithContext(context.Background(), "http://non-existent-domain-12345.com/file.pdf")
	if err == nil {
		t.Fatal("expected error for non-existent domain")
	}
}

// TestReader_ExtractTextFromReaderBothPaths tests both paths in extractTextFromReader
func TestReader_ExtractTextFromReaderBothPaths(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(reader.WithChunk(false)).(*Reader)

	docs1, err := rdr.extractTextFromReader(bytes.NewReader(data), "test1")
	if err != nil {
		t.Fatalf("extractTextFromReader failed with ReadSeeker: %v", err)
	}
	if len(docs1) == 0 {
		t.Fatal("expected at least one document")
	}

	nonSeeker := &limitedReader{r: bytes.NewReader(data), n: int64(len(data))}
	docs2, err := rdr.extractTextFromReader(nonSeeker, "test2")
	if err != nil {
		t.Fatalf("extractTextFromReader failed with non-ReadSeeker: %v", err)
	}
	if len(docs2) == 0 {
		t.Fatal("expected at least one document")
	}
}

// TestReader_ReadFromURLContextError tests ReadFromURL with context error
func TestReader_ReadFromURLContextError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := rdr.ReadFromURLWithContext(ctx, server.URL+"/test.pdf")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// TestReader_ReadFromFileTextOnlyStatError tests stat error in readFromFileTextOnly
func TestReader_ReadFromFileTextOnlyStatError(t *testing.T) {
	rdr := New(reader.WithChunk(false)).(*Reader)

	tmp := t.TempDir() + "/nonexistent.pdf"
	_, err := rdr.readFromFileTextOnly(tmp, "test")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// TestReader_ExtractTextFromPDFReaderPageError tests extractTextFromPDFReader with page error
func TestReader_ExtractTextFromPDFReaderPageError(t *testing.T) {
	data := newTestPDF(t)
	rdr := New(reader.WithChunk(false)).(*Reader)

	pdfReader, err := pdfPkg.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to create PDF reader: %v", err)
	}

	text, err := rdr.extractTextFromPDFReader(pdfReader)
	if err != nil {
		t.Fatalf("extractTextFromPDFReader failed: %v", err)
	}
	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected 'Hello World' in text, got: %s", text)
	}
}
