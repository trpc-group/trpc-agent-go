// Package file provides file-based knowledge source implementation.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Source represents a knowledge source for file-based content.
type Source struct {
	filePaths []string
	name      string
	metadata  map[string]interface{}
}

// New creates a new file knowledge source.
func New(filePaths []string, opts ...Option) *Source {
	source := &Source{
		filePaths: filePaths,
		name:      "File Source", // Default name.
		metadata:  make(map[string]interface{}),
	}

	// Apply options.
	for _, opt := range opts {
		opt(source)
	}

	return source
}

// ReadDocument reads all files and returns a combined document.
func (s *Source) ReadDocument(ctx context.Context) (*document.Document, error) {
	if len(s.filePaths) == 0 {
		return nil, fmt.Errorf("no file paths provided")
	}

	var allContent strings.Builder
	var allMetadata []map[string]interface{}

	for _, filePath := range s.filePaths {
		content, metadata, err := s.processFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to process file %s: %w", filePath, err)
		}
		allContent.WriteString(content)
		allContent.WriteString("\n\n")
		allMetadata = append(allMetadata, metadata)
	}

	return s.createDocument(allContent.String(), allMetadata), nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return "file"
}

// processFile processes a single file and returns its content and metadata.
func (s *Source) processFile(filePath string) (string, map[string]interface{}, error) {
	// Get file info.
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Check if it's a regular file.
	if !fileInfo.Mode().IsRegular() {
		return "", nil, fmt.Errorf("not a regular file: %s", filePath)
	}

	// Read file content.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Prepare metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "file"
	metadata["file_path"] = filePath
	metadata["file_name"] = filepath.Base(filePath)
	metadata["file_ext"] = filepath.Ext(filePath)
	metadata["file_size"] = fileInfo.Size()
	metadata["file_mode"] = fileInfo.Mode().String()
	metadata["modified_at"] = fileInfo.ModTime().UTC()
	metadata["content_length"] = len(content)

	return string(content), metadata, nil
}

// createDocument creates a document from combined file content.
func (s *Source) createDocument(content string, fileMetadata []map[string]interface{}) *document.Document {
	// Generate ID based on file paths.
	id := s.generateFileID()

	// Generate name from first file.
	name := "Multiple Files"
	if len(s.filePaths) > 0 {
		name = filepath.Base(s.filePaths[0])
		if len(s.filePaths) > 1 {
			name += fmt.Sprintf(" and %d more", len(s.filePaths)-1)
		}
	}

	// Combine metadata.
	metadata := make(map[string]interface{})
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata["source"] = "file"
	metadata["file_count"] = len(s.filePaths)
	metadata["file_paths"] = s.filePaths
	metadata["content_length"] = len(content)

	return &document.Document{
		ID:        id,
		Name:      name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// generateFileID generates a unique ID for the file source based on file paths.
func (s *Source) generateFileID() string {
	// Use first file path for ID generation.
	if len(s.filePaths) == 0 {
		return "file_empty"
	}

	filePath := s.filePaths[0]
	// Use absolute path for consistent IDs.
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}

	// Replace path separators with underscores and remove special characters.
	id := strings.ReplaceAll(absPath, string(filepath.Separator), "_")
	id = strings.ReplaceAll(id, ":", "")
	id = strings.ReplaceAll(id, " ", "_")

	if len(s.filePaths) > 1 {
		id += fmt.Sprintf("_and_%d_more", len(s.filePaths)-1)
	}

	return fmt.Sprintf("file_%s", id)
}
