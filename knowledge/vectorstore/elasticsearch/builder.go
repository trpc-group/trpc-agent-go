//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch-based vector storage implementation.
package elasticsearch

import (
	"encoding/json"
	"fmt"

	"github.com/elastic/go-elasticsearch/v9/typedapi/esdsl"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/textquerytype"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

const (
	// scriptParamQueryVector is the name of the script parameter for the query vector.
	scriptParamQueryVector = "query_vector"
	// scriptSource is the source of the script for the cosine similarity.
	scriptSource = "if (doc['embedding'].size() > 0) { cosineSimilarity(params.query_vector, 'embedding') + 1.0 } else { 0.0 }"

	// searchFieldContent is the content field for text search with higher weight.
	searchFieldContent = "content^2"
	// searchFieldName is the name field for text search with medium weight.
	searchFieldName = "name^1.5"
)

// buildVectorSearchQuery builds a vector similarity search query.
func (vs *VectorStore) buildVectorSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	// Marshal query vector to a valid JSON array for script params.
	vectorJSON, err := json.Marshal(query.Vector)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch failed to marshal query vector: %w", err)
	}

	// Create script for cosine similarity using esdsl.
	script := esdsl.NewScript().
		Source(esdsl.NewScriptSource().String(scriptSource)).
		AddParam(scriptParamQueryVector, json.RawMessage(vectorJSON))

	// Create match_all query using esdsl.
	matchAllQuery := esdsl.NewMatchAllQuery()

	// Create script_score query using esdsl.
	scriptScoreQuery := esdsl.NewScriptScoreQuery(matchAllQuery, script)

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(scriptScoreQuery).
		Size(vs.option.maxResults)

	// Add filters if specified.
	if query.Filter != nil {
		searchBody.PostFilter(vs.buildFilterQuery(query.Filter))
	}

	return searchBody.SearchRequestBodyCaster(), nil
}

// buildKeywordSearchQuery builds a keyword-based search query.
func (vs *VectorStore) buildKeywordSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	// Create multi_match query using esdsl.
	multiMatchQuery := esdsl.NewMultiMatchQuery(query.Query).
		Fields(searchFieldContent, searchFieldName).
		Type(textquerytype.Bestfields)

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(multiMatchQuery).
		Size(vs.option.maxResults)

	// Add filters if specified.
	if query.Filter != nil {
		searchBody.PostFilter(vs.buildFilterQuery(query.Filter))
	}

	return searchBody.SearchRequestBodyCaster(), nil
}

// buildHybridSearchQuery builds a hybrid search query combining vector and keyword search.
func (vs *VectorStore) buildHybridSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	// Marshal query vector to a valid JSON array for script params.
	vectorJSON, err := json.Marshal(query.Vector)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch failed to marshal query vector: %w", err)
	}

	// Create script for vector similarity.
	script := esdsl.NewScript().
		Source(esdsl.NewScriptSource().String(scriptSource)).
		AddParam(scriptParamQueryVector, json.RawMessage(vectorJSON))

	// Create match_all query for script_score.
	matchAllQuery := esdsl.NewMatchAllQuery()

	// Create script_score query.
	scriptScoreQuery := esdsl.NewScriptScoreQuery(matchAllQuery, script)

	// Create multi_match query.
	multiMatchQuery := esdsl.NewMultiMatchQuery(query.Query).
		Fields(searchFieldContent, searchFieldName).
		Type(textquerytype.Bestfields)

	// Combine queries using bool query.
	boolQuery := esdsl.NewBoolQuery().
		Should(scriptScoreQuery, multiMatchQuery).
		MinimumShouldMatch(esdsl.NewMinimumShouldMatch().Int(1))

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(boolQuery).
		Size(vs.option.maxResults)

	// Add filters if specified.
	if query.Filter != nil {
		searchBody.PostFilter(vs.buildFilterQuery(query.Filter))
	}

	return searchBody.SearchRequestBodyCaster(), nil
}

// buildFilterQuery builds a filter query for search results.
func (vs *VectorStore) buildFilterQuery(filter *vectorstore.SearchFilter) types.QueryVariant {
	var filters []types.QueryVariant

	// Filter by document IDs.
	if len(filter.IDs) > 0 {
		termsQuery := esdsl.NewTermsQuery()
		fieldValues := make([]types.FieldValueVariant, len(filter.IDs))
		for i, id := range filter.IDs {
			fieldValues[i] = esdsl.NewFieldValue().String(id)
		}
		termsQuery.AddTermsQuery(fieldID, esdsl.NewTermsQueryField().FieldValues(fieldValues...))
		filters = append(filters, termsQuery)
	}

	// Filter by metadata.
	for key, value := range filter.Metadata {
		termQuery := esdsl.NewTermQuery(fmt.Sprintf("%s.%s", fieldMetadata, key),
			esdsl.NewFieldValue().String(fmt.Sprintf("%v", value)))
		filters = append(filters, termQuery)
	}

	if len(filters) == 0 {
		return nil
	}

	boolQuery := esdsl.NewBoolQuery().Filter(filters...)
	return boolQuery
}
