//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func buildEmbedding(vals ...float64) []float64 {
	return vals
}

func TestVectorStore_CRUDAndSearch(t *testing.T) {
	ctx := context.Background()
	store := New()

	doc1 := &document.Document{
		ID:      "doc1",
		Content: "hello world",
		Metadata: map[string]any{
			"lang": "en",
		},
	}
	embedding1 := buildEmbedding(0.1, 0.2, 0.3)

	// Add document.
	require.NoError(t, store.Add(ctx, doc1, embedding1))

	// Get document.
	gotDoc, gotEmb, err := store.Get(ctx, "doc1")
	require.NoError(t, err)
	require.Equal(t, doc1.Content, gotDoc.Content)
	require.Equal(t, embedding1, gotEmb)

	// Update document.
	updatedDoc := &document.Document{
		ID:      "doc1",
		Content: "hello updated",
		Metadata: map[string]any{
			"lang": "en",
		},
	}
	updatedEmb := buildEmbedding(0.2, 0.2, 0.2)
	require.NoError(t, store.Update(ctx, updatedDoc, updatedEmb))

	// Search with metadata filter.
	query := &vectorstore.SearchQuery{
		Vector:   buildEmbedding(0.2, 0.2, 0.2),
		Limit:    5,
		MinScore: 0.0,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{
				"lang": "en",
			},
		},
	}

	result, err := store.Search(ctx, query)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.GreaterOrEqual(t, len(result.Results), 1)
	require.Equal(t, "doc1", result.Results[0].Document.ID)

	// Delete document.
	require.NoError(t, store.Delete(ctx, "doc1"))
	_, _, err = store.Get(ctx, "doc1")
	require.Error(t, err)
}

func TestCosineSimilarity(t *testing.T) {
	identical := cosineSimilarity([]float64{1, 0}, []float64{1, 0})
	require.InEpsilon(t, 1.0, identical, 1e-9)

	orthogonal := cosineSimilarity([]float64{1, 0}, []float64{0, 1})
	require.Equal(t, 0.0, orthogonal)

	diffDim := cosineSimilarity([]float64{1, 0}, []float64{1})
	require.Equal(t, 0.0, diffDim)
}

func TestSortByScore(t *testing.T) {
	docs := []*vectorstore.ScoredDocument{
		{Document: &document.Document{ID: "d1"}, Score: 0.2},
		{Document: &document.Document{ID: "d2"}, Score: 0.9},
		{Document: &document.Document{ID: "d3"}, Score: 0.5},
	}

	sortByScore(docs)

	require.Equal(t, "d2", docs[0].Document.ID)
	require.Equal(t, "d3", docs[1].Document.ID)
	require.Equal(t, "d1", docs[2].Document.ID)
}

