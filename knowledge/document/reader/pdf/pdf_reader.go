//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pdf provides PDF document reader implementation.
package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	pdfcpuAPI "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

var (
	// supportedExtensions defines the file extensions supported by this reader.
	supportedExtensions = []string{".pdf"}
)

// init registers the PDF reader with the global registry.
func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads PDF documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
	ocrExtractor     ocr.Extractor
}

// New creates a new PDF reader with the given options.
// PDF reader uses FixedSizeChunking by default.
func New(opts ...reader.Option) reader.Reader {
	// Build config from options
	config := &reader.Config{
		Chunk:        true,
		OCRExtractor: nil,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Build chunking strategy using the default builder for PDF
	strategy := reader.BuildChunkingStrategy(config, buildDefaultChunkingStrategy)

	// Create reader from config
	return &Reader{
		chunk:            config.Chunk,
		chunkingStrategy: strategy,
		ocrExtractor:     config.OCRExtractor,
	}
}

// buildDefaultChunkingStrategy builds the default chunking strategy for PDF reader.
// PDF uses FixedSizeChunking with configurable size and overlap.
func buildDefaultChunkingStrategy(chunkSize, overlap int) chunking.Strategy {
	var opts []chunking.Option
	if chunkSize > 0 {
		opts = append(opts, chunking.WithChunkSize(chunkSize))
	}
	if overlap > 0 {
		opts = append(opts, chunking.WithOverlap(overlap))
	}
	return chunking.NewFixedSizeChunking(opts...)
}

// Close closes the reader and releases OCR resources.
func (r *Reader) Close() error {
	if r.ocrExtractor != nil {
		return r.ocrExtractor.Close()
	}
	return nil
}

// ReadFromReader reads PDF content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, reader io.Reader) ([]*document.Document, error) {
	return r.readFromReaderWithContext(context.Background(), reader, name)
}

// ReadFromFile reads PDF content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	return r.ReadFromFileWithContext(context.Background(), filePath)
}

// ReadFromFileWithContext reads PDF content from a file path with context support.
func (r *Reader) ReadFromFileWithContext(ctx context.Context, filePath string) ([]*document.Document, error) {
	// Get file name without extension
	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	// Choose processing method based on OCR configuration
	if r.ocrExtractor != nil {
		// Process with OCR support (text + image OCR)
		return r.readFromFileWithOCR(ctx, filePath, fileName)
	}

	// Process without OCR (text only, more efficient)
	return r.readFromFileTextOnly(filePath, fileName)
}

// ReadFromURL reads PDF content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	return r.ReadFromURLWithContext(context.Background(), urlStr)
}

// ReadFromURLWithContext reads PDF content from a URL with context support.
func (r *Reader) ReadFromURLWithContext(ctx context.Context, urlStr string) ([]*document.Document, error) {
	// Validate URL before making HTTP request.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Download PDF from URL with timeout
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
	}

	// Get file name from URL.
	fileName := r.extractFileNameFromURL(urlStr)
	return r.readFromReaderWithContext(ctx, resp.Body, fileName)
}

// readFromReaderWithContext reads PDF content from an io.Reader with context support.
func (r *Reader) readFromReaderWithContext(ctx context.Context, reader io.Reader, name string) ([]*document.Document, error) {
	// Read all content to create a ReadSeeker
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF content: %w", err)
	}

	// Create a ReadSeeker from the content (using bytes.NewReader for better memory efficiency)
	readSeeker := bytes.NewReader(content)

	// Choose processing method based on OCR configuration
	if r.ocrExtractor != nil {
		// Process with OCR support using ReadSeeker (no temporary files needed!)
		return r.readFromReaderWithOCR(ctx, readSeeker, name)
	}

	// No OCR needed - process directly using efficient text extraction
	return r.extractTextFromReader(readSeeker, name)
}

// readFromFileTextOnly reads PDF content from a file path using only text extraction (no OCR).
// This is more efficient when OCR is not needed.
func (r *Reader) readFromFileTextOnly(filePath, name string) ([]*document.Document, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF file: %w", err)
	}
	defer file.Close()

	// Get file size for PDF reader
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}
	size := fileInfo.Size()

	// Create PDF reader for text extraction
	pdfReader, err := pdf.NewReader(file, size)
	if err != nil {
		return nil, fmt.Errorf("failed to create PDF reader: %w", err)
	}

	// Extract text from all pages
	text, err := r.extractTextFromPDFReader(pdfReader)
	if err != nil {
		return nil, fmt.Errorf("failed to extract text: %w", err)
	}

	doc := idocument.CreateDocument(text, name)
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// readFromFileWithOCR reads PDF content from a file path with OCR support.
// It extracts both text from the PDF text layer and text from images using OCR.
func (r *Reader) readFromFileWithOCR(ctx context.Context, filePath, name string) ([]*document.Document, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF file: %w", err)
	}
	defer file.Close()

	// Use the new ReadSeeker-based method for consistency and efficiency
	return r.readFromReaderWithOCR(ctx, file, name)
}

