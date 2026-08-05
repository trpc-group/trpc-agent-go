//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package file provides file-based knowledge source implementation.
package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/encoding"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

const (
	defaultFileSourceName = "File Source"
)

// Source represents a knowledge source for file-based content.
type Source struct {
	filePaths              []string
	name                   string
	metadata               map[string]any
	readers                map[string]reader.Reader
	resourceReaders        map[string]reader.Reader
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
	transformers           []transform.Transformer
	fileReaderType         source.FileReaderType
	contentExtractor       extractor.Extractor
}

var _ source.ResourceSource = (*Source)(nil)

// New creates a new file knowledge source.
func New(filePaths []string, opts ...Option) *Source {
	s := &Source{
		filePaths:    filePaths,
		name:         defaultFileSourceName,
		metadata:     make(map[string]any),
		chunkSize:    0,
		chunkOverlap: 0,
	}

	// Apply options first to capture configuration.
	for _, opt := range opts {
		opt(s)
	}

	// Build reader options - pass all configurations to internal source layer
	var readerOpts []isource.ReaderOption
	if s.chunkSize != 0 {
		readerOpts = append(readerOpts, isource.WithChunkSize(s.chunkSize))
	}
	if s.chunkOverlap != 0 {
		readerOpts = append(readerOpts, isource.WithChunkOverlap(s.chunkOverlap))
	}
	if s.customChunkingStrategy != nil {
		readerOpts = append(readerOpts, isource.WithCustomChunkingStrategy(s.customChunkingStrategy))
	}
	if s.ocrExtractor != nil {
		readerOpts = append(readerOpts, isource.WithOCRExtractor(s.ocrExtractor))
	}
	if len(s.transformers) > 0 {
		readerOpts = append(readerOpts, isource.WithTransformers(s.transformers...))
	}

	// Initialize readers with configuration
	s.readers = isource.GetReaders(readerOpts...)

	// Resource readers return the complete normalized representation. Keep them
	// separate from the chunking readers so ReadResources never reconstructs the
	// source text from chunks.
	resourceReaderOpts := []isource.ReaderOption{isource.WithChunk(false)}
	if s.ocrExtractor != nil {
		resourceReaderOpts = append(resourceReaderOpts, isource.WithOCRExtractor(s.ocrExtractor))
	}
	if len(s.transformers) > 0 {
		resourceReaderOpts = append(resourceReaderOpts, isource.WithTransformers(s.transformers...))
	}
	s.resourceReaders = isource.GetReaders(resourceReaderOpts...)

	return s
}

// ReadDocuments reads all files and returns documents using appropriate readers.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.filePaths) == 0 {
		return nil, nil // Skip if no file paths provided.
	}
	var allDocuments []*document.Document
	for _, filePath := range s.filePaths {
		documents, err := s.processFile(ctx, filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to process file %s: %w", filePath, err)
		}
		allDocuments = append(allDocuments, documents...)
	}
	return allDocuments, nil
}

// ReadResources reads complete source representations and the documents
// derived from the same file snapshots.
func (s *Source) ReadResources(ctx context.Context) ([]*source.Resource, error) {
	if len(s.filePaths) == 0 {
		return nil, nil
	}

	resources := make([]*source.Resource, 0, len(s.filePaths))
	seenPaths := make(map[string]string, len(s.filePaths))
	for _, filePath := range s.filePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resourcePath := filepath.ToSlash(filepath.Base(filepath.Clean(filePath)))
		if previous, ok := seenPaths[resourcePath]; ok {
			return nil, fmt.Errorf("duplicate resource path %q from files %q and %q", resourcePath, previous, filePath)
		}
		seenPaths[resourcePath] = filePath

		resource, err := s.processResource(ctx, filePath, resourcePath)
		if err != nil {
			return nil, fmt.Errorf("failed to process resource %s: %w", filePath, err)
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return source.TypeFile
}

// processFile processes a single file and returns its documents.
func (s *Source) processFile(ctx context.Context, filePath string) ([]*document.Document, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	fileName := filepath.Base(filePath)

	var documents []*document.Document

	// If a content extractor is configured and supports this extension, use it.
	if s.contentExtractor != nil && extractor.Supports(s.contentExtractor, ext) {
		documents, err = s.extractAndRead(ctx, filePath, fileName)
	} else {
		documents, err = s.readWithReader(filePath)
	}
	if err != nil {
		return nil, err
	}

	metadata, err := s.buildFileMetadata(filePath, fileInfo)
	if err != nil {
		return nil, err
	}

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

func (s *Source) processResource(
	ctx context.Context,
	filePath string,
	resourcePath string,
) (*source.Resource, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}

	snapshot, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file snapshot: %w", err)
	}
	if len(snapshot) == 0 {
		return &source.Resource{
			Path:       resourcePath,
			ModifiedAt: fileInfo.ModTime().UTC(),
		}, nil
	}
	fileType := isource.ResolveFileType(string(s.fileReaderType), isource.GetFileType(filePath))
	readerName := resourceReaderName(filePath, fileType)

	ext := strings.ToLower(filepath.Ext(filePath))
	if s.contentExtractor != nil && extractor.Supports(s.contentExtractor, ext) {
		result, err := s.contentExtractor.ExtractFromReader(ctx, bytes.NewReader(snapshot))
		if err != nil {
			return nil, fmt.Errorf("content extraction failed: %w", err)
		}
		if result == nil || result.Reader == nil {
			return nil, fmt.Errorf("content extraction returned no content")
		}
		snapshot, err = io.ReadAll(result.Reader)
		if err != nil {
			return nil, fmt.Errorf("failed to read extracted content: %w", err)
		}
		fileType = result.Format
		readerName = filepath.Base(filePath)
	}

	chunkReader, ok := s.readers[fileType]
	if !ok {
		return nil, fmt.Errorf("no reader available for file type: %s", fileType)
	}
	resourceReader, ok := s.resourceReaders[fileType]
	if !ok {
		return nil, fmt.Errorf("no resource reader available for file type: %s", fileType)
	}

	fullDocuments, err := resourceReader.ReadFromReader(readerName, bytes.NewReader(snapshot))
	if err != nil {
		return nil, fmt.Errorf("failed to read complete resource: %w", err)
	}
	content, err := completeResourceContent(fullDocuments)
	if err != nil {
		return nil, err
	}
	documents, err := chunkReader.ReadFromReader(readerName, bytes.NewReader(snapshot))
	if err != nil {
		return nil, fmt.Errorf("failed to read resource documents: %w", err)
	}

	metadata, err := s.buildFileMetadata(filePath, fileInfo)
	if err != nil {
		return nil, err
	}
	metadata[source.MetaResourcePath] = resourcePath
	if err := addMetadata(documents, metadata); err != nil {
		return nil, err
	}

	return &source.Resource{
		Path:       resourcePath,
		Content:    content,
		ModifiedAt: fileInfo.ModTime().UTC(),
		Documents:  documents,
	}, nil
}

