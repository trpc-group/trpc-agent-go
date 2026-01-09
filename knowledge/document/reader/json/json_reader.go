//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package json provides JSON document reader implementation.
package json

import (
	"encoding/json"
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
	supportedExtensions = []string{".json"}
)

// init registers the JSON reader with the global registry.
func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads JSON documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
	transformers     []transform.Transformer
}

// New creates a new JSON reader with the given options.
// JSON reader uses JSONChunking by default.
func New(opts ...reader.Option) reader.Reader {
	// Build config from options
	config := &reader.Config{
		Chunk: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Build chunking strategy using the default builder for JSON
	strategy := reader.BuildChunkingStrategy(config, buildDefaultChunkingStrategy)

	// Create reader from config
	return &Reader{
		chunk:            config.Chunk,
		chunkingStrategy: strategy,
		transformers:     config.Transformers,
	}
}

// buildDefaultChunkingStrategy builds the default chunking strategy for JSON reader.
// JSON uses JSONChunking with configurable chunk size.
func buildDefaultChunkingStrategy(chunkSize, overlap int) chunking.Strategy {
	var opts []chunking.JSONOption
	if chunkSize > 0 {
		opts = append(opts, chunking.WithJSONChunkSize(chunkSize))
	}
	// Note: JSONChunking doesn't support overlap parameter
	return chunking.NewJSONChunking(opts...)
}

// ReadFromReader reads JSON content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	// Read content from reader.
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}

	// Convert JSON to text.
	textContent, err := r.jsonToText(string(content))
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

// ReadFromFile reads JSON content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	// Read file content.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Get file name without extension.
	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	// Convert JSON to text.
	textContent, err := r.jsonToText(string(content))
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

// ReadFromURL reads JSON content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	// Validate URL before making HTTP request.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	// Download JSON from URL.
	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Get file name from URL.
	fileName := r.extractFileNameFromURL(urlStr)

	return r.ReadFromReader(fileName, resp.Body)
}

// jsonToText converts JSON content to a readable text format.
func (r *Reader) jsonToText(jsonContent string) (string, error) {
	var data any
	if err := json.Unmarshal([]byte(jsonContent), &data); err != nil {
		return "", err
	}

	// Convert to pretty-printed JSON for better readability.
	prettyJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}

	return string(prettyJSON), nil
}

// chunkDocuments applies chunking to documents.
func (r *Reader) chunkDocuments(docs []*document.Document) ([]*document.Document, error) {
	if r.chunkingStrategy == nil {
		r.chunkingStrategy = chunking.NewJSONChunking()
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
		fileName = strings.TrimSuffix(fileName, ".json")
		return fileName
	}
	return "json_document"
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "JSONReader"
}

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}