func TestMatchesFilter(t *testing.T) {
	ctx := context.Background()
	store := New()
	doc := &document.Document{
		ID:      "doc1",
		Content: "data",
		Metadata: map[string]any{
			"type": "test",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}
	require.NoError(t, store.Add(ctx, doc, embedding))

	// Match by ID
	filterByID := &vectorstore.SearchFilter{IDs: []string{"doc1"}}
	require.True(t, store.matchesFilter("doc1", filterByID))

	// Non-matching ID
	filterWrongID := &vectorstore.SearchFilter{IDs: []string{"other"}}
	require.False(t, store.matchesFilter("doc1", filterWrongID))

	// Match by metadata
	filterByMeta := &vectorstore.SearchFilter{Metadata: map[string]any{"type": "test"}}
	require.True(t, store.matchesFilter("doc1", filterByMeta))

	// Non-matching metadata
	filterWrongMeta := &vectorstore.SearchFilter{Metadata: map[string]any{"type": "prod"}}
	require.False(t, store.matchesFilter("doc1", filterWrongMeta))

	// Combined filter (AND logic)
	combinedFilter := &vectorstore.SearchFilter{
		IDs:      []string{"doc1"},
		Metadata: map[string]any{"type": "test"},
		FilterCondition: &searchfilter.UniversalFilterCondition{
			Field:    "metadata.type",
			Operator: searchfilter.OperatorEqual,
			Value:    "test",
		},
	}
	require.True(t, store.matchesFilter("doc1", combinedFilter))

	// Combined filter non-match
	combinedFilterNonMatch := &vectorstore.SearchFilter{
		IDs:      []string{"doc1"},
		Metadata: map[string]any{"type": "prod"},
		FilterCondition: &searchfilter.UniversalFilterCondition{
			Field:    "metadata.type",
			Operator: searchfilter.OperatorEqual,
			Value:    "no-test",
		},
	}
	require.False(t, store.matchesFilter("doc1", combinedFilterNonMatch))
}

func TestVectorStore_ErrorPathsAndClose(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Get non-existent ID.
	_, _, err := store.Get(ctx, "missing")
	require.Error(t, err)

	// Delete non-existent ID.
	err = store.Delete(ctx, "missing")
	require.Error(t, err)

	// Search with empty vector.
	_, err = store.Search(ctx, &vectorstore.SearchQuery{Vector: []float64{}})
	require.Error(t, err)

	// Close store.
	require.NoError(t, store.Close())
}

func TestVectorStore_DeleteByFilter(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add test documents
	doc1 := &document.Document{
		ID:      "doc1",
		Content: "test content 1",
		Metadata: map[string]any{
			"lang": "en",
			"type": "test",
		},
	}
	doc2 := &document.Document{
		ID:      "doc2",
		Content: "test content 2",
		Metadata: map[string]any{
			"lang": "fr",
			"type": "test",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}

	require.NoError(t, store.Add(ctx, doc1, embedding))
	require.NoError(t, store.Add(ctx, doc2, embedding))

	// Test delete by document IDs
	err := store.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"doc1"}))
	require.NoError(t, err)

	// Verify doc1 is deleted, doc2 still exists
	_, _, err = store.Get(ctx, "doc1")
	require.Error(t, err)
	_, _, err = store.Get(ctx, "doc2")
	require.NoError(t, err)

	// Test delete by metadata filter
	err = store.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(map[string]any{"lang": "fr"}))
	require.NoError(t, err)

	// Verify doc2 is deleted
	_, _, err = store.Get(ctx, "doc2")
	require.Error(t, err)

	// Test delete all
	require.NoError(t, store.Add(ctx, doc1, embedding))
	require.NoError(t, store.Add(ctx, doc2, embedding))

	err = store.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true))
	require.NoError(t, err)

	// Verify all documents are deleted
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// Test error cases
	err = store.DeleteByFilter(ctx) // No filter conditions
	require.Error(t, err)

	// Test invalid combination
	err = store.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true), vectorstore.WithDeleteFilter(map[string]any{"key": "value"}))
	require.Error(t, err)
}

