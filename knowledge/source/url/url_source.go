//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package url provides URL-based knowledge source implementation.
package url

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

const (
	defaultURLSourceName = "URL Source"
)

var defaultClient = &http.Client{Timeout: 30 * time.Second}

// Source represents a knowledge source for URL-based content.
type Source struct {
	identifierURLs         []string // url, used to generate document ID and check update of document.
	fetchURLs              []string // fetching url , the actual used to fetch content.
	name                   string
	metadata               map[string]any
	readers                map[string]reader.Reader
	httpClient             *http.Client
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	transformers           []transform.Transformer
	fileReaderType         source.FileReaderType
	contentExtractor       extractor.Extractor
}

// New creates a new URL knowledge source.
func New(urls []string, opts ...Option) *Source {
	s := &Source{
		identifierURLs: urls,
		name:           defaultURLSourceName,
		metadata:       make(map[string]any),
		httpClient:     defaultClient,
		chunkSize:      0,
		chunkOverlap:   0,
	}

	// Apply options first (capture configuration).
	for _, opt := range opts {
		opt(s)
	}

	// Build reader options - pass all configurations to internal source layer
	var readerOpts []isource.ReaderOption
	if s.chunkSize > 0 {
		readerOpts = append(readerOpts, isource.WithChunkSize(s.chunkSize))
	}
	if s.chunkOverlap > 0 {
		readerOpts = append(readerOpts, isource.WithChunkOverlap(s.chunkOverlap))
	}
	if s.customChunkingStrategy != nil {
		readerOpts = append(readerOpts, isource.WithCustomChunkingStrategy(s.customChunkingStrategy))
	}
	if len(s.transformers) > 0 {
		readerOpts = append(readerOpts, isource.WithTransformers(s.transformers...))
	}

	// Initialize readers with configuration
	s.readers = isource.GetReaders(readerOpts...)
	return s
}

// ReadDocuments downloads content from all URLs and returns documents using appropriate readers.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.identifierURLs) == 0 {
		return nil, nil // Skip if no URLs provided.
	}

	if len(s.fetchURLs) > 0 && len(s.identifierURLs) != len(s.fetchURLs) {
		return nil, fmt.Errorf("fetchURLs and urls must have the same count")
	}

	var allDocuments []*document.Document

	for i, identifierURL := range s.identifierURLs {
		fetchingURL := identifierURL
		if len(s.fetchURLs) > 0 {
			fetchingURL = s.fetchURLs[i]
		}
		documents, err := s.processURL(ctx, fetchingURL, identifierURL)
		if err != nil {
			return nil, fmt.Errorf("failed to process URL %s: %w", identifierURL, err)
		}
		allDocuments = append(allDocuments, documents...)
	}

	return allDocuments, nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return source.TypeURL
}

// processURL downloads content from a URL and returns its documents.
func (s *Source) processURL(ctx context.Context, fetchingURL string, identifierURL string) ([]*document.Document, error) {
	// Parse the URL.
	_, err := url.Parse(fetchingURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse fetching URL: %w", err)
	}

	// Parse and validate the identifier URL.
	parsedIdentifierURL, err := url.Parse(identifierURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse identifier URL: %w", err)
	}

	fileName := s.getFileName(parsedIdentifierURL, "")
	documents, _, err := s.fetchAndRead(ctx, fetchingURL, parsedIdentifierURL, fileName)
	if err != nil {
		return nil, err
	}

	// Create metadata for this URL.
	metadata := make(map[string]any)
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeURL
	metadata[source.MetaURL] = identifierURL
	metadata[source.MetaURLHost] = parsedIdentifierURL.Host
	metadata[source.MetaURLPath] = parsedIdentifierURL.Path
	metadata[source.MetaURLScheme] = parsedIdentifierURL.Scheme
	metadata[source.MetaURI] = identifierURL
	metadata[source.MetaSourceName] = s.name

	// Add metadata to all documents.
	for _, doc := range documents {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
	}

	return documents, nil
}

