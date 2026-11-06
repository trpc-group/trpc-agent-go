//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// defaultBatchSize is the page size used when GetMetadata has Limit=-1.
const defaultBatchSize = 1000

// limitOrDefault returns limit when positive, or options.maxResults otherwise.
func (vs *VectorStore) limitOrDefault(limit int) int {
	if limit > 0 {
		return limit
	}
	return vs.option.maxResults
}

// Search dispatches to the Vector, Filter, Keyword, or Hybrid implementation.
func (vs *VectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, errors.New("clickhouse: search query is required")
	}
	switch query.SearchMode {
	case vectorstore.SearchModeFilter:
		return vs.searchByFilter(ctx, query)
	case vectorstore.SearchModeKeyword:
		return vs.searchByKeyword(ctx, query)
	case vectorstore.SearchModeVector:
		return vs.searchByVector(ctx, query)
	case vectorstore.SearchModeHybrid:
		return vs.searchByHybrid(ctx, query)
	default:
		return nil, fmt.Errorf("clickhouse: unsupported SearchMode %d", query.SearchMode)
	}
}

// buildWherePredicate combines the IDs and the metadata or filter condition of a
// SearchFilter into one predicate.
func (vs *VectorStore) buildWherePredicate(f *vectorstore.SearchFilter) (predicate, error) {
	if f == nil {
		return predicate{}, nil
	}
	expr, err := vs.buildFilterFromSearch(f)
	if err != nil {
		return predicate{}, err
	}
	return vs.idPredicate(f.IDs).and(expr), nil
}

// searchByVector performs KNN vector search with optional expression prefiltering.
func (vs *VectorStore) searchByVector(
	ctx context.Context,
	q *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	if len(q.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("%w: want=%d got=%d",
			errVectorDimMismatch, vs.option.vectorDimension, len(q.Vector))
	}
	where, err := vs.buildWherePredicate(q.Filter)
	if err != nil {
		return nil, err
	}
	whereSQL, whereArgs := where.whereClause()
	distanceExpr := fmt.Sprintf("%s(%s, ?)", vs.option.metric.distanceFunction(), vs.option.embeddingFieldName)
	cols := append(append([]string{}, vs.selectColumns()...), distanceExpr+" AS _distance")
	sql := fmt.Sprintf("SELECT %s FROM %s FINAL%s ORDER BY _distance %s LIMIT ?",
		strings.Join(cols, ", "), vs.option.tableName, whereSQL, vs.option.metric.orderByDirection())
	args := append([]any{q.Vector}, whereArgs...)
	args = append(args, vs.limitOrDefault(q.Limit))

	docs, err := vs.queryScored(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &vectorstore.SearchResult{Results: applyMinScore(docs, q.MinScore)}, nil
}

// queryScored executes a query whose trailing column is the raw distance/product
// and returns the rows as scored documents with the metric-normalized score.
func (vs *VectorStore) queryScored(ctx context.Context, sql string, args ...any) ([]*vectorstore.ScoredDocument, error) {
	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: search: %w", err)
	}
	defer rows.Close()
	var out []*vectorstore.ScoredDocument
	for rows.Next() {
		var raw float64
		r, err := vs.scanRow(rows, &raw)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		doc, _, err := vs.rowToDoc(r)
		if err != nil {
			return nil, err
		}
		out = append(out, &vectorstore.ScoredDocument{Document: doc, Score: vs.option.metric.toScore(raw)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: search: %w", err)
	}
	return out, nil
}

// searchByFilter performs a filter-only scan without vector computation.
func (vs *VectorStore) searchByFilter(
	ctx context.Context,
	q *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	// Return no results when Filter mode has no constraints to avoid a full scan.
	if q.Filter == nil ||
		(len(q.Filter.IDs) == 0 && len(q.Filter.Metadata) == 0 && q.Filter.FilterCondition == nil) {
		return &vectorstore.SearchResult{Results: nil}, nil
	}
	where, err := vs.buildWherePredicate(q.Filter)
	if err != nil {
		return nil, err
	}
	// No effective constraint: avoid a full scan.
	if where.empty() {
		return &vectorstore.SearchResult{Results: nil}, nil
	}
	whereSQL, whereArgs := where.whereClause()
	sql := fmt.Sprintf("%s%s LIMIT ?", vs.buildSelectSQL(), whereSQL)
	args := append(whereArgs, vs.limitOrDefault(q.Limit))

	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: filter search: %w", err)
	}
	defer rows.Close()
	var out []*vectorstore.ScoredDocument
	for rows.Next() {
		r, err := vs.scanRow(rows, nil)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		doc, _, err := vs.rowToDoc(r)
		if err != nil {
			return nil, err
		}
		out = append(out, &vectorstore.ScoredDocument{Document: doc, Score: 0})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: filter search: %w", err)
	}
	return &vectorstore.SearchResult{Results: out}, nil
}

// searchByKeyword performs a substring keyword match on the content column.
// ClickHouse has no built-in BM25 here, so a case-insensitive substring match is
// used. Results carry no meaningful similarity score.
func (vs *VectorStore) searchByKeyword(
	ctx context.Context,
	q *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	if q.Query == "" {
		return nil, errors.New("clickhouse: keyword is required for keyword search")
	}
	where, err := vs.buildWherePredicate(q.Filter)
	if err != nil {
		return nil, err
	}
	combined := vs.keywordPredicate(q.Query).and(where)
	whereSQL, whereArgs := combined.whereClause()
	sql := fmt.Sprintf("%s%s LIMIT ?", vs.buildSelectSQL(), whereSQL)
	args := append(whereArgs, vs.limitOrDefault(q.Limit))

	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: keyword search: %w", err)
	}
	defer rows.Close()
	var out []*vectorstore.ScoredDocument
	for rows.Next() {
		r, err := vs.scanRow(rows, nil)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		doc, _, err := vs.rowToDoc(r)
		if err != nil {
			return nil, err
		}
		out = append(out, &vectorstore.ScoredDocument{Document: doc, Score: 0})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: keyword search: %w", err)
	}
	return &vectorstore.SearchResult{Results: out}, nil
}

