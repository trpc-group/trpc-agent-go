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

// Package file provides file-based knowledge source implementation.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
)

const (
	defaultFileSourceName = "File Source"
	fileSourceType        = "file"
)

// Source represents a knowledge source for file-based content.
type Source struct {
	filePaths    []string
	name         string
	metadata     map[string]interface{}
	readers      map[string]reader.Reader
	chunkSize    int
	chunkOverlap int
}

// New creates a new file knowledge source.
func New(filePaths []string, opts ...Option) *Source {
	s := &Source{
		filePaths:    filePaths,
		name:         defaultFileSourceName,
		metadata:     make(map[string]interface{}),
		chunkSize:    0,
		chunkOverlap: 0,
	}

	// Apply options first to capture chunk configuration.
	for _, opt := range opts {
		opt(s)
	}

	// Initialize readers with potential custom chunk configuration.
	if s.chunkSize > 0 || s.chunkOverlap > 0 {
		s.readers = isource.GetReadersWithChunkConfig(s.chunkSize, s.chunkOverlap)
	} else {
		s.readers = isource.GetReaders()
	}

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
	metadata := make(map[string]interface{})
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
	// Add metadata to all documents.
	for _, doc := range documents {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]interface{})
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
	}
	return documents, nil
}

// SetReader sets a custom reader for a specific file type.
func (s *Source) SetReader(fileType string, reader reader.Reader) {
	s.readers[fileType] = reader
}

// SetMetadata sets metadata for this source.
func (s *Source) SetMetadata(key string, value interface{}) {
	if s.metadata == nil {
		s.metadata = make(map[string]interface{})
	}
	s.metadata[key] = value
}
