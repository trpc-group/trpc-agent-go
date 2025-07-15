//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
		Metadata: map[string]interface{}{
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
		Metadata: map[string]interface{}{
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
			Metadata: map[string]interface{}{
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
	store := New()
	doc := &document.Document{
		ID:      "doc1",
		Content: "data",
		Metadata: map[string]interface{}{
			"type": "test",
		},
	}
	embedding := []float64{0.1, 0.2, 0.3}
	require.NoError(t, store.Add(nil, doc, embedding))

	// Match by ID
	filterByID := &vectorstore.SearchFilter{IDs: []string{"doc1"}}
	require.True(t, store.matchesFilter("doc1", filterByID))

	// Non-matching ID
	filterWrongID := &vectorstore.SearchFilter{IDs: []string{"other"}}
	require.False(t, store.matchesFilter("doc1", filterWrongID))

	// Match by metadata
	filterByMeta := &vectorstore.SearchFilter{Metadata: map[string]interface{}{"type": "test"}}
	require.True(t, store.matchesFilter("doc1", filterByMeta))

	// Non-matching metadata
	filterWrongMeta := &vectorstore.SearchFilter{Metadata: map[string]interface{}{"type": "prod"}}
	require.False(t, store.matchesFilter("doc1", filterWrongMeta))
}

func TestVectorStore_ErrorPathsAndClose(t *testing.T) {
	store := New()

	// Get non-existent ID.
	_, _, err := store.Get(nil, "missing")
	require.Error(t, err)

	// Delete non-existent ID.
	err = store.Delete(nil, "missing")
	require.Error(t, err)

	// Search with empty vector.
	_, err = store.Search(nil, &vectorstore.SearchQuery{Vector: []float64{}})
	require.Error(t, err)

	// Close store.
	require.NoError(t, store.Close())
}