func TestVectorStore_Count(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add test documents
	doc1 := &document.Document{
		ID:      "doc1",
		Content: "test content 1",
		Metadata: map[string]any{
			"lang": "en",
		},
	}
	doc2 := &document.Document{
		ID:      "doc2",
		Content: "test content 2",
		Metadata: map[string]any{
			"lang": "fr",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}

	// Empty store
	count, err := store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	require.NoError(t, store.Add(ctx, doc1, embedding))
	require.NoError(t, store.Add(ctx, doc2, embedding))

	// Total count
	count, err = store.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	// Count with filter
	count, err = store.Count(ctx, vectorstore.WithCountFilter(map[string]any{"lang": "en"}))
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Count with no matches
	count, err = store.Count(ctx, vectorstore.WithCountFilter(map[string]any{"lang": "de"}))
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestVectorStore_GetMetadata(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add test documents
	doc1 := &document.Document{
		ID:      "doc1",
		Content: "test content 1",
		Metadata: map[string]any{
			"lang": "en",
			"type": "test",
		},
	}
	doc2 := &document.Document{
		ID:      "doc2",
		Content: "test content 2",
		Metadata: map[string]any{
			"lang": "fr",
			"type": "prod",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}

	require.NoError(t, store.Add(ctx, doc1, embedding))
	require.NoError(t, store.Add(ctx, doc2, embedding))

	// Get all metadata
	metadata, err := store.GetMetadata(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(metadata))
	require.Equal(t, doc1.Metadata, metadata["doc1"].Metadata)
	require.Equal(t, doc2.Metadata, metadata["doc2"].Metadata)

	// Get metadata with IDs filter
	metadata, err = store.GetMetadata(ctx, vectorstore.WithGetMetadataIDs([]string{"doc1"}))
	require.NoError(t, err)
	require.Equal(t, 1, len(metadata))
	require.Equal(t, doc1.Metadata, metadata["doc1"].Metadata)

	// Get metadata with metadata filter
	metadata, err = store.GetMetadata(ctx, vectorstore.WithGetMetadataFilter(map[string]any{"lang": "en"}))
	require.NoError(t, err)
	require.Equal(t, 1, len(metadata))
	require.Equal(t, doc1.Metadata, metadata["doc1"].Metadata)

	// Get metadata with limit and offset
	metadata, err = store.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(1), vectorstore.WithGetMetadataOffset(1))
	require.NoError(t, err)
	require.Equal(t, 1, len(metadata))

	// Get metadata with combined filters
	metadata, err = store.GetMetadata(ctx,
		vectorstore.WithGetMetadataFilter(map[string]any{"type": "test"}),
		vectorstore.WithGetMetadataLimit(10),
		vectorstore.WithGetMetadataOffset(0))
	require.NoError(t, err)
	require.Equal(t, 1, len(metadata))
	require.Equal(t, doc1.Metadata, metadata["doc1"].Metadata)

	// Test error cases
	_, err = store.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(0))
	require.Error(t, err)

	_, err = store.GetMetadata(ctx, vectorstore.WithGetMetadataLimit(-1), vectorstore.WithGetMetadataOffset(10))
	require.Error(t, err)
}

func TestVectorStore_SearchModes(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add test documents
	doc1 := &document.Document{
		ID:      "doc1",
		Content: "hello world",
		Metadata: map[string]any{
			"lang": "en",
		},
	}
	doc2 := &document.Document{
		ID:      "doc2",
		Content: "bonjour monde",
		Metadata: map[string]any{
			"lang": "fr",
		},
	}
	embedding1 := []float64{0.9, 0.1, 0.2}
	embedding2 := []float64{0.1, 0.9, 0.2}

	require.NoError(t, store.Add(ctx, doc1, embedding1))
	require.NoError(t, store.Add(ctx, doc2, embedding2))

	// Test vector search mode
	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		Vector:     []float64{0.9, 0.1, 0.2},
		Limit:      5,
		SearchMode: vectorstore.SearchModeVector,
		MinScore:   0.5,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Equal(t, "doc1", result.Results[0].Document.ID)
	require.InEpsilon(t, 1.0, result.Results[0].Score, 1e-9)

	// Test filter search mode
	result, err = store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{"lang": "fr"},
		},
		Limit: 5,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Equal(t, "doc2", result.Results[0].Document.ID)
	require.Equal(t, 1.0, result.Results[0].Score)

	// Test hybrid search mode (falls back to vector search)
	result, err = store.Search(ctx, &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.9, 0.2},
		SearchMode: vectorstore.SearchModeHybrid,
		Limit:      5,
		MinScore:   0.5,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Equal(t, "doc2", result.Results[0].Document.ID)
	require.InEpsilon(t, 1.0, result.Results[0].Score, 1e-9)

	// Test keyword search mode (falls back to filter search)
	result, err = store.Search(ctx, &vectorstore.SearchQuery{
		Query:      "bonjour",
		SearchMode: vectorstore.SearchModeKeyword,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{"lang": "fr"},
		},
		Limit: 5,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	require.Equal(t, "doc2", result.Results[0].Document.ID)
}

// TestVectorStore_Add_EdgeCases tests Add method with edge cases
func TestVectorStore_Add_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		embedding []float64
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_normal_document",
			doc: &document.Document{
				ID:       "doc1",
				Name:     "Test",
				Content:  "Content",
				Metadata: map[string]any{"key": "value"},
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   true,
			errMsg:    "document cannot be nil",
		},
		{
			name: "empty_document_id",
			doc: &document.Document{
				Content: "Content",
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   true,
			errMsg:    "document ID cannot be empty",
		},
		{
			name: "empty_embedding",
			doc: &document.Document{
				ID:      "doc2",
				Content: "Content",
			},
			embedding: []float64{},
			wantErr:   true,
			errMsg:    "embedding cannot be empty",
		},
		{
			name: "very_long_content",
			doc: &document.Document{
				ID:      "long_doc",
				Content: string(make([]byte, 100000)), // 100KB
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
		{
			name: "unicode_content",
			doc: &document.Document{
				ID:      "unicode_doc",
				Content: "测试内容 тест содержание test content 🚀",
				Name:    "测试文档",
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
		{
			name: "special_characters_in_id",
			doc: &document.Document{
				ID:      "doc-with-special.chars_123",
				Content: "Content",
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
		{
			name: "nil_metadata",
			doc: &document.Document{
				ID:       "doc_nil_meta",
				Content:  "Content",
				Metadata: nil,
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			err := store.Add(context.Background(), tt.doc, tt.embedding)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestVectorStore_Update_EdgeCases tests Update method with edge cases
func TestVectorStore_Update_EdgeCases(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Pre-add a document
	originalDoc := &document.Document{
		ID:       "doc1",
		Name:     "Original",
		Content:  "Original Content",
		Metadata: map[string]any{"version": 1},
	}
	require.NoError(t, store.Add(ctx, originalDoc, []float64{0.1, 0.2, 0.3}))

	tests := []struct {
		name      string
		doc       *document.Document
		embedding []float64
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_update",
			doc: &document.Document{
				ID:       "doc1",
				Name:     "Updated",
				Content:  "Updated Content",
				Metadata: map[string]any{"version": 2},
			},
			embedding: []float64{0.9, 0.8, 0.7},
			wantErr:   false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   true,
			errMsg:    "document cannot be nil",
		},
		{
			name: "empty_document_id",
			doc: &document.Document{
				Name: "Test",
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   true,
			errMsg:    "document ID cannot be empty",
		},
		{
			name: "empty_embedding",
			doc: &document.Document{
				ID:   "doc1",
				Name: "Test",
			},
			embedding: []float64{},
			wantErr:   true,
			errMsg:    "embedding cannot be empty",
		},
		{
			name: "nonexistent_document",
			doc: &document.Document{
				ID:      "nonexistent",
				Name:    "Test",
				Content: "Content",
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   true,
			errMsg:    "document not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Update(ctx, tt.doc, tt.embedding)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				// Verify update was applied
				gotDoc, gotEmb, err := store.Get(ctx, tt.doc.ID)
				require.NoError(t, err)
				assert.Equal(t, tt.doc.Name, gotDoc.Name)
				assert.Equal(t, tt.doc.Content, gotDoc.Content)
				assert.Equal(t, tt.embedding, gotEmb)
			}
		})
	}
}

// TestVectorStore_Delete_EdgeCases tests Delete method with edge cases
func TestVectorStore_Delete_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		docID   string
		setup   func(*VectorStore)
		wantErr bool
		errMsg  string
	}{
		{
			name:  "success_delete",
			docID: "doc1",
			setup: func(vs *VectorStore) {
				doc := &document.Document{ID: "doc1", Content: "Content"}
				_ = vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3})
			},
			wantErr: false,
		},
		{
			name:    "empty_id",
			docID:   "",
			setup:   func(vs *VectorStore) {},
			wantErr: true,
			errMsg:  "document ID cannot be empty",
		},
		{
			name:    "nonexistent_document",
			docID:   "nonexistent",
			setup:   func(vs *VectorStore) {},
			wantErr: true,
			errMsg:  "document not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			tt.setup(store)

			err := store.Delete(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				// Verify document is deleted
				_, _, err := store.Get(context.Background(), tt.docID)
				require.Error(t, err)
			}
		})
	}
}

// TestVectorStore_Search_EdgeCases tests Search with edge cases
func TestVectorStore_Search_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		query    *vectorstore.SearchQuery
		setup    func(*VectorStore)
		wantErr  bool
		errMsg   string
		validate func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name:    "nil_query",
			query:   nil,
			setup:   func(vs *VectorStore) {},
			wantErr: true,
			errMsg:  "query cannot be nil",
		},
		{
			name: "empty_vector_in_vector_mode",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				SearchMode: vectorstore.SearchModeVector,
			},
			setup:   func(vs *VectorStore) {},
			wantErr: true,
			errMsg:  "vector cannot be empty",
		},
		{
			name: "zero_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.0, 0.0, 0.0},
				SearchMode: vectorstore.SearchModeVector,
			},
			setup: func(vs *VectorStore) {
				doc := &document.Document{ID: "doc1", Content: "Content"}
				_ = vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "nan_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{math.NaN(), 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
			},
			setup: func(vs *VectorStore) {
				doc := &document.Document{ID: "doc1", Content: "Content"}
				_ = vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "inf_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{math.Inf(1), 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
			},
			setup: func(vs *VectorStore) {
				doc := &document.Document{ID: "doc1", Content: "Content"}
				_ = vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				assert.NotNil(t, result)
			},
		},
		{
			name: "dimension_mismatch",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3, 0.4}, // 4D
				SearchMode: vectorstore.SearchModeVector,
			},
			setup: func(vs *VectorStore) {
				doc := &document.Document{ID: "doc1", Content: "Content"}
				_ = vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3}) // 3D
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				// Dimension mismatch should skip documents, return empty
				assert.NotNil(t, result)
				assert.Equal(t, 0, len(result.Results))
			},
		},
		{
			name: "high_min_score",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{0.1, 0.2, 0.3},
				SearchMode: vectorstore.SearchModeVector,
				MinScore:   0.99, // Very high threshold
			},
			setup: func(vs *VectorStore) {
				doc := &document.Document{ID: "doc1", Content: "Content"}
				_ = vs.Add(context.Background(), doc, []float64{0.5, 0.6, 0.7})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				// High threshold should filter out results
				assert.NotNil(t, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			tt.setup(store)

			result, err := store.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

// TestVectorStore_Search_Limit tests search limit functionality
func TestVectorStore_Search_Limit(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add multiple documents
	for i := 0; i < 20; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("doc_%d", i),
			Content: fmt.Sprintf("Content %d", i),
		}
		embedding := []float64{float64(i) / 20.0, 0.5, 0.2}
		require.NoError(t, store.Add(ctx, doc, embedding))
	}

	tests := []struct {
		name        string
		limit       int
		expectCount int
	}{
		{"limit_5", 5, 5},
		{"limit_10", 10, 10},
		{"limit_0_uses_default", 0, 10}, // Should use default (10)
		{"limit_negative_uses_default", -1, 10},
		{"limit_100_returns_all", 100, 20}, // More than available
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &vectorstore.SearchQuery{
				Vector:     []float64{0.5, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      tt.limit,
				MinScore:   0.0,
			}

			result, err := store.Search(ctx, query)
			require.NoError(t, err)
			assert.LessOrEqual(t, len(result.Results), tt.expectCount)
		})
	}
}

