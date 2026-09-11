//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"context"
	"errors"
	"fmt"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

const scoreEpsilon = 1e-6

// limitOrDefault returns limit when positive, or the configured maxResults.
func (vs *VectorStore) limitOrDefault(limit int) int {
	if limit > 0 {
		return limit
	}
	return vs.opts.maxResults
}

// requestLimit bounds one Chroma request while paging larger operations.
func (vs *VectorStore) requestLimit(limit int) int {
	if limit > vs.opts.maxRequestRecords {
		return vs.opts.maxRequestRecords
	}
	return limit
}

// cosineScore converts a Chroma cosine distance to a [0, 1] similarity score.
func cosineScore(distance float32) float64 {
	s := 1.0 - float64(distance)/2
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}

// applyMinScore filters out documents with a score below minScore.
func applyMinScore(docs []*vectorstore.ScoredDocument, minScore float64) []*vectorstore.ScoredDocument {
	if minScore <= 0 {
		return docs
	}
	out := docs[:0]
	for _, d := range docs {
		if d.Score >= minScore {
			out = append(out, d)
		}
	}
	return out
}

// Search dispatches to vector, filter, keyword, or hybrid search.
// SearchModeHybrid is the zero value of SearchQuery.SearchMode; callers that
// want pure vector search must set SearchModeVector explicitly.
//
// MinScore applies to vector, keyword, and hybrid ranked results.
//
// Get-based search pages through the configured request limit.
// Vector Query caps one request at that limit; a single Query has no cursor,
// so larger vector searches return at most the configured cap.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errors.New("chroma: search query is required")
	}

	switch query.SearchMode {
	case vectorstore.SearchModeVector:
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeKeyword:
		return vs.searchByKeyword(ctx, query)
	case vectorstore.SearchModeHybrid:
		return vs.searchByHybrid(ctx, query)
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	default:
		return nil, fmt.Errorf("chroma: unsupported SearchMode %d", query.SearchMode)
	}
}

// searchByVector performs a dense vector query against Chroma.
func (vs *VectorStore) searchByVector(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if err := validateEmbedding(q.Vector, vs.opts.indexDimension, true); err != nil {
		return nil, err
	}
	selectors, err := vs.buildSelectors(q.Filter)
	if err != nil {
		return nil, err
	}
	if selectors.noMatch {
		return &vectorstore.SearchResult{}, nil
	}
	res, err := vs.client.Query(ctx, storage.QueryParams{
		QueryEmbeddings: [][]float32{toFloat32(q.Vector)},
		NResults:        vs.requestLimit(vs.limitOrDefault(q.Limit)),
		IDs:             selectors.ids,
		Where:           selectors.where,
		WhereDocument:   selectors.whereDocument,
		Include:         includeQueryFields,
	})
	if err != nil {
		return nil, fmt.Errorf("chroma query: %w", err)
	}
	docs, err := vs.scoredFromQuery(res)
	if err != nil {
		return nil, err
	}
	return &vectorstore.SearchResult{Results: applyMinScore(docs, q.MinScore)}, nil
}

// searchByFilter retrieves documents matching IDs, metadata, or content filters.
func (vs *VectorStore) searchByFilter(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if q.Filter == nil || (len(q.Filter.IDs) == 0 && len(q.Filter.Metadata) == 0 && q.Filter.FilterCondition == nil) {
		return nil, errEmptyFilter
	}
	selectors, err := vs.buildSelectors(q.Filter)
	if err != nil {
		return nil, err
	}
	if selectors.noMatch {
		return &vectorstore.SearchResult{}, nil
	}
	if !selectors.hasSelector() {
		return nil, errEmptyFilter
	}
	docs, err := vs.collectGet(ctx, storage.GetParams{
		IDs:           selectors.ids,
		Where:         selectors.where,
		WhereDocument: selectors.whereDocument,
		Include:       includeRecordFields,
	}, vs.limitOrDefault(q.Limit))
	if err != nil {
		return nil, fmt.Errorf("chroma get: %w", err)
	}
	return &vectorstore.SearchResult{Results: docs}, nil
}

