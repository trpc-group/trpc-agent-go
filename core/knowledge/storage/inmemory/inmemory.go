// Package inmemory provides an in-memory storage implementation for documents.
package inmemory

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/storage"
)

// Storage implements storage.Storage interface using in-memory storage.
type Storage struct {
	documents map[string]*document.Document
	mutex     sync.RWMutex
}

// Option represents a functional option for configuring Storage.
type Option func(*Storage)

// New creates a new in-memory storage instance with options.
func New(opts ...Option) *Storage {
	s := &Storage{
		documents: make(map[string]*document.Document),
	}

	// Apply options.
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Store implements storage.Storage interface.
func (s *Storage) Store(ctx context.Context, doc *document.Document) error {
	if doc == nil {
		return fmt.Errorf("document cannot be nil")
	}
	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.documents[doc.ID] = doc.Clone()
	return nil
}

// Get implements storage.Storage interface.
func (s *Storage) Get(ctx context.Context, id string) (*document.Document, error) {
	if id == "" {
		return nil, fmt.Errorf("document ID cannot be empty")
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	doc, exists := s.documents[id]
	if !exists {
		return nil, fmt.Errorf("document not found: %s", id)
	}

	return doc.Clone(), nil
}

// Update implements storage.Storage interface.
func (s *Storage) Update(ctx context.Context, doc *document.Document) error {
	if doc == nil {
		return fmt.Errorf("document cannot be nil")
	}
	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.documents[doc.ID]; !exists {
		return fmt.Errorf("document not found: %s", doc.ID)
	}

	s.documents[doc.ID] = doc.Clone()
	return nil
}

// Delete implements storage.Storage interface.
func (s *Storage) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("document ID cannot be empty")
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.documents[id]; !exists {
		return fmt.Errorf("document not found: %s", id)
	}

	delete(s.documents, id)
	return nil
}

// List implements storage.Storage interface.
func (s *Storage) List(ctx context.Context, filter *storage.Filter) ([]*document.Document, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var results []*document.Document

	// Apply ID filter if specified
	if filter != nil && len(filter.IDs) > 0 {
		idSet := make(map[string]bool)
		for _, id := range filter.IDs {
			idSet[id] = true
		}

		for _, doc := range s.documents {
			if idSet[doc.ID] {
				results = append(results, doc.Clone())
			}
		}
	} else {
		// Return all documents
		for _, doc := range s.documents {
			results = append(results, doc.Clone())
		}
	}

	// Apply metadata filter if specified
	if filter != nil && len(filter.Metadata) > 0 {
		filtered := results[:0]
		for _, doc := range results {
			if s.matchesMetadata(doc, filter.Metadata) {
				filtered = append(filtered, doc)
			}
		}
		results = filtered
	}

	// Apply limit and offset
	if filter != nil {
		start := filter.Offset
		if start >= len(results) {
			results = nil
		} else {
			end := start + filter.Limit
			if end > len(results) {
				end = len(results)
			}
			results = results[start:end]
		}
	}

	return results, nil
}

// Close implements storage.Storage interface.
func (s *Storage) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.documents = nil
	return nil
}

// matchesMetadata checks if a document matches the given metadata filter.
func (s *Storage) matchesMetadata(doc *document.Document, metadata map[string]interface{}) bool {
	if doc.Metadata == nil {
		return len(metadata) == 0
	}

	for key, value := range metadata {
		if docValue, exists := doc.Metadata[key]; !exists || docValue != value {
			return false
		}
	}

	return true
}
