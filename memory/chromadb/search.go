//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"context"
	"fmt"
	"math"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

var queryIncludes = []string{"documents", "metadatas", "distances"}

// SearchMemories searches memories for a user using cosine similarity.
func (svc *Service) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if err := svc.beginOperation(); err != nil {
		return nil, err
	}
	defer svc.endOperation()
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	searchOpts := memory.ResolveSearchOptions(query, opts)
	searchOpts.Query = strings.TrimSpace(searchOpts.Query)
	if searchOpts.Query == "" {
		return []*memory.Entry{}, nil
	}
	queryEmbedding, err := svc.embed(ctx, searchOpts.Query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}

	limit := resolveSearchLimit(svc.opts.maxResults, searchOpts.MaxResults)
	scope := recordScope{appName: userKey.AppName, userID: userKey.UserID}
	results, err := svc.searchDense(ctx, scope, queryEmbedding, searchOpts, limit)
	if err != nil {
		return nil, err
	}
	results = svc.applyKindFallback(ctx, scope, queryEmbedding, searchOpts, results, limit)
	if searchOpts.HybridSearch {
		results = svc.applyHybridSearch(ctx, scope, searchOpts, results, limit)
	}
	return finalizeSearchResults(results, searchOpts, limit), nil
}

func (svc *Service) searchDense(
	ctx context.Context,
	scope recordScope,
	embedding []float32,
	opts memory.SearchOptions,
	limit int,
) ([]*memory.Entry, error) {
	response, err := svc.client.queryRecords(ctx, svc.collection, queryRecordsRequest{
		Where:           searchWhere(scope, opts),
		QueryEmbeddings: [][]float32{embedding},
		NResults:        limit,
		Include:         append([]string(nil), queryIncludes...),
	})
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	results, err := decodeQueryResponse(response)
	if err != nil {
		return nil, fmt.Errorf("decode memory search results: %w", err)
	}
	return filterSimilarity(results, resolveSimilarityThreshold(
		svc.opts.similarityThreshold,
		opts.SimilarityThreshold,
	)), nil
}

func (svc *Service) applyKindFallback(
	ctx context.Context,
	scope recordScope,
	embedding []float32,
	opts memory.SearchOptions,
	results []*memory.Entry,
	limit int,
) []*memory.Entry {
	if opts.Kind == "" || !opts.KindFallback ||
		len(results) >= imemory.MinKindFallbackResults {
		return results
	}
	fallbackOpts := opts
	fallbackOpts.Kind = ""
	fallbackOpts.KindFallback = false
	fallback, err := svc.searchDense(ctx, scope, embedding, fallbackOpts, limit)
	if err != nil || len(fallback) == 0 {
		return results
	}
	return imemory.MergeSearchResults(results, fallback, opts.Kind, limit)
}

func (svc *Service) applyHybridSearch(
	ctx context.Context,
	scope recordScope,
	opts memory.SearchOptions,
	dense []*memory.Entry,
	limit int,
) []*memory.Entry {
	records, err := svc.listRecords(
		ctx,
		activeScopeWhere(scope),
		svc.opts.hybridCandidateLimit,
	)
	if err != nil {
		return dense
	}
	entries := make([]*memory.Entry, len(records))
	for i, record := range records {
		entries[i] = record.entry
	}
	keywordOpts := opts
	keywordOpts.KindFallback = false
	keywordOpts.Deduplicate = false
	keywordOpts.HybridSearch = false
	keywordOpts.SimilarityThreshold = 0
	keywordOpts.MaxResults = limit
	keyword := imemory.SearchEntries(
		entries,
		keywordOpts,
		imemory.DefaultSearchMinScore,
		limit,
	)
	if len(keyword) == 0 {
		return dense
	}
	return imemory.MergeHybridResults(dense, keyword, opts.HybridRRFK, limit)
}

func decodeQueryResponse(response *queryRecordsResponse) ([]*memory.Entry, error) {
	if response == nil {
		return nil, fmt.Errorf("query records returned a nil response")
	}
	if len(response.IDs) != 1 {
		return nil, fmt.Errorf("query records returned %d result batches, expected 1", len(response.IDs))
	}
	if response.Documents == nil || response.Metadatas == nil || response.Distances == nil {
		return nil, fmt.Errorf("query records did not include documents, metadatas, and distances")
	}
	documents, err := onlyQueryBatch("documents", *response.Documents)
	if err != nil {
		return nil, err
	}
	metadatas, err := onlyQueryBatch("metadatas", *response.Metadatas)
	if err != nil {
		return nil, err
	}
	distances, err := onlyQueryBatch("distances", *response.Distances)
	if err != nil {
		return nil, err
	}
	return decodeQueryBatch(response.IDs[0], documents, metadatas, distances)
}

func onlyQueryBatch[T any](name string, batches [][]T) ([]T, error) {
	if len(batches) != 1 {
		return nil, fmt.Errorf("query records returned %d %s batches, expected 1", len(batches), name)
	}
	return batches[0], nil
}

func decodeQueryBatch(
	ids []string,
	documents []*string,
	metadatas []map[string]any,
	distances []*float32,
) ([]*memory.Entry, error) {
	if len(documents) != len(ids) || len(metadatas) != len(ids) || len(distances) != len(ids) {
		return nil, fmt.Errorf(
			"query records column length mismatch: ids=%d documents=%d metadatas=%d distances=%d",
			len(ids),
			len(documents),
			len(metadatas),
			len(distances),
		)
	}
	results := make([]*memory.Entry, len(ids))
	for i, id := range ids {
		if distances[i] == nil {
			return nil, fmt.Errorf("query record %s has no distance", id)
		}
		distance := float64(*distances[i])
		if math.IsNaN(distance) || math.IsInf(distance, 0) {
			return nil, fmt.Errorf("query record %s has invalid distance %v", id, distance)
		}
		record, err := decodeStoredRecord(id, documents[i], metadatas[i])
		if err != nil {
			return nil, err
		}
		record.entry.Score = clampScore(1 - distance)
		results[i] = record.entry
	}
	return results, nil
}

func filterSimilarity(entries []*memory.Entry, threshold float64) []*memory.Entry {
	if threshold <= 0 {
		return entries
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.Score >= threshold {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func finalizeSearchResults(
	results []*memory.Entry,
	opts memory.SearchOptions,
	limit int,
) []*memory.Entry {
	if len(results) > 1 {
		if opts.Kind != "" && opts.KindFallback {
			imemory.SortSearchResultsWithKindPriority(results, opts.Kind, opts.OrderByEventTime)
		} else {
			imemory.SortSearchResults(results, opts.OrderByEventTime)
		}
	}
	if opts.Deduplicate && len(results) > 1 {
		results = imemory.DeduplicateResults(results)
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func resolveSearchLimit(defaultLimit, override int) int {
	if override > 0 {
		return override
	}
	return defaultLimit
}

func resolveSimilarityThreshold(defaultThreshold, override float64) float64 {
	if override > 0 {
		return override
	}
	return defaultThreshold
}

func clampScore(score float64) float64 {
	switch {
	case score < 0:
		return 0
	case score > 1:
		return 1
	default:
		return score
	}
}
