//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestBuildVectorSearchQuery(t *testing.T) {
	// Create a mock VectorStore with options
	vs := &VectorStore{
		option: options{
			maxResults:      20,
			vectorDimension: 3,
		},
		filterConverter: &esConverter{},
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2, 0.3},
		SearchMode: vectorstore.SearchModeVector,
	}

	result, err := vs.buildVectorSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 20, *result.Size)
	assert.NotNil(t, result.Query)
}

func TestBuildKeywordSearchQuery(t *testing.T) {
	// Create a mock VectorStore with options
	vs := &VectorStore{
		option: options{
			maxResults: 15,
		},
		filterConverter: &esConverter{},
	}

	query := &vectorstore.SearchQuery{
		Query:      "test query",
		SearchMode: vectorstore.SearchModeKeyword,
	}

	result, err := vs.buildKeywordSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 15, *result.Size)
	assert.NotNil(t, result.Query)
}

func TestBuildHybridSearchQuery(t *testing.T) {
	// Create a mock VectorStore with options
	vs := &VectorStore{
		option: options{
			maxResults:      25,
			vectorDimension: 3,
		},
		filterConverter: &esConverter{},
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2, 0.3},
		Query:      "test query",
		SearchMode: vectorstore.SearchModeHybrid,
	}

	result, err := vs.buildHybridSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 25, *result.Size)
	assert.NotNil(t, result.Query)
}

func TestBuildFilterQuery(t *testing.T) {
	vs := &VectorStore{
		option: options{
			idFieldName: "id",
		},
		filterConverter: &esConverter{},
	}

	// Test with ID filter
	filter := &vectorstore.SearchFilter{
		IDs: []string{"doc1", "doc2"},
	}

	result, err := vs.buildFilterQuery(filter)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Test with metadata filter
	filter = &vectorstore.SearchFilter{
		Metadata: map[string]any{
			"category": "test",
			"type":     "document",
		},
	}

	result, err = vs.buildFilterQuery(filter)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Test with empty filter
	filter = &vectorstore.SearchFilter{}
	result, err = vs.buildFilterQuery(filter)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestBuildFilterQueryWithBothFilters(t *testing.T) {
	vs := &VectorStore{
		filterConverter: &esConverter{},
		option: options{
			idFieldName: "id",
		},
	}

	filter := &vectorstore.SearchFilter{
		IDs: []string{"doc1", "doc2"},
		Metadata: map[string]any{
			"category": "test",
		},
	}

	result, err := vs.buildFilterQuery(filter)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestBuildHybridSearchQueryWithFilter(t *testing.T) {
	// Create a mock VectorStore with options
	vs := &VectorStore{
		option: options{
			maxResults:      30,
			idFieldName:     "id",
			vectorDimension: 3,
		},
		filterConverter: &esConverter{},
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2, 0.3},
		Query:      "test query",
		SearchMode: vectorstore.SearchModeHybrid,
		Filter: &vectorstore.SearchFilter{
			IDs: []string{"doc1", "doc2"},
			Metadata: map[string]any{
				"category": "test",
				"type":     "document",
			},
		},
	}

	result, err := vs.buildHybridSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 30, *result.Size)
	assert.NotNil(t, result.Query)

	// Verify that PostFilter is set when filter is provided
	assert.NotNil(t, result.PostFilter)
}

func TestBuildVectorSearchQuery_WithEmptyFilter_NoPostFilter(t *testing.T) {
	vs := &VectorStore{
		option:          options{maxResults: 10, vectorDimension: 2},
		filterConverter: &esConverter{},
	}
	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2},
		SearchMode: vectorstore.SearchModeVector,
		Filter:     &vectorstore.SearchFilter{},
	}
	res, err := vs.buildVectorSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Nil(t, res.PostFilter)
}

func TestBuildKeywordSearchQuery_WithEmptyFilter_NoPostFilter(t *testing.T) {
	vs := &VectorStore{
		option:          options{maxResults: 10},
		filterConverter: &esConverter{},
	}
	query := &vectorstore.SearchQuery{
		Query:      "hello",
		SearchMode: vectorstore.SearchModeKeyword,
		Filter:     &vectorstore.SearchFilter{},
	}
	res, err := vs.buildKeywordSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Nil(t, res.PostFilter)
}

func TestBuildHybridSearchQuery_WithEmptyFilter_NoPostFilter(t *testing.T) {
	vs := &VectorStore{
		option:          options{maxResults: 10, vectorDimension: 2},
		filterConverter: &esConverter{},
	}
	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2},
		Query:      "hello",
		SearchMode: vectorstore.SearchModeHybrid,
		Filter:     &vectorstore.SearchFilter{},
	}
	res, err := vs.buildHybridSearchQuery(query)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Nil(t, res.PostFilter)
}
