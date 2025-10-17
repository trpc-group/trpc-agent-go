//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory vector store implementation.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

var (
	// errDocumentCannotBeNil is the error when the document is nil.
	errDocumentCannotBeNil = errors.New("document cannot be nil")
	// errDocumentIDCannotBeEmpty is the error when the document ID is empty.
	errDocumentIDCannotBeEmpty = errors.New("document ID cannot be empty")
	// errEmbeddingCannotBeEmpty is the error when the embedding is empty.
	errEmbeddingCannotBeEmpty = errors.New("embedding cannot be empty")

	// defaultMaxResults is the default maximum number of search results.
	defaultMaxResults = 10
)
var _ vectorstore.VectorStore = (*VectorStore)(nil)

// VectorStore implements vectorstore.VectorStore interface using in-memory storage.
type VectorStore struct {
	documents  map[string]*document.Document
	embeddings map[string][]float64
	mutex      sync.RWMutex

	// maxResults is the maximum number of search results.
	maxResults int

	filterConverter searchfilter.Converter[comparisonFunc]
}

// Option represents a functional option for configuring VectorStore.
type Option func(*VectorStore)

// WithMaxResults sets the maximum number of search results.
func WithMaxResults(maxResults int) Option {
	return func(vs *VectorStore) {
		if maxResults <= 0 {
			maxResults = defaultMaxResults
		}
		vs.maxResults = maxResults
	}
}

// New creates a new in-memory vector store instance with options.
func New(opts ...Option) *VectorStore {
	vs := &VectorStore{
		documents:       make(map[string]*document.Document),
		embeddings:      make(map[string][]float64),
		maxResults:      defaultMaxResults,
		filterConverter: &inmemoryConverter{},
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
		return errDocumentCannotBeNil
	}
	if doc.ID == "" {
		return errDocumentIDCannotBeEmpty
	}
	if len(embedding) == 0 {
		return errEmbeddingCannotBeEmpty
	}

	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	clonedDoc := doc.Clone()
	now := time.Now()
	if clonedDoc.CreatedAt.IsZero() {
		clonedDoc.CreatedAt = now
	}
	clonedDoc.UpdatedAt = now

	vs.documents[doc.ID] = clonedDoc
	vs.embeddings[doc.ID] = make([]float64, len(embedding))
	copy(vs.embeddings[doc.ID], embedding)

	return nil
}

// Get implements vectorstore.VectorStore interface.
func (vs *VectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	if id == "" {
		return nil, nil, errDocumentIDCannotBeEmpty
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
		return errDocumentCannotBeNil
	}
	if doc.ID == "" {
		return errDocumentIDCannotBeEmpty
	}
	if len(embedding) == 0 {
		return errEmbeddingCannotBeEmpty
	}

	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	existingDoc, exists := vs.documents[doc.ID]
	if !exists {
		return fmt.Errorf("document not found: %s", doc.ID)
	}

	clonedDoc := doc.Clone()
	// Preserve original creation time
	clonedDoc.CreatedAt = existingDoc.CreatedAt
	clonedDoc.UpdatedAt = time.Now()

	vs.documents[doc.ID] = clonedDoc
	vs.embeddings[doc.ID] = make([]float64, len(embedding))
	copy(vs.embeddings[doc.ID], embedding)

	return nil
}

// Delete implements vectorstore.VectorStore interface.
func (vs *VectorStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errDocumentIDCannotBeEmpty
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
		return nil, errors.New("query cannot be nil")
	}

	// Handle different search modes
	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	case vectorstore.SearchModeHybrid:
		// For in-memory implementation, hybrid mode falls back to vector search
		// since we don't have full-text search capabilities
		if len(query.Vector) == 0 {
			return nil, fmt.Errorf("query vector cannot be empty for hybrid search")
		}
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeKeyword:
		// For in-memory implementation, keyword search is not supported
		// Fall back to filter search
		return vs.searchByFilter(ctx, query)
	default:
		// Default behavior: require vector for backward compatibility
		if len(query.Vector) == 0 {
			return nil, fmt.Errorf("query vector cannot be empty")
		}
		return vs.searchByVector(ctx, query)
	}
}

// searchByVector performs vector similarity search
func (vs *VectorStore) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("query vector cannot be empty for vector search")
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
	limit := vs.getMaxResult(query.Limit)
	if len(results) > limit {
		results = results[:limit]
	}

	return &vectorstore.SearchResult{
		Results: results,
	}, nil
}

// searchByFilter performs filter-only search
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	vs.mutex.RLock()
	defer vs.mutex.RUnlock()

	var results []*vectorstore.ScoredDocument

	// Filter documents based on criteria
	for docID := range vs.documents {
		// Apply filter if specified
		if query.Filter != nil {
			if !vs.matchesFilter(docID, query.Filter) {
				continue
			}
		}

		// For filter-only search, assign a default score
		results = append(results, &vectorstore.ScoredDocument{
			Document: vs.documents[docID].Clone(),
			Score:    1.0, // Default score for filter matches
		})
	}

	// Sort by creation time (newest first) for filter-only search
	sort.Slice(results, func(i, j int) bool {
		return results[i].Document.CreatedAt.After(results[j].Document.CreatedAt)
	})

	// Apply limit
	limit := vs.getMaxResult(query.Limit)
	if len(results) > limit {
		results = results[:limit]
	}

	return &vectorstore.SearchResult{
		Results: results,
	}, nil
}