func (s *Source) buildFileMetadata(filePath string, fileInfo os.FileInfo) (map[string]any, error) {
	metadata := make(map[string]any, len(s.metadata)+10)
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeFile
	metadata[source.MetaFilePath] = filePath
	metadata[source.MetaFileName] = filepath.Base(filePath)
	metadata[source.MetaFileExt] = filepath.Ext(filePath)
	metadata[source.MetaFileSize] = fileInfo.Size()
	metadata[source.MetaFileMode] = fileInfo.Mode().String()
	metadata[source.MetaModifiedAt] = fileInfo.ModTime().UTC()

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	metadata[source.MetaURI] = (&url.URL{Scheme: "file", Path: absPath}).String()
	metadata[source.MetaSourceName] = s.name
	return metadata, nil
}

func addMetadata(documents []*document.Document, metadata map[string]any) error {
	for _, doc := range documents {
		if doc == nil {
			return fmt.Errorf("reader returned a nil document")
		}
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
	}
	return nil
}

func completeResourceContent(documents []*document.Document) (string, error) {
	if len(documents) != 1 || documents[0] == nil {
		return "", fmt.Errorf("resource reader returned %d documents, want exactly one", len(documents))
	}
	content, _ := encoding.SmartProcessText(documents[0].Content)
	if !utf8.ValidString(content) {
		content = strings.ToValidUTF8(content, "\uFFFD")
	}
	return content, nil
}

func resourceReaderName(filePath, fileType string) string {
	switch fileType {
	case "go", "proto", "python":
		return filePath
	default:
		return strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	}
}

// extractAndRead uses the content extractor to convert the file, then pipes
// the result through the appropriate reader for chunking and processing.
func (s *Source) extractAndRead(ctx context.Context, filePath, fileName string) ([]*document.Document, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for extraction: %w", err)
	}
	defer f.Close()

	result, err := s.contentExtractor.ExtractFromReader(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("content extraction failed: %w", err)
	}

	// Find the reader matching the extraction output format.
	r, exists := s.readers[result.Format]
	if !exists {
		return nil, fmt.Errorf("no reader available for extracted format: %s", result.Format)
	}

	return r.ReadFromReader(fileName, result.Reader)
}

// readWithReader uses the registered reader to process the file directly.
func (s *Source) readWithReader(filePath string) ([]*document.Document, error) {
	fileType := isource.ResolveFileType(string(s.fileReaderType), isource.GetFileType(filePath))
	r, exists := s.readers[fileType]
	if !exists {
		return nil, fmt.Errorf("no reader available for file type: %s", fileType)
	}
	documents, err := r.ReadFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file with reader: %w", err)
	}
	return documents, nil
}

// SetReader sets a custom reader for a specific file type.
func (s *Source) SetReader(fileType string, reader reader.Reader) {
	s.readers[fileType] = reader
	// A chunk reader does not necessarily expose an unchunked representation.
	// Require callers that enable resource ingestion to provide the matching
	// reader explicitly instead of silently pairing incompatible readers.
	delete(s.resourceReaders, fileType)
}

// SetResourceReader sets the reader that must return exactly one complete
// representation for a file type configured through SetReader.
func (s *Source) SetResourceReader(fileType string, resourceReader reader.Reader) {
	if resourceReader == nil {
		delete(s.resourceReaders, fileType)
		return
	}
	s.resourceReaders[fileType] = resourceReader
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
