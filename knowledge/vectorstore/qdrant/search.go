//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"context"
	"fmt"
	"strings"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// Search performs similarity search.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errQueryRequired
	}

	switch query.SearchMode {
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	case vectorstore.SearchModeKeyword:
		return vs.searchByKeyword(ctx, query)
	case vectorstore.SearchModeHybrid:
		return vs.searchByHybrid(ctx, query)
	default:
		return vs.searchByVector(ctx, query)
	}
}

// searchByVector performs dense vector similarity search.
func (vs *VectorStore) searchByVector(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, errVectorRequired
	}
	if len(query.Vector) != vs.opts.dimension {
		return nil, fmt.Errorf("%w: expected %d dimensions, got %d",
			ErrInvalidInput, vs.opts.dimension, len(query.Vector))
	}

	filter, err := vs.buildSearchFilter(query.Filter)
	if err != nil {
		return nil, err
	}

	limit := uint64(query.Limit)
	if limit == 0 {
		limit = uint64(vs.opts.maxResults)
	}

	// Build query based on vector configuration
	var queryReq *qdrant.QueryPoints
	if vs.opts.bm25Enabled {
		// Named vector query
		queryReq = &qdrant.QueryPoints{
			CollectionName: vs.opts.collectionName,
			Query:          qdrant.NewQueryDense(toFloat32Slice(query.Vector)),
			Using:          qdrant.PtrOf(vectorNameDense),
			Filter:         filter,
			Limit:          qdrant.PtrOf(limit),
			WithPayload:    qdrant.NewWithPayload(true),
		}
		if query.MinScore > 0 {
			queryReq.ScoreThreshold = qdrant.PtrOf(float32(query.MinScore))
		}
	} else {
		// Single vector query
		queryReq = &qdrant.QueryPoints{
			CollectionName: vs.opts.collectionName,
			Query:          qdrant.NewQuery(toFloat32Slice(query.Vector)...),
			Filter:         filter,
			Limit:          qdrant.PtrOf(limit),
			WithPayload:    qdrant.NewWithPayload(true),
		}
		if query.MinScore > 0 {
			queryReq.ScoreThreshold = qdrant.PtrOf(float32(query.MinScore))
		}
	}

	results, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.ScoredPoint, error) {
		return vs.client.Query(ctx, queryReq)
	})
	if err != nil {
		return nil, fmt.Errorf("search in %q: %w", vs.opts.collectionName, err)
	}

	return toSearchResult(results), nil
}

// searchByFilter performs filter-only search without vector similarity.
func (vs *VectorStore) searchByFilter(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	filter, err := vs.buildSearchFilter(query.Filter)
	if err != nil {
		return nil, err
	}

	limit := uint32(query.Limit)
	if limit == 0 {
		limit = uint32(vs.opts.maxResults)
	}

	points, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.RetrievedPoint, error) {
		return vs.client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: vs.opts.collectionName,
			Filter:         filter,
			Limit:          qdrant.PtrOf(limit),
			WithPayload:    qdrant.NewWithPayload(true),
		})
	})
	if err != nil {
		return nil, fmt.Errorf("filter search in %q: %w", vs.opts.collectionName, err)
	}

	return toFilterSearchResult(points), nil
}

// searchByKeyword performs BM25 sparse vector search.
func (vs *VectorStore) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if !vs.opts.bm25Enabled {
		return nil, fmt.Errorf("%w: keyword search requires WithBM25(true) option", ErrUnsupportedSearchMode)
	}
	if query.Query == "" {
		return nil, errQueryTextRequired
	}

	filter, err := vs.buildSearchFilter(query.Filter)
	if err != nil {
		return nil, err
	}

	limit := uint64(query.Limit)
	if limit == 0 {
		limit = uint64(vs.opts.maxResults)
	}

	// Use BM25 sparse vector search with Document inference
	results, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.ScoredPoint, error) {
		queryReq := &qdrant.QueryPoints{
			CollectionName: vs.opts.collectionName,
			Query: qdrant.NewQueryDocument(&qdrant.Document{
				Text:  query.Query,
				Model: defaultBM25Model,
			}),
			Using:       qdrant.PtrOf(vectorNameSparse),
			Filter:      filter,
			Limit:       qdrant.PtrOf(limit),
			WithPayload: qdrant.NewWithPayload(true),
		}
		if query.MinScore > 0 {
			queryReq.ScoreThreshold = qdrant.PtrOf(float32(query.MinScore))
		}
		return vs.client.Query(ctx, queryReq)
	})
	if err != nil {
		return nil, fmt.Errorf("keyword search in %q: %w", vs.opts.collectionName, err)
	}

	return toSearchResult(results), nil
}

