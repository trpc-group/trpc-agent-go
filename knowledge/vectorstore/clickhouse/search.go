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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
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

// DeleteByFilter deletes documents according to the supplied options.
func (vs *VectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	cfg := vectorstore.ApplyDeleteOptions(opts...)
	if cfg.DeleteAll {
		if len(cfg.DocumentIDs) > 0 || len(cfg.Filter) > 0 {
			return errors.New(
				"clickhouse: WithDeleteAll cannot be combined with WithDeleteDocumentIDs or WithDeleteFilter")
		}
		return vs.deleteAll(ctx)
	}
	if len(cfg.DocumentIDs) == 0 && len(cfg.Filter) == 0 {
		return errors.New("clickhouse: DeleteByFilter requires DocumentIDs, Filter, or DeleteAll")
	}
	where, err := vs.buildDeletePredicate(cfg.DocumentIDs, cfg.Filter)
	if err != nil {
		return err
	}
	whereSQL, args := where.whereClause()
	sql := fmt.Sprintf("ALTER TABLE %s DELETE%s", vs.option.tableName, whereSQL)
	// Match Delete: wait for the mutation unless the caller opted out, so the
	// deleted rows are gone by the time this returns nil.
	if err := vs.client.Exec(vs.mutationContext(ctx), sql, args...); err != nil {
		return fmt.Errorf("clickhouse: delete by filter: %w", err)
	}
	return nil
}

// buildDeletePredicate builds the predicate selecting the rows a delete or a
// metadata query touches, from IDs and/or a metadata filter.
func (vs *VectorStore) buildDeletePredicate(ids []string, filter map[string]any) (predicate, error) {
	md, err := vs.metadataMapToExpr(filter)
	if err != nil {
		return predicate{}, err
	}
	return vs.idPredicate(ids).and(md), nil
}

// deleteAll clears the whole table. It is destructive; callers opt in per
// operation through vectorstore.WithDeleteAll(true), matching the other vector
// store implementations.
func (vs *VectorStore) deleteAll(ctx context.Context) error {
	// ClickHouse has no TRUNCATE; use a broad ALTER TABLE DELETE mutation.
	sql := fmt.Sprintf("ALTER TABLE %s DELETE WHERE 1", vs.option.tableName)
	if err := vs.client.Exec(vs.mutationContext(ctx), sql); err != nil {
		return fmt.Errorf("clickhouse: delete all: %w", err)
	}
	return nil
}

// Count returns the number of matching documents.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	cfg := vectorstore.ApplyCountOptions(opts...)
	md, err := vs.metadataMapToExpr(cfg.Filter)
	if err != nil {
		return 0, err
	}
	whereSQL, _ := md.whereClause()
	sql := fmt.Sprintf("SELECT count() FROM %s FINAL%s", vs.option.tableName, whereSQL)
	rows, err := vs.client.Query(ctx, sql)
	if err != nil {
		return 0, fmt.Errorf("clickhouse: count: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("clickhouse: count: %w", err)
		}
		return 0, errors.New("clickhouse: count returned no rows")
	}
	var count uint64
	if err := rows.Scan(&count); err != nil {
		return 0, fmt.Errorf("clickhouse: scan count: %w", err)
	}
	return int(count), nil
}

// GetMetadata retrieves metadata for matching documents. With Limit=-1 it
// retrieves all pages using defaultBatchSize.
func (vs *VectorStore) GetMetadata(
	ctx context.Context,
	opts ...vectorstore.GetMetadataOption,
) (map[string]vectorstore.DocumentMetadata, error) {
	cfg, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}

	out := map[string]vectorstore.DocumentMetadata{}
	if cfg.Limit > 0 {
		idMap, _, err := vs.queryMetadataOnce(ctx, cfg.IDs, cfg.Filter, cfg.Limit, cfg.Offset)
		if err != nil {
			return nil, err
		}
		for id, md := range idMap {
			out[id] = vectorstore.DocumentMetadata{Metadata: md}
		}
		return out, nil
	}

	// Limit < 0 retrieves all pages. ApplyGetMetadataOptions rejects a negative
	// limit combined with a positive offset, so paging always starts at zero here.
	//
	// Pagination advances by the number of rows scanned, not by len(idMap): a
	// page may contain duplicate IDs, and using the deduplicated size would both
	// under-advance the offset and end the loop early, silently dropping the
	// remaining rows.
	var offset int
	for {
		idMap, scanned, err := vs.queryMetadataOnce(ctx, cfg.IDs, cfg.Filter, defaultBatchSize, offset)
		if err != nil {
			return nil, err
		}
		if scanned == 0 {
			break
		}
		for id, md := range idMap {
			out[id] = vectorstore.DocumentMetadata{Metadata: md}
		}
		if scanned < defaultBatchSize {
			break
		}
		offset += scanned
	}
	return out, nil
}