// fetchAndRead performs the HTTP download and reads the content using the appropriate reader or extractor.
// It returns the documents, the resolved file name, and any error.
func (s *Source) fetchAndRead(ctx context.Context, fetchingURL string, parsedIdentifierURL *url.URL, fileName string) ([]*document.Document, string, error) {
	// Create HTTP request with context.
	req, err := http.NewRequestWithContext(ctx, "GET", fetchingURL, nil)
	if err != nil {
		return nil, fileName, fmt.Errorf("failed to create request: %w", err)
	}

	// Set user agent to avoid being blocked.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; KnowledgeSource/1.0)")

	// Make the request.
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fileName, fmt.Errorf("failed to download URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fileName, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Determine the content type and file name.
	contentType := resp.Header.Get("Content-Type")
	fileName = s.getFileName(parsedIdentifierURL, contentType)

	ext := strings.ToLower(filepath.Ext(fileName))
	if s.contentExtractor != nil && !extractor.Supports(s.contentExtractor, ext) {
		if inferredExt := extractorExtFromContentType(contentType); inferredExt != "" {
			ext = inferredExt
		}
	}
	if s.contentExtractor != nil && extractor.Supports(s.contentExtractor, ext) {
		docs, err := s.extractFromResponse(ctx, resp.Body, fileName)
		return docs, fileName, err
	}

	// Determine file type and get appropriate reader.
	fileType := isource.ResolveFileType(string(s.fileReaderType), isource.GetFileTypeFromContentType(contentType, fileName))
	r, exists := s.readers[fileType]
	if !exists {
		return nil, fileName, fmt.Errorf("no reader available for file type: %s", fileType)
	}

	// Read the content using the reader's ReadFromReader method.
	documents, err := r.ReadFromReader(fileName, resp.Body)
	if err != nil {
		return nil, fileName, fmt.Errorf("failed to read content with reader: %w", err)
	}
	return documents, fileName, nil
}

// extractFromResponse uses the content extractor to process the HTTP response body.
func (s *Source) extractFromResponse(ctx context.Context, body io.Reader, fileName string) ([]*document.Document, error) {
	result, err := s.contentExtractor.ExtractFromReader(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("content extraction failed: %w", err)
	}
	r, exists := s.readers[result.Format]
	if !exists {
		return nil, fmt.Errorf("no reader available for extracted format: %s", result.Format)
	}
	documents, err := r.ReadFromReader(fileName, result.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read extracted content with reader: %w", err)
	}
	return documents, nil
}

func extractorExtFromContentType(contentType string) string {
	if contentType == "" {
		return ""
	}
	parts := strings.Split(contentType, ";")
	mainType := strings.TrimSpace(parts[0])

	switch {
	case strings.Contains(mainType, "text/html"):
		return ".html"
	case strings.Contains(mainType, "application/pdf"):
		return ".pdf"
	case strings.Contains(mainType, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"):
		return ".docx"
	case strings.Contains(mainType, "application/vnd.openxmlformats-officedocument.presentationml.presentation"):
		return ".pptx"
	case strings.Contains(mainType, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"):
		return ".xlsx"
	case strings.Contains(mainType, "text/csv"):
		return ".csv"
	case strings.Contains(mainType, "image/png"):
		return ".png"
	case strings.Contains(mainType, "image/jpeg"):
		return ".jpg"
	case strings.Contains(mainType, "image/tiff"):
		return ".tiff"
	case strings.Contains(mainType, "image/bmp"):
		return ".bmp"
	default:
		return ""
	}
}

// getFileName extracts a file name from the URL or content type.
func (s *Source) getFileName(parsedURL *url.URL, contentType string) string {
	// Try to get file name from URL path.
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		fileName := filepath.Base(parsedURL.Path)
		if fileName != "" && fileName != "." {
			return fileName
		}
	}
	// Try to get file name from content type.
	if contentType != "" {
		parts := strings.Split(contentType, ";")
		mainType := strings.TrimSpace(parts[0])

		switch {
		case strings.Contains(mainType, "text/html"):
			return "index.html"
		case strings.Contains(mainType, "text/plain"):
			return "document.txt"
		case strings.Contains(mainType, "application/json"):
			return "document.json"
		case strings.Contains(mainType, "text/csv"):
			return "document.csv"
		case strings.Contains(mainType, "application/pdf"):
			return "document.pdf"
		default:
			return "document"
		}
	}
	// Fall back to host name.
	if parsedURL.Host != "" {
		return parsedURL.Host + ".txt"
	}
	return "document.txt"
}

// SetMetadata sets metadata for this source.
func (s *Source) SetMetadata(key string, value any) {
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	s.metadata[key] = value
}

// GetMetadata returns the metadata associated with this source.
func (s *Source) GetMetadata() map[string]any {
	result := make(map[string]any)
	for k, v := range s.metadata {
		result[k] = v
	}
	return result
}
