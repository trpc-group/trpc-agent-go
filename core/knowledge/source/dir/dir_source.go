// Package dir provides directory-based knowledge source implementation.
package dir

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/readerfactory"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

const (
	defaultDirSourceName = "Directory Source"
	dirSourceType        = "dir"
)

// Source represents a knowledge source for directory-based content.
type Source struct {
	dirPath        string
	name           string
	metadata       map[string]interface{}
	readerFactory  *readerfactory.Factory
	fileExtensions []string // Optional: filter by file extensions
	recursive      bool     // Whether to process subdirectories
}

// New creates a new directory knowledge source.
func New(dirPath string, opts ...Option) *Source {
	sourceObj := &Source{
		dirPath:       dirPath,
		name:          defaultDirSourceName,
		metadata:      make(map[string]interface{}),
		readerFactory: readerfactory.NewFactory(), // Use default config.
		recursive:     false,                      // Default to non-recursive.
	}

	// Apply options.
	for _, opt := range opts {
		opt(sourceObj)
	}

	return sourceObj
}

// ReadDocuments reads all files in the directory and returns documents using appropriate readers.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if s.dirPath == "" {
		return nil, fmt.Errorf("no directory path provided")
	}

	// Get all file paths in the directory.
	filePaths, err := s.getFilePaths()
	if err != nil {
		return nil, fmt.Errorf("failed to get file paths: %w", err)
	}

	if len(filePaths) == 0 {
		return nil, fmt.Errorf("no files found in directory: %s", s.dirPath)
	}

	var allDocuments []*document.Document

	for _, filePath := range filePaths {
		documents, err := s.processFile(filePath)
		if err != nil {
			// Log error but continue with other files.
			fmt.Printf("Warning: failed to process file %s: %v\n", filePath, err)
			continue
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
	return source.TypeDir
}

// getFilePaths returns all file paths in the directory.
func (s *Source) getFilePaths() ([]string, error) {
	var filePaths []string

	err := filepath.Walk(s.dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories if not recursive.
		if info.IsDir() {
			if path == s.dirPath {
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

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Read file content.
	content, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read file content: %w", err)
	}

	// Create metadata for this file.
	metadata := make(map[string]interface{})
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

	// Create the appropriate reader based on file extension.
	reader := s.readerFactory.CreateReader(filePath)

	// Read the file content and create documents.
	documents, err := reader.Read(string(content), filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("failed to read file with reader: %w", err)
	}

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

// SetReaderFactory sets the reader factory for this source.
func (s *Source) SetReaderFactory(factory *readerfactory.Factory) {
	s.readerFactory = factory
}

// SetMetadata sets metadata for this source.
func (s *Source) SetMetadata(key string, value interface{}) {
	if s.metadata == nil {
		s.metadata = make(map[string]interface{})
	}
	s.metadata[key] = value
}
 