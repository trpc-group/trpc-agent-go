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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
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
	defaultDirSourceName = "Directory Source"
)

// Source represents a knowledge source for directory-based content.
type Source struct {
	dirPaths               []string
	name                   string
	metadata               map[string]any
	readers                map[string]reader.Reader
	resourceReaders        map[string]reader.Reader
	fileExtensions         []string // Optional: filter by file extensions
	recursive              bool     // Whether to process subdirectories
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
	transformers           []transform.Transformer
	fileReaderType         source.FileReaderType
	contentExtractor       extractor.Extractor
}

var _ source.ResourceSource = (*Source)(nil)

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

	resourceReaderOpts := []isource.ReaderOption{isource.WithChunk(false)}
	if s.ocrExtractor != nil {
		resourceReaderOpts = append(resourceReaderOpts, isource.WithOCRExtractor(s.ocrExtractor))
	}
	if len(s.transformers) > 0 {
		resourceReaderOpts = append(resourceReaderOpts, isource.WithTransformers(s.transformers...))
	}
	s.resourceReaders = isource.GetReaders(resourceReaderOpts...)
}

// getFileType determines the file type based on the file extension.
func (s *Source) getFileType(filePath string) string {
	return isource.ResolveFileType(string(s.fileReaderType), isource.GetFileType(filePath))
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
			documents, err := s.processFile(ctx, filePath)
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

// ReadResources reads complete source representations and the documents
// derived from the same file snapshots.
func (s *Source) ReadResources(ctx context.Context) ([]*source.Resource, error) {
	if len(s.dirPaths) == 0 {
		return nil, nil
	}

	var resources []*source.Resource
	seenRoots := make(map[string]string, len(s.dirPaths))
	seenPaths := make(map[string]string)
	seenFiles := make(map[string]string)
	totalFiles := 0
	for _, dirPath := range s.dirPaths {
		if dirPath == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		absRoot, err := filepath.Abs(dirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve directory %s: %w", dirPath, err)
		}
		rootName := filepath.Base(filepath.Clean(absRoot))
		if rootName == "." || rootName == string(filepath.Separator) {
			return nil, fmt.Errorf("directory %q has no usable resource root name", dirPath)
		}
		if previous, ok := seenRoots[rootName]; ok {
			return nil, fmt.Errorf("duplicate resource root %q from directories %q and %q", rootName, previous, dirPath)
		}
		seenRoots[rootName] = dirPath

		filePaths, err := s.getFilePaths(dirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get file paths from directory %s: %w", dirPath, err)
		}
		totalFiles += len(filePaths)
		for _, filePath := range filePaths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			absFile, err := filepath.Abs(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve file %s: %w", filePath, err)
			}
			if previous, ok := seenFiles[absFile]; ok {
				return nil, fmt.Errorf("file %q is included by both %q and %q", absFile, previous, dirPath)
			}
			seenFiles[absFile] = dirPath

			relPath, err := filepath.Rel(dirPath, filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to build directory-relative path: %w", err)
			}
			resourcePath := path.Join(rootName, filepath.ToSlash(relPath))
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
	}

	if totalFiles == 0 {
		return nil, fmt.Errorf("no files found in any of the provided directories")
	}
	return resources, nil
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
	fileType := s.getFileType(filePath)
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
	metadata[source.MetaSource] = source.TypeDir
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

	r, exists := s.readers[result.Format]
	if !exists {
		return nil, fmt.Errorf("no reader available for extracted format: %s", result.Format)
	}

	return r.ReadFromReader(fileName, result.Reader)
}

// readWithReader uses the registered reader to process the file directly.
func (s *Source) readWithReader(filePath string) ([]*document.Document, error) {
	fileType := s.getFileType(filePath)
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
