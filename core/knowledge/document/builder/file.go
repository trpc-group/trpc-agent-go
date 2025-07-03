// Package builder provides file document builder logic.
package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// FileOption represents a functional option for file document creation.
type FileOption func(*fileConfig)

// fileConfig holds configuration for file document creation.
type fileConfig struct {
	id       string
	name     string
	metadata map[string]interface{}
}

// WithFileID sets the document ID for file documents.
func WithFileID(id string) FileOption {
	return func(c *fileConfig) {
		c.id = id
	}
}

// WithFileName sets the document name for file documents.
func WithFileName(name string) FileOption {
	return func(c *fileConfig) {
		c.name = name
	}
}

// WithFileMetadata sets the document metadata for file documents.
func WithFileMetadata(metadata map[string]interface{}) FileOption {
	return func(c *fileConfig) {
		c.metadata = metadata
	}
}

// WithFileMetadataValue adds a single metadata key-value pair for file documents.
func WithFileMetadataValue(key string, value interface{}) FileOption {
	return func(c *fileConfig) {
		if c.metadata == nil {
			c.metadata = make(map[string]interface{})
		}
		c.metadata[key] = value
	}
}

// FromFile creates a document by loading content from a file.
func FromFile(ctx context.Context, filePath string, options ...FileOption) (*document.Document, error) {
	config := &fileConfig{
		metadata: make(map[string]interface{}),
	}
	// Apply options.
	for _, opt := range options {
		opt(config)
	}
	// Get file info.
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	// Check if it's a regular file.
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filePath)
	}
	// Read file content.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	// Generate ID if not provided.
	id := config.id
	if id == "" {
		id = generateFileDocumentID(filePath)
	}
	// Generate name if not provided.
	name := config.name
	if name == "" {
		name = filepath.Base(filePath)
	}
	// Set metadata.
	metadata := config.metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadata["source"] = "file"
	metadata["file_path"] = filePath
	metadata["file_name"] = filepath.Base(filePath)
	metadata["file_ext"] = filepath.Ext(filePath)
	metadata["file_size"] = fileInfo.Size()
	metadata["file_mode"] = fileInfo.Mode().String()
	metadata["modified_at"] = fileInfo.ModTime().UTC()
	metadata["content_length"] = len(content)
	return &document.Document{
		ID:        id,
		Name:      name,
		Content:   string(content),
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, nil
}

// FromDirectory loads multiple documents from files in a directory.
func FromDirectory(ctx context.Context, dirPath string, options ...FileOption) ([]*document.Document, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}
	var documents []*document.Document
	for _, entry := range entries {
		// Skip directories and hidden files.
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		filePath := filepath.Join(dirPath, entry.Name())
		// Load document from file.
		doc, err := FromFile(ctx, filePath, options...)
		if err != nil {
			// Log error but continue with other files.
			fmt.Printf("Warning: failed to load file %s: %v\n", filePath, err)
			continue
		}
		documents = append(documents, doc)
	}
	return documents, nil
}

// generateFileDocumentID generates a unique ID for a file document based on file path.
func generateFileDocumentID(filePath string) string {
	// Use absolute path for consistent IDs.
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}
	// Replace path separators with underscores and remove special characters.
	id := strings.ReplaceAll(absPath, string(filepath.Separator), "_")
	id = strings.ReplaceAll(id, ":", "")
	id = strings.ReplaceAll(id, " ", "_")
	return fmt.Sprintf("file_%s", id)
}