// extractTextFromReader reads text from a PDF reader without OCR support.
// This is more efficient as it doesn't require a temporary file.
func (r *Reader) extractTextFromReader(reader io.Reader, name string) ([]*document.Document, error) {
	// If reader is already a ReadSeeker, use it directly
	var readSeeker io.ReadSeeker
	if rs, ok := reader.(io.ReadSeeker); ok {
		readSeeker = rs
	} else {
		// Read all content to create a ReadSeeker
		content, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to read PDF content: %w", err)
		}
		readSeeker = bytes.NewReader(content)
	}

	// Extract text using the ReadSeeker
	text, err := r.extractTextFromReadSeeker(readSeeker)
	if err != nil {
		return nil, fmt.Errorf("failed to extract text: %w", err)
	}

	doc := idocument.CreateDocument(text, name)
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// readFromReaderWithOCR reads PDF content from a ReadSeeker with OCR support.
// This method processes each page sequentially to maintain context between text and images.
func (r *Reader) readFromReaderWithOCR(ctx context.Context, readSeeker io.ReadSeeker, name string) ([]*document.Document, error) {
	// Extract content page by page to maintain context
	pageContents, err := r.extractContentByPage(ctx, readSeeker)
	if err != nil {
		return nil, fmt.Errorf("failed to extract content by page: %w", err)
	}

	// Combine all page contents
	var allText strings.Builder
	for i, pageContent := range pageContents {
		if i > 0 {
			allText.WriteString("\n")
		}
		allText.WriteString(pageContent)
	}

	doc := idocument.CreateDocument(allText.String(), name)
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// extractContentByPage extracts text and OCR content for each page separately.
// This maintains the context relationship between text and images on the same page.
func (r *Reader) extractContentByPage(ctx context.Context, readSeeker io.ReadSeeker) ([]string, error) {

	// Read PDF content once to avoid multiple reads
	if _, err := readSeeker.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to start: %w", err)
	}

	content, err := io.ReadAll(readSeeker)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}

	// Create PDF reader for text extraction
	pdfReader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("failed to create PDF reader: %w", err)
	}

	totalPages := pdfReader.NumPage()
	pageContents := make([]string, 0, totalPages)

	// Create pdfcpu context once for image extraction
	// This is more efficient than calling ExtractImagesRaw which processes all pages
	var pdfcpuCtx *model.Context
	if r.ocrExtractor != nil {
		conf := model.NewDefaultConfiguration()
		conf.Cmd = model.EXTRACTIMAGES
		pdfcpuCtx, err = pdfcpuAPI.ReadValidateAndOptimize(bytes.NewReader(content), conf)
		if err != nil {
			// If we can't create pdfcpu context, continue without OCR
			pdfcpuCtx = nil
		}
	}

	// Process each page (1-indexed)
	for pageIndex := 1; pageIndex <= totalPages; pageIndex++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var pageContent strings.Builder

		// 1. Extract text from this page
		pageText := r.extractTextFromPage(pdfReader, pageIndex)
		if pageText != "" {
			pageContent.WriteString(pageText)
		}

		// 2. Extract and OCR images from this page (on-demand, per page)
		if pdfcpuCtx != nil && r.ocrExtractor != nil {
			pageImages, err := pdfcpu.ExtractPageImages(pdfcpuCtx, pageIndex, false)
			if err == nil && len(pageImages) > 0 {
				for _, img := range pageImages {
					imageData, err := r.getImageDataFromPDFCPUImage(img)
					if err != nil || len(imageData) == 0 {
						continue
					}

					// Use OCR to extract text from image
					ocrText, err := r.ocrExtractor.ExtractText(ctx, imageData)
					if err != nil {
						continue
					}

					if ocrText != "" {
						// Add OCR text with source marker
						if pageContent.Len() > 0 {
							pageContent.WriteString("\n")
						}
						// Mark the content as coming from OCR with page and image number
						pageContent.WriteString(ocrText)
					}
				}
			}
		}

		// Add this page's content to the result
		if pageContent.Len() > 0 {
			pageContents = append(pageContents, pageContent.String())
		}
	}

	return pageContents, nil
}

// extractTextFromPage extracts text from a single PDF page.
// This is a helper method to avoid code duplication.
func (r *Reader) extractTextFromPage(pdfReader *pdf.Reader, pageIndex int) string {
	page := pdfReader.Page(pageIndex)
	if page.V.IsNull() {
		return ""
	}

	text, err := page.GetPlainText(nil)
	if err != nil || text == "" {
		return ""
	}

	return text
}

// extractTextFromReadSeeker extracts text from a PDF ReadSeeker.
func (r *Reader) extractTextFromReadSeeker(readSeeker io.ReadSeeker) (string, error) {
	// Reset to beginning
	if _, err := readSeeker.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("failed to seek to start: %w", err)
	}

	// Read content for PDF reader
	content, err := io.ReadAll(readSeeker)
	if err != nil {
		return "", fmt.Errorf("failed to read content: %w", err)
	}

	// Create PDF reader from bytes (using bytes.NewReader for better memory efficiency)
	pdfReader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("failed to create PDF reader: %w", err)
	}

	// Extract text from all pages
	return r.extractTextFromPDFReader(pdfReader)
}

// extractTextFromPDFReader extracts text from all pages of a PDF reader.
// This is a common helper function used by both text-only and OCR-enabled processing.
func (r *Reader) extractTextFromPDFReader(pdfReader *pdf.Reader) (string, error) {
	var allText strings.Builder
	totalPage := pdfReader.NumPage()

	// Extract text from each page
	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		page := pdfReader.Page(pageIndex)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err == nil && text != "" {
			allText.WriteString(text)
			allText.WriteString("\n")
		}
	}

	return allText.String(), nil
}

// getImageDataFromPDFCPUImage extracts raw image data from pdfcpu's model.Image.
// This is a helper method that handles the pdfcpu Image structure.
func (r *Reader) getImageDataFromPDFCPUImage(img model.Image) ([]byte, error) {
	// The pdfcpu model.Image should contain a Reader or raw data
	// We need to read from it to get the actual image bytes
	if img.Reader != nil {
		return io.ReadAll(img.Reader)
	}

	// If there's no Reader, the image might be stored differently
	// This would need to be adjusted based on the actual pdfcpu Image structure
	return nil, fmt.Errorf("no image data available")
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

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}