// queryMetadataOnce retrieves one page of (id, metadata) pairs. It also returns
// the number of rows actually scanned, which can exceed len(map) when a page
// contains duplicate IDs. Callers must page on the row count, not the map size.
func (vs *VectorStore) queryMetadataOnce(
	ctx context.Context,
	ids []string,
	filter map[string]any,
	limit, offset int,
) (map[string]map[string]any, int, error) {
	where, err := vs.buildDeletePredicate(ids, filter)
	if err != nil {
		return nil, 0, err
	}
	whereSQL, args := where.whereClause()
	// ORDER BY must follow WHERE, and it is required for stable LIMIT/OFFSET
	// pagination: without it ClickHouse returns rows in an arbitrary order, so
	// successive pages can overlap or skip IDs.
	sql := fmt.Sprintf("%s%s ORDER BY %s LIMIT ? OFFSET ?",
		vs.buildMetadataSelectSQL(), whereSQL, vs.option.idFieldName)
	args = append(args, limit, offset)

	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("clickhouse: get metadata: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	var scanned int
	for rows.Next() {
		id, md, err := vs.scanMetadataRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out[id] = md
		scanned++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("clickhouse: get metadata: %w", err)
	}
	return out, scanned, nil
}

// metadataColumns returns the ordered column list for metadata-only queries.
func (vs *VectorStore) metadataColumns() []string {
	o := vs.option
	cols := []string{o.idFieldName, o.metadataFieldName}
	for _, spec := range o.filterFields {
		cols = append(cols, spec.Name)
	}
	return cols
}

// buildMetadataSelectSQL builds
// "SELECT id, metadata [, filter fields] FROM <table> FINAL".
//
// The ORDER BY is appended separately by queryMetadataOnce, after the WHERE
// clause, so the two cannot end up in the wrong order.
func (vs *VectorStore) buildMetadataSelectSQL() string {
	return fmt.Sprintf("SELECT %s FROM %s FINAL",
		strings.Join(vs.metadataColumns(), ", "), vs.option.tableName)
}

// scanMetadataRow scans a (id, metadata [, filter fields]) row.
func (vs *VectorStore) scanMetadataRow(rows interface{ Scan(dest ...any) error }) (string, map[string]any, error) {
	var id string
	var metadataStr string
	filterDests := vs.newFilterDests()
	targets := []any{&id, &metadataStr}
	targets = append(targets, filterDests...)
	if err := rows.Scan(targets...); err != nil {
		return "", nil, fmt.Errorf("clickhouse: scan metadata row: %w", err)
	}
	// The embedding text is dropped here: GetMetadata reports caller metadata
	// only. unmarshalMetadata already strips the internal envelope.
	md, _, err := unmarshalMetadata(metadataStr)
	if err != nil {
		return "", nil, fmt.Errorf("clickhouse: decode metadata: %w", err)
	}
	vs.mergeFilterDests(md, filterDests)
	return id, md, nil
}

// UpdateByFilter partially updates documents selected by DocumentIDs and/or a
// FilterCondition. It reads the matching rows once, applies the field updates in
// memory, and rewrites every updated version with a single batch INSERT.
//
// The returned count is the number of documents rewritten. Because the rewrite
// is one INSERT, the backend applies it as a unit: the count is either the full
// number of matches on success, or 0 together with an error. The operation is
// not divided into per-document commits, so callers never observe a partially
// applied count.
func (vs *VectorStore) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	cfg, err := vectorstore.ApplyUpdateByFilterOptions(opts...)
	if err != nil {
		return 0, err
	}

	where, err := vs.buildUpdatePredicate(cfg.DocumentIDs, cfg.FilterCondition)
	if err != nil {
		return 0, err
	}
	if where.empty() {
		return 0, errors.New("clickhouse: UpdateByFilter requires DocumentIDs or FilterCondition")
	}
	whereSQL, args := where.whereClause()

	// Read the full matching rows instead of only their IDs: each document is
	// needed to build its new version, and a single query avoids the 1+3N round
	// trips of reading and re-reading one document at a time.
	sql := fmt.Sprintf("%s%s", vs.buildSelectSQL(), whereSQL)
	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("clickhouse: update by filter: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	var batchArgs []any
	var count int
	for rows.Next() {
		current, err := vs.scanRow(rows, nil)
		if err != nil {
			return 0, err
		}
		if current == nil {
			continue
		}
		doc, embedding, err := vs.rowToDoc(current)
		if err != nil {
			return 0, err
		}
		newDoc, newEmbedding, err := applyUpdatesToDoc(doc, embedding, cfg.Updates)
		if err != nil {
			return 0, err
		}
		newRow, err := vs.docToRow(newDoc, newEmbedding, now)
		if err != nil {
			return 0, err
		}
		// Preserve the original creation time, as Update does.
		if !current.createdAt.IsZero() {
			newRow.createdAt = current.createdAt
		}
		insertArgs, err := vs.insertArgs(newRow)
		if err != nil {
			return 0, err
		}
		batchArgs = append(batchArgs, insertArgs...)
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("clickhouse: update by filter: %w", err)
	}
	if count == 0 {
		return 0, nil
	}

	if err := vs.client.Exec(ctx, vs.buildInsertSQLRows(count), batchArgs...); err != nil {
		return 0, fmt.Errorf("clickhouse: update by filter: %w", err)
	}
	return int64(count), nil
}

// buildUpdatePredicate builds the predicate selecting the rows an update by
// filter touches, from IDs and/or a filter condition.
func (vs *VectorStore) buildUpdatePredicate(
	ids []string,
	cond *searchfilter.UniversalFilterCondition,
) (predicate, error) {
	expr, err := buildFilterExpr(cond, vs.allowedFilterFields())
	if err != nil {
		return predicate{}, err
	}
	return vs.idPredicate(ids).and(literalPredicate(expr)), nil
}

// applyUpdatesToDoc applies the UpdateByFilterConfig.Updates map to a document.
//
// Field mappings:
//   - name      -> doc.Name
//   - content   -> doc.Content
//   - embedding -> the returned embedding (value must be []float64)
//   - metadata.X -> doc.Metadata[X]
func applyUpdatesToDoc(doc *document.Document, embedding []float64, updates map[string]any) (*document.Document, []float64, error) {
	newDoc := doc.Clone()
	if newDoc.Metadata == nil {
		newDoc.Metadata = map[string]any{}
	}
	newEmbedding := embedding
	for key, val := range updates {
		switch {
		case key == "name":
			s, ok := val.(string)
			if !ok {
				return nil, nil, fmt.Errorf("clickhouse: updates[name] must be string, got %T", val)
			}
			newDoc.Name = s
		case key == "content":
			s, ok := val.(string)
			if !ok {
				return nil, nil, fmt.Errorf("clickhouse: updates[content] must be string, got %T", val)
			}
			newDoc.Content = s
		case key == "embedding":
			emb, ok := val.([]float64)
			if !ok {
				return nil, nil, fmt.Errorf("clickhouse: updates[embedding] must be []float64, got %T", val)
			}
			newEmbedding = emb
		case strings.HasPrefix(key, "metadata."):
			mdKey := key[len("metadata."):]
			if mdKey == "" {
				return nil, nil, fmt.Errorf("clickhouse: updates key %q is invalid (empty metadata key)", key)
			}
			newDoc.Metadata[mdKey] = val
		default:
			return nil, nil, fmt.Errorf(
				"clickhouse: updates key %q is not supported (allowed: name/content/embedding/metadata.*)", key)
		}
	}
	return newDoc, newEmbedding, nil
}
