//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package csv provides CSV document reader implementation.
package csv

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	itransform "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

var (
	// supportedExtensions defines the file extensions supported by this reader.
	supportedExtensions = []string{".csv"}
)

// init registers the CSV reader with the global registry.
func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads CSV documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
	transformers     []transform.Transformer
}

// New creates a new CSV reader with the given options.
// CSV reader uses line-preserving FixedSizeChunking by default.
func New(opts ...reader.Option) reader.Reader {
	// Build config from options
	config := &reader.Config{
		Chunk: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Build chunking strategy using the default builder for CSV
	strategy := reader.BuildChunkingStrategy(config, buildDefaultChunkingStrategy)

	// Create reader from config
	return &Reader{
		chunk:            config.Chunk,
		chunkingStrategy: strategy,
		transformers:     config.Transformers,
	}
}

// buildDefaultChunkingStrategy builds the default chunking strategy for CSV reader.
// Newlines keep complete records together whenever one record fits the budget.
// Oversized records are refined by natural text boundaries.
func buildDefaultChunkingStrategy(chunkSize, overlap int) chunking.Strategy {
	opts := []chunking.Option{chunking.WithPreserveLines()}
	if chunkSize != 0 {
		opts = append(opts, chunking.WithChunkSize(chunkSize))
	}
	if overlap != 0 {
		opts = append(opts, chunking.WithOverlap(overlap))
	}
	return chunking.NewFixedSizeChunking(opts...)
}

// ReadFromReader reads CSV content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	// Read content from reader.
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	// Convert CSV to text.
	textContent, err := r.csvToText(string(content))
	if err != nil {
		return nil, err
	}
	// Create document.
	doc := idocument.CreateDocument(textContent, name)

	// Apply preprocess.
	docs, err := itransform.ApplyPreprocess([]*document.Document{doc}, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply preprocess: %w", err)
	}

	// Apply chunking if enabled.
	if r.chunk {
		docs, err = r.chunkDocuments(docs)
		if err != nil {
			return nil, err
		}
	}

	// Apply postprocess.
	docs, err = itransform.ApplyPostprocess(docs, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply postprocess: %w", err)
	}

	return docs, nil
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
	textContent, err := r.csvToText(string(content))
	if err != nil {
		return nil, err
	}
	// Create document.
	doc := idocument.CreateDocument(textContent, fileName)

	// Apply preprocess.
	docs, err := itransform.ApplyPreprocess([]*document.Document{doc}, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply preprocess: %w", err)
	}

	// Apply chunking if enabled.
	if r.chunk {
		docs, err = r.chunkDocuments(docs)
		if err != nil {
			return nil, err
		}
	}

	// Apply postprocess.
	docs, err = itransform.ApplyPostprocess(docs, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply postprocess: %w", err)
	}

	return docs, nil
}

// ReadFromURL reads CSV content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	// Validate URL before making HTTP request.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	// Download CSV from URL.
	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Get file name from URL.
	fileName := r.extractFileNameFromURL(urlStr)
	return r.ReadFromReader(fileName, resp.Body)
}

// csvToText converts CSV records to one normalized text line per record.
func (r *Reader) csvToText(csvContent string) (string, error) {
	csvReader := csv.NewReader(strings.NewReader(csvContent))
	csvReader.FieldsPerRecord = -1
	records, err := csvReader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("parse csv: %w", err)
	}

	processedLines := make([]string, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		fields := make([]string, len(record))
		for i, field := range record {
			field = strings.ReplaceAll(field, "\r\n", "\n")
			field = strings.ReplaceAll(field, "\r", "\n")
			field = strings.ReplaceAll(field, "\n", " ")
			fields[i] = strings.TrimSpace(field)
		}
		processedLines = append(processedLines, strings.Join(fields, " | "))
	}
	return strings.Join(processedLines, "\n"), nil
}

// chunkDocuments applies chunking to documents.
func (r *Reader) chunkDocuments(docs []*document.Document) ([]*document.Document, error) {
	if r.chunkingStrategy == nil {
		r.chunkingStrategy = buildDefaultChunkingStrategy(0, 0)
	}

	var result []*document.Document
	for _, doc := range docs {
		chunks, err := r.chunkingStrategy.Chunk(doc)
		if err != nil {
			return nil, err
		}
		result = append(result, chunks...)
	}
	return result, nil
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

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}