// TestVectorStore_Search_SortingOrder tests that results are sorted by score
func TestVectorStore_Search_SortingOrder(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add documents with varying similarity
	docs := []struct {
		id        string
		embedding []float64
	}{
		{"low_similarity", []float64{0.1, 0.1, 0.1}},
		{"high_similarity", []float64{1.0, 0.0, 0.0}},
		{"medium_similarity", []float64{0.5, 0.5, 0.0}},
	}

	for _, d := range docs {
		doc := &document.Document{ID: d.id, Content: "Content"}
		require.NoError(t, store.Add(ctx, doc, d.embedding))
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{1.0, 0.0, 0.0}, // Match high_similarity best
		SearchMode: vectorstore.SearchModeVector,
		Limit:      10,
		MinScore:   0.0,
	}

	result, err := store.Search(ctx, query)
	require.NoError(t, err)
	require.Equal(t, 3, len(result.Results))

	// Verify results are sorted by score descending
	assert.Equal(t, "high_similarity", result.Results[0].Document.ID)
	assert.Greater(t, result.Results[0].Score, result.Results[1].Score)
	assert.Greater(t, result.Results[1].Score, result.Results[2].Score)
}

// TestVectorStore_ConcurrentOperations tests concurrent access
func TestVectorStore_ConcurrentOperations(t *testing.T) {
	store := New()
	ctx := context.Background()
	numGoroutines := 20

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*3)

	// Concurrent adds
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			doc := &document.Document{
				ID:      fmt.Sprintf("doc_%d", idx),
				Content: fmt.Sprintf("Content %d", idx),
			}
			embedding := []float64{float64(idx) / 20.0, 0.5, 0.2}
			if err := store.Add(ctx, doc, embedding); err != nil {
				errChan <- err
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			docID := fmt.Sprintf("doc_%d", idx)
			// May or may not exist yet
			_, _, _ = store.Get(ctx, docID)
		}(i)
	}

	// Concurrent searches
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := &vectorstore.SearchQuery{
				Vector:     []float64{0.5, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      5,
			}
			_, _ = store.Search(ctx, query)
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent operation failed: %v", err)
	}

	// Verify all documents were added
	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, numGoroutines, count)
}

