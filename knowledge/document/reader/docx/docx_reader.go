//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package docx provides DOCX document reader implementation.
package docx

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gonfva/docxlib"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
)

// Reader reads DOCX documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
}

// Option represents a functional option for configuring the DOCX reader.
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

// New creates a new DOCX reader with the given options.
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

// ReadFromReader reads DOCX content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, reader io.Reader) ([]*document.Document, error) {
	return r.readFromReader(reader, name)
}

// ReadFromFile reads DOCX content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	// Open the file.
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file size.
	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Parse the DOCX document.
	doc, err := docxlib.Parse(file, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("failed to parse DOCX: %w", err)
	}

	// Extract text content.
	textContent := r.extractTextFromDoc(doc)

	// Get file name without extension.
	fileName := strings.TrimSuffix(
		filepath.Base(filePath), filepath.Ext(filePath),
	)

	// Create document.
	docResult := idocument.CreateDocument(textContent, fileName)
	// Apply chunking if enabled.
	if r.chunk {
		return r.chunkDocument(docResult)
	}
	return []*document.Document{docResult}, nil
}

// ReadFromURL reads DOCX content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(url string) ([]*document.Document, error) {
	// Download DOCX from URL.
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Get file name from URL.
	fileName := r.extractFileNameFromURL(url)
	return r.readFromReader(resp.Body, fileName)
}

// readFromReader reads DOCX content from an io.Reader and returns a list of documents.
func (r *Reader) readFromReader(reader io.Reader, name string) ([]*document.Document, error) {
	// Read all data from the reader.
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// Create a temporary file to work with docxlib.
	tmpFile, err := os.CreateTemp("", "docx_*.docx")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name()) // Clean up temporary file.

	// Write data to temporary file.
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write to temporary file: %w", err)
	}

	// Close and reopen for reading.
	tmpFile.Close()
	file, err := os.Open(tmpFile.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to reopen temporary file: %w", err)
	}
	defer file.Close()

	// Get file size.
	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Parse the DOCX document.
	doc, err := docxlib.Parse(file, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("failed to parse DOCX: %w", err)
	}

	// Extract text content.
	textContent := r.extractTextFromDoc(doc)

	// Create document.
	docResult := idocument.CreateDocument(textContent, name)
	// Apply chunking if enabled.
	if r.chunk {
		return r.chunkDocument(docResult)
	}
	return []*document.Document{docResult}, nil
}

// extractTextFromDoc extracts all text content from a docxlib document.
func (r *Reader) extractTextFromDoc(doc *docxlib.DocxLib) string {
	var textContent strings.Builder

	// Get all paragraphs from the document.
	paragraphs := doc.Paragraphs()

	for _, paragraph := range paragraphs {
		// Get children (runs, hyperlinks, etc.) from the paragraph.
		children := paragraph.Children()

		for _, child := range children {
			// Extract text from runs.
			if child.Run != nil && child.Run.Text != nil {
				text := strings.TrimSpace(child.Run.Text.Text)
				if text != "" {
					textContent.WriteString(text)
					textContent.WriteString(" ")
				}
			}

			// Extract text from hyperlinks.
			if child.Link != nil && child.Link.Run.Text != nil {
				text := strings.TrimSpace(child.Link.Run.Text.Text)
				if text != "" {
					textContent.WriteString(text)
					textContent.WriteString(" ")
				}
			}
		}

		// Add newline after each paragraph.
		if textContent.Len() > 0 {
			textContent.WriteString("\n")
		}
	}

	return strings.TrimSpace(textContent.String())
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
		fileName = strings.TrimSuffix(fileName, ".docx")
		return fileName
	}
	return "docx_document"
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "DOCXReader"
}
