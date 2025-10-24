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
	"errors"
	"fmt"

	"github.com/elastic/go-elasticsearch/v9/typedapi/esdsl"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types"
	"github.com/elastic/go-elasticsearch/v9/typedapi/types/enums/textquerytype"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	// scriptParamQueryVector is the name of the script parameter for the query vector.
	scriptParamQueryVector = "query_vector"
)

// buildVectorSearchQuery builds a vector similarity search query.
func (vs *VectorStore) buildVectorSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("elasticsearch query vector cannot be empty for %s", query.Query)
	}

	if len(query.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("elasticsearch query vector dimension %d does not match expected dimension %d", len(query.Vector), vs.option.vectorDimension)
	}
	// Marshal query vector to a valid JSON array for script params.
	vectorJSON, err := json.Marshal(query.Vector)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch failed to marshal query vector: %w", err)
	}

	// Build script source dynamically to support custom embedding field.
	embeddingField := vs.option.embeddingFieldName
	scriptSource := fmt.Sprintf("if (doc['%s'].size() > 0) { cosineSimilarity(params.query_vector, '%s') + 1.0 } else { 0.0 }", embeddingField, embeddingField)

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
		Size(vs.getMaxResult(query.Limit))

	// Add filters if specified.
	filterQuery, err := vs.buildFilterQuery(query.Filter)
	if err != nil {
		return nil, err
	}
	if filterQuery != nil {
		searchBody.PostFilter(filterQuery)
	}

	return searchBody.SearchRequestBodyCaster(), nil
}

func (vs *VectorStore) buildFilterSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	filterQuery, err := vs.buildFilterQuery(query.Filter)
	if err != nil {
		return nil, err
	}
	if filterQuery == nil {
		return nil, errors.New("elasticsearch filter query is nil")
	}

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(filterQuery.QueryCaster()).
		Size(vs.getMaxResult(query.Limit))

	return searchBody.SearchRequestBodyCaster(), nil
}

// buildKeywordSearchQuery builds a keyword-based search query.
func (vs *VectorStore) buildKeywordSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	contentField := vs.option.contentFieldName

	// Create multi_match query using esdsl.
	nameField := vs.option.nameFieldName
	multiMatchQuery := esdsl.NewMultiMatchQuery(query.Query).
		Fields(fmt.Sprintf("%s^2", contentField), fmt.Sprintf("%s^1.5", nameField)).
		Type(textquerytype.Bestfields)

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(multiMatchQuery).
		Size(vs.getMaxResult(query.Limit))

	// Add filters if specified.
	filterQuery, err := vs.buildFilterQuery(query.Filter)
	if err != nil {
		return nil, err
	}
	if filterQuery != nil {
		searchBody.PostFilter(filterQuery)
	}

	return searchBody.SearchRequestBodyCaster(), nil
}

// buildHybridSearchQuery builds a hybrid search query combining vector and keyword search.
func (vs *VectorStore) buildHybridSearchQuery(query *vectorstore.SearchQuery) (*types.SearchRequestBody, error) {
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("elasticsearch query vector cannot be empty for %s", query.Query)
	}

	if len(query.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("elasticsearch query vector dimension %d does not match expected dimension %d", len(query.Vector), vs.option.vectorDimension)
	}
	// Marshal query vector to a valid JSON array for script params.
	vectorJSON, err := json.Marshal(query.Vector)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch failed to marshal query vector: %w", err)
	}

	// Build script with custom embedding field.
	embeddingField := vs.option.embeddingFieldName
	scriptSource := fmt.Sprintf("if (doc['%s'].size() > 0) { cosineSimilarity(params.query_vector, '%s') + 1.0 } else { 0.0 }", embeddingField, embeddingField)
	script := esdsl.NewScript().
		Source(esdsl.NewScriptSource().String(scriptSource)).
		AddParam(scriptParamQueryVector, json.RawMessage(vectorJSON))

	// Create match_all query for script_score.
	matchAllQuery := esdsl.NewMatchAllQuery()

	// Create script_score query.
	scriptScoreQuery := esdsl.NewScriptScoreQuery(matchAllQuery, script)

	contentField := vs.option.contentFieldName
	nameField := vs.option.nameFieldName
	multiMatchQuery := esdsl.NewMultiMatchQuery(query.Query).
		Fields(fmt.Sprintf("%s^2", contentField), fmt.Sprintf("%s^1.5", nameField)).
		Type(textquerytype.Bestfields)

	// Combine queries using bool query.
	boolQuery := esdsl.NewBoolQuery().
		Should(scriptScoreQuery, multiMatchQuery).
		MinimumShouldMatch(esdsl.NewMinimumShouldMatch().Int(1))

	// Build the complete search request using official SearchRequestBody.
	searchBody := esdsl.NewSearchRequestBody().
		Query(boolQuery).
		Size(vs.getMaxResult(query.Limit))

	// Add filters if specified.
	filterQuery, err := vs.buildFilterQuery(query.Filter)
	if err != nil {
		return nil, err
	}
	if filterQuery != nil {
		searchBody.PostFilter(filterQuery)
	}

	return searchBody.SearchRequestBodyCaster(), nil
}

// buildFilterQuery builds a filter query for search results.
func (vs *VectorStore) buildFilterQuery(filter *vectorstore.SearchFilter) (types.QueryVariant, error) {
	if filter == nil {
		return nil, nil
	}
	var filters []*searchfilter.UniversalFilterCondition
	if filter.FilterCondition != nil {
		filters = append(filters, filter.FilterCondition)
	}

	// Filter by document IDs.
	if len(filter.IDs) > 0 {
		filters = append(filters, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorIn,
			Field:    vs.option.idFieldName,
			Value:    filter.IDs,
		})
	}

	// Filter by metadata.
	for key, value := range filter.Metadata {
		filters = append(filters, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorEqual,
			Field:    fmt.Sprintf("%s.%s", vs.option.metadataFieldName, key),
			Value:    value,
		})
	}

	if len(filters) == 0 {
		return nil, nil
	}

	filterQuery, err := vs.filterConverter.Convert(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    filters,
	})
	if err != nil {
		log.Warnf("elasticsearch build filter query failed: %v", err)
		return nil, err
	}
	return filterQuery, nil
}

func (vs *VectorStore) getMaxResult(maxResults int) int {
	if maxResults <= 0 {
		return vs.option.maxResults
	}
	return maxResults
}