// TestVectorStore_MultipleUpdates tests multiple updates to same document
func TestVectorStore_MultipleUpdates(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add initial document
	doc := &document.Document{
		ID:       "doc1",
		Name:     "Version 1",
		Content:  "Content 1",
		Metadata: map[string]any{"version": 1},
	}
	require.NoError(t, store.Add(ctx, doc, []float64{0.1, 0.2, 0.3}))

	// Update multiple times
	for i := 2; i <= 5; i++ {
		updatedDoc := &document.Document{
			ID:       "doc1",
			Name:     fmt.Sprintf("Version %d", i),
			Content:  fmt.Sprintf("Content %d", i),
			Metadata: map[string]any{"version": i},
		}
		embedding := []float64{float64(i) / 10.0, 0.2, 0.3}
		require.NoError(t, store.Update(ctx, updatedDoc, embedding))

		// Verify each update
		gotDoc, gotEmb, err := store.Get(ctx, "doc1")
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("Version %d", i), gotDoc.Name)
		assert.Equal(t, embedding, gotEmb)
		assert.Equal(t, i, gotDoc.Metadata["version"])
	}
}

// TestVectorStore_AddDuplicateID tests adding document with duplicate ID
func TestVectorStore_AddDuplicateID(t *testing.T) {
	ctx := context.Background()
	store := New()

	doc1 := &document.Document{
		ID:      "doc1",
		Name:    "First",
		Content: "First content",
	}
	require.NoError(t, store.Add(ctx, doc1, []float64{0.1, 0.2, 0.3}))

	// Add again with same ID but different content
	doc2 := &document.Document{
		ID:      "doc1",
		Name:    "Second",
		Content: "Second content",
	}
	require.NoError(t, store.Add(ctx, doc2, []float64{0.9, 0.8, 0.7}))

	// Verify latest document overwrites previous
	gotDoc, gotEmb, err := store.Get(ctx, "doc1")
	require.NoError(t, err)
	assert.Equal(t, "Second", gotDoc.Name)
	assert.Equal(t, "Second content", gotDoc.Content)
	assert.Equal(t, []float64{0.9, 0.8, 0.7}, gotEmb)

	// Verify count is still 1
	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestVectorStore_Search_WithFilters tests combining filters
func TestVectorStore_Search_WithFilters(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add documents with various metadata
	docs := []struct {
		id       string
		metadata map[string]any
		vector   []float64
	}{
		{
			"doc1",
			map[string]any{"category": "AI", "priority": 8, "status": "active"},
			[]float64{1.0, 0.0, 0.0},
		},
		{
			"doc2",
			map[string]any{"category": "ML", "priority": 5, "status": "active"},
			[]float64{0.9, 0.1, 0.0},
		},
		{
			"doc3",
			map[string]any{"category": "Data", "priority": 3, "status": "inactive"},
			[]float64{0.8, 0.2, 0.0},
		},
	}

	for _, d := range docs {
		doc := &document.Document{
			ID:       d.id,
			Content:  "Content",
			Metadata: d.metadata,
		}
		require.NoError(t, store.Add(ctx, doc, d.vector))
	}

	tests := []struct {
		name         string
		filter       map[string]any
		expectIDs    []string
		expectMinLen int
	}{
		{
			name:         "filter_by_category",
			filter:       map[string]any{"category": "AI"},
			expectIDs:    []string{"doc1"},
			expectMinLen: 1,
		},
		{
			name:         "filter_by_status",
			filter:       map[string]any{"status": "active"},
			expectIDs:    []string{"doc1", "doc2"},
			expectMinLen: 2,
		},
		{
			name:         "filter_by_multiple_fields",
			filter:       map[string]any{"category": "ML", "priority": 5},
			expectIDs:    []string{"doc2"},
			expectMinLen: 1,
		},
		{
			name:         "filter_no_match",
			filter:       map[string]any{"category": "Unknown"},
			expectIDs:    []string{},
			expectMinLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.0, 0.0},
				SearchMode: vectorstore.SearchModeVector,
				Filter: &vectorstore.SearchFilter{
					Metadata: tt.filter,
				},
				Limit:    10,
				MinScore: 0.0,
			}

			result, err := store.Search(ctx, query)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(result.Results), tt.expectMinLen)

			// Verify expected IDs are present
			if len(tt.expectIDs) > 0 {
				foundIDs := make(map[string]bool)
				for _, r := range result.Results {
					foundIDs[r.Document.ID] = true
				}
				for _, expectedID := range tt.expectIDs {
					assert.True(t, foundIDs[expectedID], "Expected ID %s not found", expectedID)
				}
			}
		})
	}
}

