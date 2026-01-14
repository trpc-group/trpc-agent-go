//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package vectorstore provides interfaces for vector storage and similarity search.
package vectorstore

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

// VectorStore defines the interface for vector storage and similarity search operations.
type VectorStore interface {
	// Add stores a document with its embedding vector.
	Add(ctx context.Context, doc *document.Document, embedding []float64) error

	// Get retrieves a document by ID along with its embedding.
	Get(ctx context.Context, id string) (*document.Document, []float64, error)

	// Update modifies an existing document and its embedding.
	Update(ctx context.Context, doc *document.Document, embedding []float64) error

	// Delete removes a document and its embedding.
	Delete(ctx context.Context, id string) error

	// Search performs similarity search and returns the most similar documents.
	// Used for search tool
	Search(ctx context.Context, query *SearchQuery) (*SearchResult, error)

	// DeleteByFilter deletes documents by filter.
	DeleteByFilter(ctx context.Context, opts ...DeleteOption) error

	// UpdateByFilter updates documents matching the filter with the specified field values.
	// Supported fields: name, content, embedding, metadata.* (e.g., metadata.category, metadata.status)
	// Note: id, created_at cannot be updated via this method.
	UpdateByFilter(ctx context.Context, opts ...UpdateByFilterOption) (int64, error)

	// Count counts documents in the vector store.
	Count(ctx context.Context, opts ...CountOption) (int, error)

	// GetMetadata retrieves metadata from the vector store.
	GetMetadata(ctx context.Context, opts ...GetMetadataOption) (map[string]DocumentMetadata, error)

	// Close closes the vector store connection.
	Close() error
}

// DeleteOption represents a functional option for DeleteByFilter.
type DeleteOption func(*DeleteConfig)

// DeleteConfig holds the configuration for delete operations.
type DeleteConfig struct {
	DocumentIDs []string
	Filter      map[string]any
	DeleteAll   bool
}

// WithDeleteDocumentIDs sets the document IDs to delete.
func WithDeleteDocumentIDs(ids []string) DeleteOption {
	return func(c *DeleteConfig) {
		c.DocumentIDs = ids
	}
}

// WithDeleteFilter sets the filter for delete operations.
func WithDeleteFilter(filter map[string]any) DeleteOption {
	return func(c *DeleteConfig) {
		c.Filter = filter
	}
}

// WithDeleteAll enables deleting all matching documents.
func WithDeleteAll(deleteAll bool) DeleteOption {
	return func(c *DeleteConfig) {
		c.DeleteAll = deleteAll
	}
}

// CountOption represents a functional option for Count.
type CountOption func(*CountConfig)

// CountConfig holds the configuration for count operations.
type CountConfig struct {
	Filter map[string]any
}

// WithCountFilter sets the filter for count operations.
func WithCountFilter(filter map[string]any) CountOption {
	return func(c *CountConfig) {
		c.Filter = filter
	}
}

// GetMetadataOption represents a functional option for GetMetadata.
type GetMetadataOption func(*GetMetadataConfig)

// GetMetadataConfig holds the configuration for get metadata operations.
type GetMetadataConfig struct {
	IDs    []string
	Filter map[string]any
	Limit  int
	Offset int
}

// WithGetMetadataIDs sets the document IDs to retrieve metadata for.
func WithGetMetadataIDs(ids []string) GetMetadataOption {
	return func(c *GetMetadataConfig) {
		c.IDs = ids
	}
}

// WithGetMetadataFilter sets the filter for get metadata operations.
func WithGetMetadataFilter(filter map[string]any) GetMetadataOption {
	return func(c *GetMetadataConfig) {
		c.Filter = filter
	}
}

// WithGetMetadataLimit sets the limit for get metadata operations.
func WithGetMetadataLimit(limit int) GetMetadataOption {
	return func(c *GetMetadataConfig) {
		c.Limit = limit
	}
}

// WithGetMetadataOffset sets the offset for get metadata operations.
func WithGetMetadataOffset(offset int) GetMetadataOption {
	return func(c *GetMetadataConfig) {
		c.Offset = offset
	}
}

// UpdateByFilterOption represents a functional option for UpdateByFilter.
type UpdateByFilterOption func(*UpdateByFilterConfig)

// UpdateByFilterConfig holds the configuration for update by filter operations.
type UpdateByFilterConfig struct {
	// DocumentIDs filters documents by IDs.
	DocumentIDs []string
	// FilterCondition filters documents by universal filter conditions.
	FilterCondition *searchfilter.UniversalFilterCondition
	// Updates contains the field-value pairs to update.
	// Supported fields:
	//   - name: update document name
	//   - content: update document content
	//   - embedding: update document embedding vector (value must be []float64)
	//   - metadata.{key}: update specific metadata field (e.g., metadata.category, metadata.status)
	// Note: id, created_at fields cannot be updated.
	Updates map[string]any
}

