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
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
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
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
}

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
	if s.chunkSize > 0 {
		readerOpts = append(readerOpts, isource.WithChunkSize(s.chunkSize))
	}
	if s.chunkOverlap > 0 {
		readerOpts = append(readerOpts, isource.WithChunkOverlap(s.chunkOverlap))
	}
	if s.customChunkingStrategy != nil {
		readerOpts = append(readerOpts, isource.WithCustomChunkingStrategy(s.customChunkingStrategy))
	}
	if s.ocrExtractor != nil {
		readerOpts = append(readerOpts, isource.WithOCRExtractor(s.ocrExtractor))
	}

	// Initialize readers with configuration
	s.readers = isource.GetReaders(readerOpts...)

	return s
}

// ReadDocuments reads all files and returns documents using appropriate readers.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.filePaths) == 0 {
		return nil, nil // Skip if no file paths provided.
	}
	var allDocuments []*document.Document
	for _, filePath := range s.filePaths {
		documents, err := s.processFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to process file %s: %w", filePath, err)
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
	return source.TypeFile
}

// processFile processes a single file and returns its documents.
func (s *Source) processFile(filePath string) ([]*document.Document, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}
	// Determine file type and get appropriate reader.
	fileType := isource.GetFileType(filePath)
	reader, exists := s.readers[fileType]
	if !exists {
		return nil, fmt.Errorf("no reader available for file type: %s", fileType)
	}
	// Read the file using the reader's ReadFromFile method.
	documents, err := reader.ReadFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file with reader: %w", err)
	}
	// Create metadata for this file.
	metadata := make(map[string]any)
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

	// Get absolute path for URI
	// Not include ip address and port
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: absPath}).String()
	metadata[source.MetaURI] = fileURL
	metadata[source.MetaSourceName] = s.name

	// Add metadata to all documents.
	chunkIndex := 0
	for _, doc := range documents {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
		chunkIndex++
	}
	return documents, nil
}

// SetReader sets a custom reader for a specific file type.
func (s *Source) SetReader(fileType string, reader reader.Reader) {
	s.readers[fileType] = reader
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
