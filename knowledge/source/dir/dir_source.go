//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package dir provides directory-based knowledge source implementation.
package dir

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
)

const (
	defaultDirSourceName = "Directory Source"
)

// Source represents a knowledge source for directory-based content.
type Source struct {
	dirPaths               []string
	name                   string
	metadata               map[string]any
	readers                map[string]reader.Reader
	fileExtensions         []string // Optional: filter by file extensions
	recursive              bool     // Whether to process subdirectories
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
}

// New creates a new directory knowledge source.
func New(dirPaths []string, opts ...Option) *Source {
	s := &Source{
		dirPaths:     dirPaths,
		name:         defaultDirSourceName,
		metadata:     make(map[string]any),
		recursive:    false, // Default to non-recursive.
		chunkSize:    0,
		chunkOverlap: 0,
	}

	// Apply options first so chunk configuration is set.
	for _, opt := range opts {
		opt(s)
	}

	// Initialize readers with potential custom chunk configuration.
	s.initializeReaders()

	return s
}

// initializeReaders sets up readers for different file types.
func (s *Source) initializeReaders() {
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
}

// getFileType determines the file type based on the file extension.
func (s *Source) getFileType(filePath string) string {
	return isource.GetFileType(filePath)
}

// ReadDocuments reads all files in the directories and returns documents using appropriate readers.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.dirPaths) == 0 {
		return nil, nil // Skip if no directory paths provided.
	}

	var allDocuments []*document.Document
	var totalFiles int

	for _, dirPath := range s.dirPaths {
		if dirPath == "" {
			continue
		}

		// Get all file paths in the directory.
		filePaths, err := s.getFilePaths(dirPath)
		if err != nil {
			// Log error but continue with other directories.
			fmt.Printf("Warning: failed to get file paths from directory %s: %v\n", dirPath, err)
			continue
		}

		if len(filePaths) == 0 {
			fmt.Printf("Warning: no files found in directory: %s\n", dirPath)
			continue
		}

		totalFiles += len(filePaths)

		for _, filePath := range filePaths {
			documents, err := s.processFile(filePath)
			if err != nil {
				// Log error but continue with other files.
				fmt.Printf("Warning: failed to process file %s: %v\n", filePath, err)
				continue
			}
			allDocuments = append(allDocuments, documents...)
		}
	}

	if totalFiles == 0 {
		return nil, fmt.Errorf("no files found in any of the provided directories")
	}

	return allDocuments, nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return source.TypeDir
}

// getFilePaths returns all file paths in the specified directory.
func (s *Source) getFilePaths(dirPath string) ([]string, error) {
	var filePaths []string

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories if not recursive.
		if info.IsDir() {
			if path == dirPath {
				return nil // Process the root directory.
			}
			if !s.recursive {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip if not a regular file.
		if !info.Mode().IsRegular() {
			return nil
		}

		// Filter by file extension if specified.
		if len(s.fileExtensions) > 0 {
			ext := strings.ToLower(filepath.Ext(path))
			found := false
			for _, allowedExt := range s.fileExtensions {
				if ext == allowedExt {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}

		filePaths = append(filePaths, path)
		return nil
	})
	return filePaths, err
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
	fileType := s.getFileType(filePath)
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
	metadata[source.MetaSource] = source.TypeDir
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

// SetMetadata sets a metadata value for the directory source.
func (s *Source) SetMetadata(key string, value any) {
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