// TestVectorStore_WithMaxResults tests custom max results option
func TestVectorStore_WithMaxResults(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		maxResults     int
		addDocs        int
		queryLimit     int
		expectedMaxLen int
	}{
		{"default_max_results", 0, 20, 0, 10}, // Default is 10
		{"custom_max_results_5", 5, 20, 0, 5},
		{"custom_max_results_20", 20, 15, 0, 15}, // Less than max available
		{"query_limit_overrides", 10, 20, 3, 3},  // Query limit takes precedence
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var store *VectorStore
			if tt.maxResults > 0 {
				store = New(WithMaxResults(tt.maxResults))
			} else {
				store = New()
			}

			// Add documents
			for i := 0; i < tt.addDocs; i++ {
				doc := &document.Document{
					ID:      fmt.Sprintf("doc_%d", i),
					Content: fmt.Sprintf("Content %d", i),
				}
				embedding := []float64{float64(i) / 100.0, 0.5, 0.2}
				require.NoError(t, store.Add(ctx, doc, embedding))
			}

			query := &vectorstore.SearchQuery{
				Vector:     []float64{0.5, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      tt.queryLimit,
				MinScore:   0.0,
			}

			result, err := store.Search(ctx, query)
			require.NoError(t, err)
			assert.LessOrEqual(t, len(result.Results), tt.expectedMaxLen)
		})
	}
}