// searchByHybrid combines a keyword prefilter with vector ranking: the content
// must contain the query text, and the remaining candidates are ranked by vector
// similarity.
func (vs *VectorStore) searchByHybrid(
	ctx context.Context,
	q *vectorstore.SearchQuery,
) (*vectorstore.SearchResult, error) {
	if q.Query == "" {
		return nil, errors.New("clickhouse: query is required for hybrid search")
	}
	if len(q.Vector) != vs.option.vectorDimension {
		return nil, fmt.Errorf("%w: want=%d got=%d",
			errVectorDimMismatch, vs.option.vectorDimension, len(q.Vector))
	}
	where, err := vs.buildWherePredicate(q.Filter)
	if err != nil {
		return nil, err
	}
	combined := vs.keywordPredicate(q.Query).and(where)
	whereSQL, whereArgs := combined.whereClause()
	distanceExpr := fmt.Sprintf("%s(%s, ?)", vs.option.metric.distanceFunction(), vs.option.embeddingFieldName)
	cols := append(append([]string{}, vs.selectColumns()...), distanceExpr+" AS _distance")
	sql := fmt.Sprintf("SELECT %s FROM %s FINAL%s ORDER BY _distance %s LIMIT ?",
		strings.Join(cols, ", "), vs.option.tableName, whereSQL, vs.option.metric.orderByDirection())
	args := append([]any{q.Vector}, whereArgs...)
	args = append(args, vs.limitOrDefault(q.Limit))

	docs, err := vs.queryScored(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &vectorstore.SearchResult{Results: applyMinScore(docs, q.MinScore)}, nil
}

// applyMinScore retains documents whose score is at least minScore.
// Non-positive thresholds disable filtering.
func applyMinScore(docs []*vectorstore.ScoredDocument, minScore float64) []*vectorstore.ScoredDocument {
	if minScore <= 0 {
		return docs
	}
	// A fresh slice is allocated instead of reusing docs[:0], which would
	// overwrite the caller's backing array.
	out := make([]*vectorstore.ScoredDocument, 0, len(docs))
	for _, d := range docs {
		if d.Score >= minScore {
			out = append(out, d)
		}
	}
	return out
}