// searchByHybrid performs hybrid search combining dense vectors and BM25.
// If BM25 is not enabled, it falls back to vector-only search.
func (vs *VectorStore) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if !vs.opts.bm25Enabled {
		if vs.opts.logger != nil {
			vs.opts.logger.Warn("hybrid search falling back to vector search: BM25 not enabled (use WithBM25(true) for full hybrid search)")
		}
		return vs.searchByVector(ctx, query)
	}

	if len(query.Vector) == 0 {
		return nil, errVectorRequired
	}
	if len(query.Vector) != vs.opts.dimension {
		return nil, fmt.Errorf("%w: expected %d dimensions, got %d",
			ErrInvalidInput, vs.opts.dimension, len(query.Vector))
	}
	if query.Query == "" {
		return nil, errQueryTextRequired
	}

	filter, err := vs.buildSearchFilter(query.Filter)
	if err != nil {
		return nil, err
	}

	limit := uint64(query.Limit)
	if limit == 0 {
		limit = uint64(vs.opts.maxResults)
	}

	// Prefetch more results for better fusion (capped to avoid excessive memory usage)
	prefetchLimit := limit * uint64(vs.opts.prefetchMultiplier)
	if prefetchLimit < minPrefetchLimit {
		prefetchLimit = minPrefetchLimit
	}
	if prefetchLimit > maxPrefetchLimit {
		prefetchLimit = maxPrefetchLimit
	}

	// Hybrid search with Prefetch + RRF fusion
	prefetches := []*qdrant.PrefetchQuery{
		// Dense vector search
		{
			Query:  qdrant.NewQueryDense(toFloat32Slice(query.Vector)),
			Using:  qdrant.PtrOf(vectorNameDense),
			Filter: filter,
			Limit:  qdrant.PtrOf(prefetchLimit),
		},
		// BM25 sparse vector search
		{
			Query: qdrant.NewQueryDocument(&qdrant.Document{
				Text:  query.Query,
				Model: defaultBM25Model,
			}),
			Using:  qdrant.PtrOf(vectorNameSparse),
			Filter: filter,
			Limit:  qdrant.PtrOf(prefetchLimit),
		},
	}

	results, err := retry(ctx, vs.retryCfg, func() ([]*qdrant.ScoredPoint, error) {
		queryReq := &qdrant.QueryPoints{
			CollectionName: vs.opts.collectionName,
			Prefetch:       prefetches,
			Query:          qdrant.NewQueryFusion(qdrant.Fusion_RRF),
			Limit:          qdrant.PtrOf(limit),
			WithPayload:    qdrant.NewWithPayload(true),
		}
		if query.MinScore > 0 {
			queryReq.ScoreThreshold = qdrant.PtrOf(float32(query.MinScore))
		}
		return vs.client.Query(ctx, queryReq)
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid search in %q: %w", vs.opts.collectionName, err)
	}

	return toSearchResult(results), nil
}

// buildSearchFilter converts a SearchFilter to a Qdrant Filter.
func (vs *VectorStore) buildSearchFilter(filter *vectorstore.SearchFilter) (*qdrant.Filter, error) {
	if filter == nil {
		return nil, nil
	}

	var conditions []*qdrant.Condition

	// Add ID filter if present (uses special HasId condition)
	if len(filter.IDs) > 0 {
		conditions = append(conditions, &qdrant.Condition{
			ConditionOneOf: &qdrant.Condition_HasId{
				HasId: &qdrant.HasIdCondition{
					HasId: stringsToPointIDs(filter.IDs),
				},
			},
		})
	}

	// Build metadata and filter conditions using the converter
	var universalFilters []*searchfilter.UniversalFilterCondition

	for key, value := range filter.Metadata {
		if !strings.HasPrefix(key, source.MetadataFieldPrefix) {
			key = source.MetadataFieldPrefix + key
		}
		universalFilters = append(universalFilters, &searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorEqual,
			Field:    key,
			Value:    value,
		})
	}

	if filter.FilterCondition != nil {
		universalFilters = append(universalFilters, filter.FilterCondition)
	}

	// Convert universal filters to Qdrant filter
	if len(universalFilters) > 0 {
		converted, err := vs.filterConverter.Convert(&searchfilter.UniversalFilterCondition{
			Operator: searchfilter.OperatorAnd,
			Value:    universalFilters,
		})
		if err != nil {
			return nil, fmt.Errorf("convert filter: %w", err)
		}
		// Extract conditions from converted filter and add to our conditions
		if converted != nil {
			conditions = append(conditions, converted.Must...)
			conditions = append(conditions, converted.Should...)
			// MustNot needs special handling - wrap in a nested filter
			if len(converted.MustNot) > 0 {
				conditions = append(conditions, qdrant.NewFilterAsCondition(&qdrant.Filter{MustNot: converted.MustNot}))
			}
		}
	}

	if len(conditions) == 0 {
		return nil, nil
	}

	return &qdrant.Filter{Must: conditions}, nil
}