// DeleteByFilter deletes documents by filter.
func (vs *VectorStore) DeleteByFilter(
	ctx context.Context,
	opts ...vectorstore.DeleteOption,
) error {
	config := vectorstore.ApplyDeleteOptions(opts...)
	docIDs := config.DocumentIDs
	filters := config.Filter
	deleteAll := config.DeleteAll

	// Validate parameters - similar to tcvector's validation
	if deleteAll && (len(docIDs) > 0 || len(filters) > 0) {
		return fmt.Errorf("inmemory delete all documents, but document ids or filter are provided")
	}

	vs.mutex.Lock()
	defer vs.mutex.Unlock()

	// Handle delete all case
	if deleteAll {
		vs.documents = make(map[string]*document.Document)
		vs.embeddings = make(map[string][]float64)
		return nil
	}

	// Validate that at least one filter condition is provided
	if len(filters) == 0 && len(docIDs) == 0 {
		return fmt.Errorf("inmemory delete by filter: no filter conditions specified")
	}

	// Create a SearchFilter for reusing matchesFilter logic
	searchFilter := &vectorstore.SearchFilter{}
	if len(docIDs) > 0 {
		searchFilter.IDs = docIDs
	}
	if len(filters) > 0 {
		searchFilter.Metadata = filters
	}

	// Collect document IDs to delete
	var toDelete []string
	for docID := range vs.documents {
		if vs.matchesFilter(docID, searchFilter) {
			toDelete = append(toDelete, docID)
		}
	}

	// Delete the matched documents
	for _, docID := range toDelete {
		delete(vs.documents, docID)
		delete(vs.embeddings, docID)
	}

	return nil
}

// Count counts the number of documents in the vector store.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)
	filter := config.Filter

	vs.mutex.RLock()
	defer vs.mutex.RUnlock()

	// If no filter conditions, return total count
	if len(filter) == 0 {
		return len(vs.documents), nil
	}

	// Create a SearchFilter for reusing matchesFilter logic
	searchFilter := &vectorstore.SearchFilter{}
	if len(filter) > 0 {
		searchFilter.Metadata = filter
	}

	// Count documents that match the filter
	count := 0
	for docID := range vs.documents {
		if vs.matchesFilter(docID, searchFilter) {
			count++
		}
	}

	return count, nil
}

// GetMetadata retrieves metadata from the vector store with filtering and pagination support.
func (vs *VectorStore) GetMetadata(
	ctx context.Context,
	opts ...vectorstore.GetMetadataOption,
) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	vs.mutex.RLock()
	defer vs.mutex.RUnlock()

	// First, collect all matching documents
	var matchedDocs []string
	for docID := range vs.documents {
		// Check if document matches IDs filter
		if len(config.IDs) > 0 {
			found := false
			for _, id := range config.IDs {
				if docID == id {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Check if document matches metadata filter
		if len(config.Filter) > 0 {
			searchFilter := &vectorstore.SearchFilter{Metadata: config.Filter}
			if !vs.matchesFilter(docID, searchFilter) {
				continue
			}
		}

		matchedDocs = append(matchedDocs, docID)
	}

	result := make(map[string]vectorstore.DocumentMetadata)

	// Handle pagination
	limit := config.Limit
	offset := config.Offset

	// If limit < 0, return all matched documents (no pagination)
	if limit < 0 {
		for _, docID := range matchedDocs {
			if doc, exists := vs.documents[docID]; exists {
				result[docID] = vectorstore.DocumentMetadata{
					Metadata: doc.Metadata,
				}
			}
		}
		return result, nil
	}

	// Apply offset
	if offset >= len(matchedDocs) {
		return result, nil // Return empty result if offset exceeds total
	}

	// Apply limit
	end := offset + limit
	if end > len(matchedDocs) {
		end = len(matchedDocs)
	}

	// Get the paginated slice
	for i := offset; i < end; i++ {
		docID := matchedDocs[i]
		if doc, exists := vs.documents[docID]; exists {
			result[docID] = vectorstore.DocumentMetadata{
				Metadata: doc.Metadata,
			}
		}
	}

	return result, nil
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

// sortByScore sorts results by score in descending order using efficient sort algorithm.
func sortByScore(results []*vectorstore.ScoredDocument) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
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
			if docValue, exists := doc.Metadata[key]; !exists || !reflect.DeepEqual(docValue, value) {
				return false
			}
		}
	}

	if filter.FilterCondition != nil {
		condFunc, err := vs.filterConverter.Convert(filter.FilterCondition)
		if err != nil {
			return false
		}
		if condFunc != nil && !condFunc(doc) {
			return false
		}
	}

	return true
}

func (vs *VectorStore) getMaxResult(maxResults int) int {
	if maxResults <= 0 {
		return vs.maxResults
	}
	return maxResults
}