// TestVectorStore_DeleteMultiple tests deleting multiple documents
func TestVectorStore_DeleteMultiple(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add multiple documents
	numDocs := 10
	for i := 0; i < numDocs; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("doc_%d", i),
			Content: fmt.Sprintf("Content %d", i),
		}
		embedding := []float64{float64(i) / 10.0, 0.5, 0.2}
		require.NoError(t, store.Add(ctx, doc, embedding))
	}

	// Delete half of them
	for i := 0; i < numDocs/2; i++ {
		require.NoError(t, store.Delete(ctx, fmt.Sprintf("doc_%d", i)))
	}

	// Verify deleted documents don't exist
	for i := 0; i < numDocs/2; i++ {
		_, _, err := store.Get(ctx, fmt.Sprintf("doc_%d", i))
		require.Error(t, err)
	}

	// Verify remaining documents exist
	for i := numDocs / 2; i < numDocs; i++ {
		_, _, err := store.Get(ctx, fmt.Sprintf("doc_%d", i))
		require.NoError(t, err)
	}

	// Verify count
	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, numDocs/2, count)
}

// TestVectorStore_GetMetadata_Pagination tests pagination scenarios
func TestVectorStore_GetMetadata_Pagination(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add test documents
	for i := 0; i < 15; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("doc_%d", i),
			Content: "Content",
			Metadata: map[string]any{
				"index": i,
				"type":  "test",
			},
		}
		require.NoError(t, store.Add(ctx, doc, []float64{0.1, 0.2, 0.3}))
	}

	tests := []struct {
		name        string
		limit       int
		offset      int
		expectCount int
	}{
		{"first_page", 5, 0, 5},
		{"second_page", 5, 5, 5},
		{"third_page", 5, 10, 5},
		{"offset_beyond_total", 5, 20, 0},
		{"large_limit", 100, 0, 15},
		{"get_all", -1, -1, 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, err := store.GetMetadata(ctx,
				vectorstore.WithGetMetadataLimit(tt.limit),
				vectorstore.WithGetMetadataOffset(tt.offset),
			)
			require.NoError(t, err)
			assert.Equal(t, tt.expectCount, len(metadata))
		})
	}
}

