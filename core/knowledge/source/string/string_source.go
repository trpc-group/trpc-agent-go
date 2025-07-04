// Package string provides string-based knowledge source implementation.
package string

import (
	"context"
	"crypto/md5"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Source represents a knowledge source for plain text content.
type Source struct {
	content  string
	name     string
	metadata map[string]interface{}
}

// New creates a new string knowledge source.
func New(content string, opts ...Option) *Source {
	source := &Source{
		content:  content,
		name:     "String Source", // Default name.
		metadata: make(map[string]interface{}),
	}

	// Apply options.
	for _, opt := range opts {
		opt(source)
	}

	return source
}

// ReadDocument reads the string content and returns a document.
func (s *Source) ReadDocument(ctx context.Context) (*document.Document, error) {
	if s.content == "" {
		return nil, fmt.Errorf("content cannot be empty")
	}

	return s.createDocument(), nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return "string"
}

// createDocument creates a document from the string content.
func (s *Source) createDocument() *document.Document {
	contentMD5 := md5.Sum([]byte(s.content))
	contentID := fmt.Sprintf("%x", contentMD5)

	now := time.Now()
	doc := &document.Document{
		ID:        contentID,
		Name:      fmt.Sprintf("String Document %s", contentID[:8]),
		Content:   s.content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Copy metadata.
	if s.metadata != nil {
		doc.Metadata = make(map[string]interface{})
		for k, v := range s.metadata {
			doc.Metadata[k] = v
		}
	} else {
		doc.Metadata = make(map[string]interface{})
	}

	// Add source-specific metadata.
	doc.Metadata["source"] = "string"
	doc.Metadata["content_length"] = len(s.content)

	return doc
}
