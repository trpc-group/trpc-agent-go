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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestBuildVectorSearchQuery(t *testing.T) {
	// Create a mock VectorStore with options
	vs := &VectorStore{
		option: options{
			maxResults: 20,
		},
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2, 0.3},
		SearchMode: vectorstore.SearchModeVector,
	}

	result := vs.buildVectorSearchQuery(query)

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
	}

	query := &vectorstore.SearchQuery{
		Query:      "test query",
		SearchMode: vectorstore.SearchModeKeyword,
	}

	result := vs.buildKeywordSearchQuery(query)

	assert.NotNil(t, result)
	assert.Equal(t, 15, *result.Size)
	assert.NotNil(t, result.Query)
}

func TestBuildHybridSearchQuery(t *testing.T) {
	// Create a mock VectorStore with options
	vs := &VectorStore{
		option: options{
			maxResults: 25,
		},
	}

	query := &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2, 0.3},
		Query:      "test query",
		SearchMode: vectorstore.SearchModeHybrid,
	}

	result := vs.buildHybridSearchQuery(query)

	assert.NotNil(t, result)
	assert.Equal(t, 25, *result.Size)
	assert.NotNil(t, result.Query)
}

func TestBuildFilterQuery(t *testing.T) {
	vs := &VectorStore{}

	// Test with ID filter
	filter := &vectorstore.SearchFilter{
		IDs: []string{"doc1", "doc2"},
	}

	result := vs.buildFilterQuery(filter)
	assert.NotNil(t, result)

	// Test with metadata filter
	filter = &vectorstore.SearchFilter{
		Metadata: map[string]any{
			"category": "test",
			"type":     "document",
		},
	}

	result = vs.buildFilterQuery(filter)
	assert.NotNil(t, result)

	// Test with empty filter
	filter = &vectorstore.SearchFilter{}
	result = vs.buildFilterQuery(filter)
	assert.Nil(t, result)
}

func TestBuildFilterQueryWithBothFilters(t *testing.T) {
	vs := &VectorStore{}

	filter := &vectorstore.SearchFilter{
		IDs: []string{"doc1", "doc2"},
		Metadata: map[string]any{
			"category": "test",
		},
	}

	result := vs.buildFilterQuery(filter)
	assert.NotNil(t, result)
}
