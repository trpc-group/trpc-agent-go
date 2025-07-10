// Package pdf provides PDF document reader implementation.
package pdf

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
)

// Reader reads PDF documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
}

// Option represents a functional option for configuring the PDF reader.
type Option func(*Reader)

// WithChunking enables or disables document chunking.
func WithChunking(chunk bool) Option {
	return func(r *Reader) {
		r.chunk = chunk
	}
}

// WithChunkingStrategy sets the chunking strategy to use.
func WithChunkingStrategy(strategy chunking.Strategy) Option {
	return func(r *Reader) {
		r.chunkingStrategy = strategy
	}
}

// New creates a new PDF reader with the given options.
func New(opts ...Option) *Reader {
	r := &Reader{
		chunk:            true,
		chunkingStrategy: chunking.NewFixedSizeChunking(),
	}
	// Apply options.
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ReadFromReader reads PDF content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, reader io.Reader) ([]*document.Document, error) {
	return r.readFromReader(reader, name)
}

// ReadFromFile reads PDF content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	// Open PDF file.
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Get file name without extension.
	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	return r.readFromReader(file, fileName)
}

// ReadFromURL reads PDF content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(url string) ([]*document.Document, error) {
	// Download PDF from URL.
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Get file name from URL.
	fileName := r.extractFileNameFromURL(url)
	return r.readFromReader(resp.Body, fileName)
}

// readFromReader reads PDF content from an io.Reader and returns a list of documents.
func (r *Reader) readFromReader(reader io.Reader, name string) ([]*document.Document, error) {
	readerAt, size, err := toReaderAt(reader)
	if err != nil {
		return nil, err
	}
	pdfReader, err := pdf.NewReader(readerAt, size)
	if err != nil {
		return nil, err
	}

	var allText strings.Builder
	totalPage := pdfReader.NumPage()
	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		page := pdfReader.Page(pageIndex)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		allText.WriteString(text)
		allText.WriteString("\n")
	}
	doc := idocument.CreateDocument(allText.String(), name)
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

func toReaderAt(r io.Reader) (io.ReaderAt, int64, error) {
	// If the reader is already an io.ReaderAt and io.ReadSeeker (like an *os.File),
	// we can get its size and use it directly without buffering.
	if ra, ok := r.(io.ReaderAt); ok {
		if rs, ok := r.(io.ReadSeeker); ok {
			size, err := getReaderSize(rs)
			if err != nil {
				return nil, 0, err
			}
			return ra, size, nil
		}
	}

	// For other readers (like network streams), we must buffer the whole
	// content into memory to make it seekable and get its size.
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	return bytes.NewReader(data), int64(len(data)), nil
}

// chunkDocument applies chunking to a document.
func (r *Reader) chunkDocument(doc *document.Document) ([]*document.Document, error) {
	if r.chunkingStrategy == nil {
		r.chunkingStrategy = chunking.NewFixedSizeChunking()
	}
	return r.chunkingStrategy.Chunk(doc)
}

// extractFileNameFromURL extracts a file name from a URL.
func (r *Reader) extractFileNameFromURL(url string) string {
	// Extract the last part of the URL as the file name.
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		fileName := parts[len(parts)-1]
		// Remove query parameters and fragments.
		if idx := strings.Index(fileName, "?"); idx != -1 {
			fileName = fileName[:idx]
		}
		if idx := strings.Index(fileName, "#"); idx != -1 {
			fileName = fileName[:idx]
		}
		// Remove file extension.
		fileName = strings.TrimSuffix(fileName, ".pdf")
		return fileName
	}
	return "pdf_document"
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "PDFReader"
}

// getReaderSize returns the total size of an io.ReadSeeker without altering
// its current position.
func getReaderSize(rs io.ReadSeeker) (int64, error) {
	current, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	end, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	_, err = rs.Seek(current, io.SeekStart)
	if err != nil {
		return 0, err
	}
	return end, nil
}
