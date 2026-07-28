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

// SearchMemories searches memories for a user using cosine similarity.
//
// Hybrid search fuses unfiltered dense and bounded keyword ranks using RRF. The scan is
// serialized with writes by this Service, while cross-instance pagination remains best-effort.
// SearchMemories rejects time bounds outside the signed 64-bit nanosecond range.
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
	if err := validateNanosecondTime("time after", searchOpts.TimeAfter); err != nil {
		return nil, err
	}
	if err := validateNanosecondTime("time before", searchOpts.TimeBefore); err != nil {
		return nil, err
	}
	searchOpts.Query = strings.TrimSpace(searchOpts.Query)
	if searchOpts.Query == "" {
		return []*memory.Entry{}, nil
	}
	queryEmbedding, err := svc.embed(ctx, searchOpts.Query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}

	if searchOpts.MaxResults <= 0 {
		searchOpts.MaxResults = svc.opts.maxResults
	}
	if searchOpts.SimilarityThreshold <= 0 || math.IsNaN(searchOpts.SimilarityThreshold) {
		searchOpts.SimilarityThreshold = svc.opts.similarityThreshold
	}
	scope := recordScope{appName: userKey.AppName, userID: userKey.UserID}
	results, err := svc.searchDense(ctx, scope, queryEmbedding, searchOpts)
	if err != nil {
		return nil, err
	}
	results = svc.applyKindFallback(ctx, scope, queryEmbedding, searchOpts, results)
	if searchOpts.HybridSearch {
		results = svc.applyHybridSearch(ctx, scope, searchOpts, results)
	}
	return finalizeSearchResults(results, searchOpts), nil
}

// searchDense runs cosine retrieval and applies the threshold outside hybrid search.
func (svc *Service) searchDense(
	ctx context.Context,
	scope recordScope,
	embedding []float32,
	opts memory.SearchOptions,
) ([]*memory.Entry, error) {
	response, err := svc.client.queryRecords(ctx, svc.collection, queryRecordsRequest{
		Where:           searchWhere(scope, opts),
		QueryEmbeddings: [][]float32{embedding},
		NResults:        opts.MaxResults,
		Include:         []string{"documents", "metadatas", "distances"},
	})
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	results, err := decodeQueryResponse(response)
	if err != nil {
		return nil, fmt.Errorf("decode memory search results: %w", err)
	}
	if opts.HybridSearch || opts.SimilarityThreshold <= 0 {
		return results, nil
	}
	filtered := results[:0]
	for _, entry := range results {
		if entry.Score >= opts.SimilarityThreshold {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

// applyKindFallback retries without a kind constraint while reusing the query vector.
func (svc *Service) applyKindFallback(
	ctx context.Context,
	scope recordScope,
	embedding []float32,
	opts memory.SearchOptions,
	results []*memory.Entry,
) []*memory.Entry {
	if opts.Kind == "" || !opts.KindFallback ||
		len(results) >= imemory.MinKindFallbackResults {
		return results
	}
	limit := opts.MaxResults
	fallbackOpts := opts
	fallbackOpts.Kind = ""
	fallbackOpts.KindFallback = false
	fallback, err := svc.searchDense(ctx, scope, embedding, fallbackOpts)
	if err != nil || len(fallback) == 0 {
		return results
	}
	return imemory.MergeSearchResults(results, fallback, opts.Kind, limit)
}

// applyHybridSearch fuses dense and bounded local keyword ranks with shared RRF logic.
func (svc *Service) applyHybridSearch(
	ctx context.Context,
	scope recordScope,
	opts memory.SearchOptions,
	dense []*memory.Entry,
) []*memory.Entry {
	lock := svc.writeLock(scope)
	lock.Lock()
	defer lock.Unlock()
	limit := opts.MaxResults
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

// decodeQueryResponse unwraps Chroma's single-query batch and checks every column.
func decodeQueryResponse(response *queryRecordsResponse) ([]*memory.Entry, error) {
	if response == nil {
		return nil, fmt.Errorf("query records returned a nil response")
	}
	idBatches := response.IDs.value
	if len(idBatches) != 1 {
		return nil, fmt.Errorf("query records returned %d result batches, expected 1", len(idBatches))
	}
	if response.Documents == nil || response.Metadatas == nil || response.Distances == nil {
		return nil, fmt.Errorf("query records did not include documents, metadatas, and distances")
	}
	documentBatches := *response.Documents
	if len(documentBatches) != 1 {
		return nil, fmt.Errorf(
			"query records returned %d documents batches, expected 1",
			len(documentBatches),
		)
	}
	metadataBatches := *response.Metadatas
	if len(metadataBatches) != 1 {
		return nil, fmt.Errorf(
			"query records returned %d metadatas batches, expected 1",
			len(metadataBatches),
		)
	}
	distanceBatches := *response.Distances
	if len(distanceBatches) != 1 {
		return nil, fmt.Errorf(
			"query records returned %d distances batches, expected 1",
			len(distanceBatches),
		)
	}
	ids := idBatches[0]
	documents := documentBatches[0]
	metadatas := metadataBatches[0]
	distances := distanceBatches[0]
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

// finalizeSearchResults deduplicates, sorts, and limits the merged retrieval output.
func finalizeSearchResults(
	results []*memory.Entry,
	opts memory.SearchOptions,
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
	if opts.MaxResults > 0 && len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}
	return results
}

// clampScore bounds cosine similarity to the framework's zero-to-one score range.
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
