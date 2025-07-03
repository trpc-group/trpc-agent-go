// Package builder provides text document builder logic.
package builder

import (
	"crypto/md5"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// TextOption represents a functional option for text document creation.
type TextOption func(*textConfig)

// textConfig holds configuration for text document creation.
type textConfig struct {
	id       string
	name     string
	metadata map[string]interface{}
}

// WithTextID sets the document ID for text documents.
func WithTextID(id string) TextOption {
	return func(c *textConfig) {
		c.id = id
	}
}

// WithTextName sets the document name for text documents.
func WithTextName(name string) TextOption {
	return func(c *textConfig) {
		c.name = name
	}
}

// WithTextMetadata sets the document metadata for text documents.
func WithTextMetadata(metadata map[string]interface{}) TextOption {
	return func(c *textConfig) {
		c.metadata = metadata
	}
}

// WithTextMetadataValue adds a single metadata key-value pair for text documents.
func WithTextMetadataValue(key string, value interface{}) TextOption {
	return func(c *textConfig) {
		if c.metadata == nil {
			c.metadata = make(map[string]interface{})
		}
		c.metadata[key] = value
	}
}

// FromText creates a document from plain text content.
func FromText(content string, options ...TextOption) *document.Document {
	config := &textConfig{
		metadata: make(map[string]interface{}),
	}
	// Apply options.
	for _, opt := range options {
		opt(config)
	}
	// Generate ID if not provided.
	id := config.id
	if id == "" {
		id = generateTextDocumentID(content)
	}
	// Generate name if not provided.
	name := config.name
	if name == "" {
		name = generateTextDocumentName(content)
	}
	// Set default metadata.
	metadata := config.metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadata["source"] = "text"
	metadata["content_length"] = len(content)
	metadata["word_count"] = countWords(content)
	return &document.Document{
		ID:        id,
		Name:      name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// FromLines creates a document from a slice of text lines.
func FromLines(lines []string, options ...TextOption) *document.Document {
	content := strings.Join(lines, "\n")
	return FromText(content, options...)
}

// generateTextDocumentID generates a unique ID for a text document based on content.
func generateTextDocumentID(content string) string {
	hash := md5.Sum([]byte(content))
	return fmt.Sprintf("text_%x", hash[:8]) // Use first 8 bytes for shorter ID
}

// generateTextDocumentName generates a name for a text document based on content.
func generateTextDocumentName(content string) string {
	// Extract first sentence or first 50 characters as name.
	lines := strings.Split(content, "\n")
	firstLine := strings.TrimSpace(lines[0])
	if firstLine == "" && len(lines) > 1 {
		firstLine = strings.TrimSpace(lines[1])
	}
	if len(firstLine) > 50 {
		firstLine = firstLine[:50] + "..."
	}
	if firstLine == "" {
		return "Untitled Document"
	}
	return firstLine
}

// countWords counts the number of words in the text.
func countWords(text string) int {
	words := strings.Fields(text)
	return len(words)
} 