// TestVectorStore_Count_WithFilters tests counting with different filters
func TestVectorStore_Count_WithFilters(t *testing.T) {
	ctx := context.Background()
	store := New()

	// Add documents
	docs := []struct {
		id       string
		metadata map[string]any
	}{
		{"doc1", map[string]any{"lang": "en", "type": "article"}},
		{"doc2", map[string]any{"lang": "en", "type": "blog"}},
		{"doc3", map[string]any{"lang": "fr", "type": "article"}},
		{"doc4", map[string]any{"lang": "fr", "type": "blog"}},
	}

	for _, d := range docs {
		doc := &document.Document{
			ID:       d.id,
			Content:  "Content",
			Metadata: d.metadata,
		}
		require.NoError(t, store.Add(ctx, doc, []float64{0.1, 0.2, 0.3}))
	}

	tests := []struct {
		name      string
		filter    map[string]any
		wantCount int
	}{
		{"count_all", nil, 4},
		{"count_en", map[string]any{"lang": "en"}, 2},
		{"count_article", map[string]any{"type": "article"}, 2},
		{"count_en_article", map[string]any{"lang": "en", "type": "article"}, 1},
		{"count_no_match", map[string]any{"lang": "de"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var count int
			var err error
			if tt.filter == nil {
				count, err = store.Count(ctx)
			} else {
				count, err = store.Count(ctx, vectorstore.WithCountFilter(tt.filter))
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, count)
		})
	}
}

// TestCosineSimilarity_EdgeCases tests cosine similarity with edge cases
func TestCosineSimilarity_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{"identical_vectors", []float64{1, 0, 0}, []float64{1, 0, 0}, 1.0},
		{"orthogonal_vectors", []float64{1, 0, 0}, []float64{0, 1, 0}, 0.0},
		{"opposite_vectors", []float64{1, 0, 0}, []float64{-1, 0, 0}, -1.0},
		{"zero_vector_a", []float64{0, 0, 0}, []float64{1, 0, 0}, 0.0},
		{"zero_vector_b", []float64{1, 0, 0}, []float64{0, 0, 0}, 0.0},
		{"both_zero", []float64{0, 0, 0}, []float64{0, 0, 0}, 0.0},
		{"different_dimensions", []float64{1, 0}, []float64{1, 0, 0}, 0.0},
		{"empty_vectors", []float64{}, []float64{}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			assert.InDelta(t, tt.expected, result, 1e-9)
		})
	}
}

// TestVectorStore_Close_MultipleTimes tests closing store multiple times
func TestVectorStore_Close_MultipleTimes(t *testing.T) {
	store := New()
	ctx := context.Background()

	// Add document
	doc := &document.Document{ID: "doc1", Content: "Content"}
	require.NoError(t, store.Add(ctx, doc, []float64{0.1, 0.2, 0.3}))

	// Close multiple times should not panic
	require.NoError(t, store.Close())
	require.NoError(t, store.Close())
	require.NoError(t, store.Close())
}