// searchByKeyword delegates sparse keyword ranking to Chroma /search.
func (vs *VectorStore) searchByKeyword(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if q.Query == "" {
		return nil, errors.New("chroma: keyword is required for keyword search")
	}
	if !vs.opts.sparseSearch {
		return nil, errors.New("chroma: keyword search requires WithSparseSearch")
	}
	selectors, err := vs.buildSelectors(q.Filter)
	if err != nil {
		return nil, err
	}
	if selectors.noMatch {
		return &vectorstore.SearchResult{}, nil
	}
	filter, err := searchWhereFilter(selectors.where, selectors.whereDocument)
	if err != nil {
		return nil, err
	}
	limit := vs.requestLimit(vs.limitOrDefault(q.Limit))
	knnQuery, err := vs.knnQuery(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	knn := map[string]any{"$knn": map[string]any{
		"query":       knnQuery,
		"key":         vs.opts.sparseSearchKey,
		"limit":       limit,
		"return_rank": true,
	}}
	res, err := vs.client.Search(ctx, storage.SearchParams{
		IDs:    selectors.ids,
		Filter: filter,
		Rank:   reciprocalRankExpr(knn, defaultRRFOffset),
		Limit:  limit,
		Select: []string{"#document", "#metadata", "#score"},
	})
	if err != nil {
		if storage.IsNotImplemented(err) {
			return nil, fmt.Errorf("chroma: keyword search is not supported by this server: %w", err)
		}
		return nil, fmt.Errorf("chroma keyword search: %w", err)
	}
	docs, err := vs.scoredFromSearch(res, negativeRankScore)
	if err != nil {
		return nil, err
	}
	return &vectorstore.SearchResult{Results: applyMinScore(docs, q.MinScore)}, nil
}

// searchByHybrid uses Chroma /search when sparse search is configured. An empty
// text query or an unconfigured sparse path falls back to dense vector search.
func (vs *VectorStore) searchByHybrid(ctx context.Context, q *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if err := validateEmbedding(q.Vector, vs.opts.indexDimension, true); err != nil {
		return nil, err
	}
	if q.Query == "" || !vs.opts.sparseSearch {
		return vs.searchByVector(ctx, q)
	}
	res, err := vs.searchByHybridRank(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("chroma server hybrid search: %w", err)
	}
	return res, nil
}

// searchByHybridRank delegates dense and sparse ranking to Chroma's /search
// endpoint. Metadata and document predicates are combined in its where
// expression.
func (vs *VectorStore) searchByHybridRank(
	ctx context.Context,
	q *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	selectors, err := vs.buildSelectors(q.Filter)
	if err != nil {
		return nil, err
	}
	if selectors.noMatch {
		return &vectorstore.SearchResult{}, nil
	}
	filter, err := searchWhereFilter(selectors.where, selectors.whereDocument)
	if err != nil {
		return nil, err
	}

	limit := vs.requestLimit(vs.limitOrDefault(q.Limit))
	knnLimit := vs.requestLimit(hybridCandidateLimit(limit))
	sparseQuery, err := vs.knnQuery(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	dense := map[string]any{
		"$knn": map[string]any{
			"query":       toFloat32(q.Vector),
			"key":         "#embedding",
			"limit":       knnLimit,
			"default":     knnLimit,
			"return_rank": true,
		},
	}
	sparse := map[string]any{
		"$knn": map[string]any{
			"query":       sparseQuery,
			"key":         vs.opts.sparseSearchKey,
			"limit":       knnLimit,
			"default":     knnLimit,
			"return_rank": true,
		},
	}
	rrf := rrfRankExpr(
		dense,
		sparse,
		defaultRRFOffset,
		vs.opts.hybridDenseWeight,
		vs.opts.hybridSparseWeight,
	)

	res, err := vs.client.Search(ctx, storage.SearchParams{
		IDs:    selectors.ids,
		Filter: filter,
		Rank:   rrf,
		Limit:  limit,
		Select: []string{"#document", "#metadata", "#score"},
	})
	if err != nil {
		return nil, err
	}
	docs, err := vs.scoredFromSearch(res, negativeRankScore)
	if err != nil {
		return nil, err
	}
	return &vectorstore.SearchResult{Results: applyMinScore(docs, q.MinScore)}, nil
}

// reciprocalRankExpr maps a zero-based rank to a normalized [-1, 0] score.
func reciprocalRankExpr(rank map[string]any, k uint32) map[string]any {
	return map[string]any{
		"$sub": map[string]any{
			"left": map[string]any{"$val": 0},
			"right": map[string]any{
				"$div": map[string]any{
					"left": map[string]any{"$val": k},
					"right": map[string]any{
						"$sum": []any{map[string]any{"$val": k}, rank},
					},
				},
			},
		},
	}
}

// rrfRankExpr builds Chroma's normalized weighted RRF expression. Each KNN
// supplies a missing-rank default so the candidate set is the branch union.
func rrfRankExpr(
	dense, sparse map[string]any,
	k uint32,
	denseWeight, sparseWeight float64,
) map[string]any {
	term := func(rank map[string]any, weight float64) map[string]any {
		return map[string]any{
			"$div": map[string]any{
				"left": map[string]any{"$val": weight * float64(k)},
				"right": map[string]any{
					"$sum": []any{
						map[string]any{"$val": k},
						rank,
					},
				},
			},
		}
	}
	return map[string]any{
		"$sub": map[string]any{
			"left": map[string]any{"$val": 0},
			"right": map[string]any{
				"$sum": []any{
					term(dense, denseWeight),
					term(sparse, sparseWeight),
				},
			},
		},
	}
}

// hybridCandidateLimit returns the widened Cloud KNN candidate count.
func hybridCandidateLimit(limit int) int {
	n := limit * defaultCandidateRatio
	if n < limit {
		n = limit
	}
	if n < defaultMinCandidates {
		n = defaultMinCandidates
	}
	return n
}

// collectGet pages Get until maxRecords unique IDs are collected. maxRecords
// <= 0 reads every matching page.
func (vs *VectorStore) collectGet(
	ctx context.Context,
	p storage.GetParams,
	maxRecords int,
) ([]*vectorstore.ScoredDocument, error) {
	var docs []*vectorstore.ScoredDocument
	if err := vs.forEachGetPage(ctx, p, 0, maxRecords, func(res *storage.GetResult) error {
		page, err := vs.scoredFromGet(res, 0)
		if err != nil {
			return err
		}
		docs = append(docs, page...)
		return nil
	}); err != nil {
		return nil, err
	}
	return docs, nil
}

// scoredFromQuery converts a Chroma QueryResult into scored documents.
func (vs *VectorStore) scoredFromQuery(res *storage.QueryResult) ([]*vectorstore.ScoredDocument, error) {
	if res == nil || len(res.IDs) == 0 {
		return nil, nil
	}
	ids := res.IDs[0]
	var docs []string
	if len(res.Documents) > 0 {
		docs = res.Documents[0]
	}
	var mds []map[string]any
	if len(res.Metadatas) > 0 {
		mds = res.Metadatas[0]
	}
	var embs [][]float32
	if len(res.Embeddings) > 0 {
		embs = res.Embeddings[0]
	}
	var dists []float32
	if len(res.Distances) > 0 {
		dists = res.Distances[0]
	}
	get := &storage.GetResult{IDs: ids, Documents: docs, Metadatas: mds, Embeddings: embs}
	out := make([]*vectorstore.ScoredDocument, 0, len(ids))
	for i := range ids {
		doc, _, err := vs.recordToDoc(get, i)
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue
		}
		if i >= len(dists) {
			return nil, fmt.Errorf("chroma: query returned no distance for document %q", ids[i])
		}
		if len(res.DistanceValid) > 0 && len(res.DistanceValid[0]) > i && !res.DistanceValid[0][i] {
			return nil, fmt.Errorf("chroma: query returned null distance for document %q", ids[i])
		}
		score := cosineScore(dists[i])
		out = append(out, &vectorstore.ScoredDocument{Document: doc, Score: score})
	}
	return out, nil
}

// negativeRankScore converts a negative similarity-oriented rank score to the
// framework's larger-is-better convention.
func negativeRankScore(score float32) float64 {
	return -float64(score)
}

// scoredFromSearch converts a Chroma SearchResult into scored documents.
func (vs *VectorStore) scoredFromSearch(
	res *storage.SearchResult,
	scoreFromWire func(float32) float64,
) ([]*vectorstore.ScoredDocument, error) {
	if res == nil || len(res.IDs) == 0 {
		return nil, nil
	}
	ids := res.IDs[0]
	var docs []*string
	if len(res.Documents) > 0 {
		docs = res.Documents[0]
	}
	var mds []map[string]any
	if len(res.Metadatas) > 0 {
		mds = res.Metadatas[0]
	}
	var scores []*float32
	if len(res.Scores) > 0 {
		scores = res.Scores[0]
	}
	out := make([]*vectorstore.ScoredDocument, 0, len(ids))
	for i, id := range ids {
		get := &storage.GetResult{
			IDs:        []string{id},
			Documents:  []string{""},
			Metadatas:  []map[string]any{nil},
			Embeddings: [][]float32{nil},
		}
		if i < len(docs) && docs[i] != nil {
			get.Documents[0] = *docs[i]
		}
		if i < len(mds) && mds[i] != nil {
			get.Metadatas[0] = mds[i]
		}
		if len(res.Embeddings) > 0 && len(res.Embeddings[0]) > i && res.Embeddings[0][i] != nil {
			get.Embeddings[0] = res.Embeddings[0][i]
		}
		doc, _, err := vs.recordToDoc(get, 0)
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue
		}
		if i >= len(scores) || scores[i] == nil {
			return nil, fmt.Errorf("chroma: search returned null score for document %q", id)
		}
		score := scoreFromWire(*scores[i])
		if math.IsNaN(score) || math.IsInf(score, 0) ||
			score < -scoreEpsilon || score > 1+scoreEpsilon {
			return nil, fmt.Errorf("chroma: search returned invalid score %v for document %q", score, id)
		}
		score = max(0, min(1, score))
		out = append(out, &vectorstore.ScoredDocument{
			Document: doc,
			Score:    score,
		})
	}
	return out, nil
}

// scoredFromGet converts a Chroma GetResult into scored documents with a fixed score.
func (vs *VectorStore) scoredFromGet(res *storage.GetResult, score float64) ([]*vectorstore.ScoredDocument, error) {
	if res == nil {
		return nil, nil
	}
	out := make([]*vectorstore.ScoredDocument, 0, len(res.IDs))
	for i := range res.IDs {
		doc, _, err := vs.recordToDoc(res, i)
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue
		}
		out = append(out, &vectorstore.ScoredDocument{Document: doc, Score: score})
	}
	return out, nil
}