// WithUpdateByFilterDocumentIDs sets the document IDs to filter.
func WithUpdateByFilterDocumentIDs(ids []string) UpdateByFilterOption {
	return func(c *UpdateByFilterConfig) {
		c.DocumentIDs = ids
	}
}

// WithUpdateByFilterCondition sets the filter condition for update operations.
func WithUpdateByFilterCondition(cond *searchfilter.UniversalFilterCondition) UpdateByFilterOption {
	return func(c *UpdateByFilterConfig) {
		c.FilterCondition = cond
	}
}

// WithUpdateByFilterUpdates sets the field-value pairs to update.
// Supported fields:
//   - name: update document name
//   - content: update document content
//   - embedding: update document embedding vector (value must be []float64)
//   - metadata.{key}: update specific metadata field (e.g., metadata.category, metadata.status)
//
// Note: id, created_at fields cannot be updated.
func WithUpdateByFilterUpdates(updates map[string]any) UpdateByFilterOption {
	return func(c *UpdateByFilterConfig) {
		c.Updates = updates
	}
}

// ApplyDeleteOptions parses delete options and returns a DeleteConfig.
func ApplyDeleteOptions(opts ...DeleteOption) *DeleteConfig {
	config := &DeleteConfig{}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// ApplyCountOptions parses count options and returns a CountConfig.
func ApplyCountOptions(opts ...CountOption) *CountConfig {
	config := &CountConfig{}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// ApplyUpdateByFilterOptions parses update by filter options and returns an UpdateByFilterConfig.
func ApplyUpdateByFilterOptions(opts ...UpdateByFilterOption) (*UpdateByFilterConfig, error) {
	config := &UpdateByFilterConfig{}
	for _, opt := range opts {
		opt(config)
	}

	// Validate: must have filter conditions
	if len(config.DocumentIDs) == 0 && config.FilterCondition == nil {
		return nil, fmt.Errorf("update by filter: no filter conditions specified (document IDs or filter condition required)")
	}

	// Validate: must have updates
	if len(config.Updates) == 0 {
		return nil, fmt.Errorf("update by filter: no updates specified")
	}

	return config, nil
}

// ApplyGetMetadataOptions parses get metadata options and returns a GetMetadataConfig.
func ApplyGetMetadataOptions(opts ...GetMetadataOption) (*GetMetadataConfig, error) {
	config := &GetMetadataConfig{
		Limit:  -1,
		Offset: -1,
	}
	for _, opt := range opts {
		opt(config)
	}

	if config.Limit == 0 {
		return nil, fmt.Errorf("get metadata limit should be greater than 0")
	}

	if config.Limit < 0 && config.Offset > 0 {
		return nil, fmt.Errorf("get metadata limit should be greater than 0 when offset is greater than 0")
	}

	if config.Limit > 0 && config.Offset < 0 {
		// reset offset to 0
		config.Offset = 0
	}

	return config, nil
}

// SearchQuery represents a vector similarity search query.
type SearchQuery struct {
	// Query is the original text query for hybrid search capabilities.
	Query string

	// Vector is the query embedding vector.
	Vector []float64

	// Limit specifies the number of top results to return.
	Limit int

	// MinScore specifies the minimum similarity score threshold.
	MinScore float64

	// Filter specifies additional filtering criteria.
	Filter *SearchFilter

	// SearchMode specifies the search mode.
	SearchMode SearchMode
}

// SearchMode specifies the search mode.
type SearchMode = int

const (
	// SearchModeHybrid is the default search mode.
	SearchModeHybrid SearchMode = iota
	// SearchModeVector is the vector search mode.
	SearchModeVector
	// SearchModeKeyword is the keyword search mode.
	SearchModeKeyword
	// SearchModeFilter is the filter search mode.
	SearchModeFilter
)

// SearchFilter represents filtering criteria for vector search.
type SearchFilter struct {
	// IDs filters results to specific document IDs.
	IDs []string
	// Metadata filters results by metadata key-value pairs.
	Metadata map[string]any

	// FilterCondition filters documents by universal filter conditions.
	FilterCondition *searchfilter.UniversalFilterCondition
}

// SearchResult represents the result of a vector similarity search.
type SearchResult struct {
	// Results contains the matching documents with their similarity scores.
	Results []*ScoredDocument
}

// ScoredDocument represents a document with its similarity score.
type ScoredDocument struct {
	// Document is the matched document.
	Document *document.Document

	// Score is the similarity score (0.0 to 1.0, higher is more similar).
	Score float64
}

// DocumentMetadata represents a document metadata.
type DocumentMetadata struct {
	// Metadata is the document metadata.
	Metadata map[string]any
}
