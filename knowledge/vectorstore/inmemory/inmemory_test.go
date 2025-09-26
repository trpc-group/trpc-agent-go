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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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
