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

// Package inmemory provides an in-memory vector store implementation.
package inmemory

import (
	"context"
	"fmt"
	"math"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// VectorStore implements vectorstore.VectorStore interface using in-memory storage.
type VectorStore struct {
	documents  map[string]*document.Document
	embeddings map[string][]float64
	mutex      sync.RWMutex
}

// Option represents a functional option for configuring VectorStore.
type Option func(*VectorStore)

// New creates a new in-memory vector store instance with options.
func New(opts ...Option) *VectorStore {
	vs := &VectorStore{
		documents:  make(map[string]*document.Document),
		embeddings: make(map[string][]float64),
	}

	// Apply options.
	for _, opt := range opts {
		opt(vs)
	}

	return vs
}

// Add implements vectorstore.VectorStore interface.
func (vs *VectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return fmt.Errorf("document cannot be nil")
	}
	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}

	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	vs.documents[doc.ID] = doc.Clone()
	vs.embeddings[doc.ID] = make([]float64, len(embedding))
	copy(vs.embeddings[doc.ID], embedding)

	return nil
}

// Get implements vectorstore.VectorStore interface.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, fmt.Errorf("document ID cannot be empty")
	}

	vs.mutex.RLock()
	defer vs.mutex.RUnlock()

	doc, exists := vs.documents[id]
	if !exists {
		return nil, nil, fmt.Errorf("document not found: %s", id)
	}

	embedding, exists := vs.embeddings[id]
	if !exists {
		return nil, nil, fmt.Errorf("embedding not found: %s", id)
	}

	embeddingCopy := make([]float64, len(embedding))
	copy(embeddingCopy, embedding)

	return doc.Clone(), embeddingCopy, nil
}

// Update implements vectorstore.VectorStore interface.
func (vs *VectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return fmt.Errorf("document cannot be nil")
	}
	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}

	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	if _, exists := vs.documents[doc.ID]; !exists {
		return fmt.Errorf("document not found: %s", doc.ID)
	}

	vs.documents[doc.ID] = doc.Clone()
	vs.embeddings[doc.ID] = make([]float64, len(embedding))
	copy(vs.embeddings[doc.ID], embedding)

	return nil
}

// Delete implements vectorstore.VectorStore interface.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("document ID cannot be empty")
	}

	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	if _, exists := vs.documents[id]; !exists {
		return fmt.Errorf("document not found: %s", id)
	}

	delete(vs.documents, id)
	delete(vs.embeddings, id)

	return nil
}

// Search implements vectorstore.VectorStore interface.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, fmt.Errorf("query cannot be nil")
	}
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("query vector cannot be empty")
	}

	vs.mutex.RLock()
	defer vs.mutex.RUnlock()

	var results []*vectorstore.ScoredDocument

	// Calculate similarity scores for all documents
	for docID, embedding := range vs.embeddings {
		// Skip if embedding dimensions don't match
		if len(embedding) != len(query.Vector) {
			continue
		}

		// Apply filter if specified
		if query.Filter != nil {
			if !vs.matchesFilter(docID, query.Filter) {
				continue
			}
		}

		// Calculate cosine similarity
		score := cosineSimilarity(query.Vector, embedding)

		// Apply minimum score threshold
		if score >= query.MinScore {
			results = append(results, &vectorstore.ScoredDocument{
				Document: vs.documents[docID].Clone(),
				Score:    score,
			})
		}
	}

	// Sort by score (descending)
	sortByScore(results)

	// Apply limit
	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return &vectorstore.SearchResult{
		Results: results,
	}, nil
}

// Close implements vectorstore.VectorStore interface.
func (vs *VectorStore) Close() error {
	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	vs.documents = nil
	vs.embeddings = nil

	return nil
}

// cosineSimilarity calculates the cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0.0
	}

	var dotProduct, normA, normB float64

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// sortByScore sorts results by score in descending order.
func sortByScore(results []*vectorstore.ScoredDocument) {
	// Simple bubble sort for small datasets.
	// For larger datasets, consider using sort.Slice.
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Score < results[j].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// matchesFilter checks if a document matches the given filter criteria.
func (vs *VectorStore) matchesFilter(docID string, filter *vectorstore.SearchFilter) bool {
	doc, exists := vs.documents[docID]
	if !exists {
		return false
	}

	// Check ID filter
	if len(filter.IDs) > 0 {
		found := false
		for _, id := range filter.IDs {
			if id == docID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check metadata filter
	if len(filter.Metadata) > 0 {
		if doc.Metadata == nil {
			return false
		}
		for key, value := range filter.Metadata {
			if docValue, exists := doc.Metadata[key]; !exists || docValue != value {
				return false
			}
		}
	}

	return true
}
