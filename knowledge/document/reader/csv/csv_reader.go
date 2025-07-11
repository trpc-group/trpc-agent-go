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

// Package csv provides CSV document reader implementation.
package csv

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
)

// Reader reads CSV documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
}

// Option represents a functional option for configuring the CSV reader.
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

// New creates a new CSV reader with the given options.
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

// ReadFromReader reads CSV content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, reader io.Reader) ([]*document.Document, error) {
	// Read content from reader.
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	// Convert CSV to text.
	textContent := r.csvToText(string(content))
	// Create document.
	doc := idocument.CreateDocument(textContent, name)
	// Apply chunking if enabled.
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// ReadFromFile reads CSV content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	// Read file content.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	// Get file name without extension.
	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	// Convert CSV to text.
	textContent := r.csvToText(string(content))
	// Create document.
	doc := idocument.CreateDocument(textContent, fileName)
	// Apply chunking if enabled.
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// ReadFromURL reads CSV content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(url string) ([]*document.Document, error) {
	// Download CSV from URL.
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Get file name from URL.
	fileName := r.extractFileNameFromURL(url)
	return r.ReadFromReader(fileName, resp.Body)
}

// csvToText converts CSV content to a readable text format.
func (r *Reader) csvToText(csvContent string) string {
	// Split content into lines.
	lines := strings.Split(csvContent, "\n")
	// Process each line to handle CSV formatting.
	var processedLines []string
	for _, line := range lines {
		// Skip empty lines.
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Split by comma and clean up each field.
		fields := strings.Split(line, ",")
		for i, field := range fields {
			// Remove quotes and trim whitespace.
			field = strings.Trim(field, `"'`)
			field = strings.TrimSpace(field)
			fields[i] = field
		}
		// Join fields with a more readable separator.
		processedLine := strings.Join(fields, " | ")
		processedLines = append(processedLines, processedLine)
	}
	return strings.Join(processedLines, "\n")
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
		fileName = strings.TrimSuffix(fileName, ".csv")
		return fileName
	}
	return "csv_document"
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "CSVReader"
}
