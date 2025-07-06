// Package document provides document processing functionality for knowledge management.
package document

import (
	"time"
)

// Document represents a text document with metadata.
type Document struct {
	// ID is the unique identifier of the document.
	ID string `json:"id,omitempty"`

	// Name is the name or title of the document.
	Name string `json:"name,omitempty"`

	// Content is the text content of the document.
	Content string `json:"content"`

	// Metadata contains additional information about the document.
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// CreatedAt is the creation timestamp of the document.
	CreatedAt time.Time `json:"created_at,omitempty"`

	// UpdatedAt is the last update timestamp of the document.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// IsEmpty checks if the document has no content.
func (d *Document) IsEmpty() bool {
	if d == nil || d.Content == "" {
		return true
	}
	return false
}

// Clone creates a deep copy of the document.
func (d *Document) Clone() *Document {
	clone := &Document{
		ID:        d.ID,
		Name:      d.Name,
		Content:   d.Content,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}

	if d.Metadata != nil {
		clone.Metadata = make(map[string]interface{})
		for k, v := range d.Metadata {
			clone.Metadata[k] = v
		}
	}
	return clone
}
