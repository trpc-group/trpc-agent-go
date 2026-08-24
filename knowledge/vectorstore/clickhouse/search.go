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

// buildWhereClause builds a " WHERE ..." clause from a SearchFilter, together with
// positional arguments for ID placeholders. Metadata and filter-condition values
// are inlined as quoted literals, while IDs use placeholders.
func (vs *VectorStore) buildWhereClause(f *vectorstore.SearchFilter) (string, []any, error) {
	var parts []string
	var args []any
	if f != nil {
		if len(f.IDs) > 0 {
			placeholders := make([]string, len(f.IDs))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			parts = append(parts, fmt.Sprintf("%s IN (%s)", vs.option.idFieldName, strings.Join(placeholders, ", ")))
			for _, id := range f.IDs {
				args = append(args, id)
			}
		}
		expr, err := vs.buildFilterFromSearch(f)
		if err != nil {
			return "", nil, err
		}
		if expr != "" {
			parts = append(parts, expr)
		}
	}
	if len(parts) == 0 {
		return "", args, nil
	}
	return " WHERE " + joinAnd(parts...), args, nil
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
	where, whereArgs, err := vs.buildWhereClause(q.Filter)
	if err != nil {
		return nil, err
	}
	distanceExpr := fmt.Sprintf("%s(%s, ?)", vs.option.metric.distanceFunction(), vs.option.embeddingFieldName)
	cols := append(append([]string{}, vs.selectColumns()...), distanceExpr+" AS _distance")
	sql := fmt.Sprintf("SELECT %s FROM %s FINAL%s ORDER BY _distance %s LIMIT ?",
		strings.Join(cols, ", "), vs.option.tableName, where, vs.option.metric.orderByDirection())
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
	where, whereArgs, err := vs.buildWhereClause(q.Filter)
	if err != nil {
		return nil, err
	}
	// No effective constraint: avoid a full scan.
	if where == "" {
		return &vectorstore.SearchResult{Results: nil}, nil
	}
	sql := fmt.Sprintf("%s%s LIMIT ?", vs.buildSelectSQL(), where)
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
	where, whereArgs, err := vs.buildWhereClause(q.Filter)
	if err != nil {
		return nil, err
	}
	keywordCond := fmt.Sprintf("positionCaseInsensitive(%s, ?) > 0", vs.option.contentFieldName)
	combined := combineWhere(keywordCond, where)
	sql := fmt.Sprintf("%s%s LIMIT ?", vs.buildSelectSQL(), combined)
	args := append([]any{q.Query}, whereArgs...)
	args = append(args, vs.limitOrDefault(q.Limit))

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
	where, whereArgs, err := vs.buildWhereClause(q.Filter)
	if err != nil {
		return nil, err
	}
	keywordCond := fmt.Sprintf("positionCaseInsensitive(%s, ?) > 0", vs.option.contentFieldName)
	combined := combineWhere(keywordCond, where)
	distanceExpr := fmt.Sprintf("%s(%s, ?)", vs.option.metric.distanceFunction(), vs.option.embeddingFieldName)
	cols := append(append([]string{}, vs.selectColumns()...), distanceExpr+" AS _distance")
	sql := fmt.Sprintf("SELECT %s FROM %s FINAL%s ORDER BY _distance %s LIMIT ?",
		strings.Join(cols, ", "), vs.option.tableName, combined, vs.option.metric.orderByDirection())
	args := append([]any{q.Vector, q.Query}, whereArgs...)
	args = append(args, vs.limitOrDefault(q.Limit))

	docs, err := vs.queryScored(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &vectorstore.SearchResult{Results: applyMinScore(docs, q.MinScore)}, nil
}

// combineWhere joins a keyword predicate with an existing " WHERE ..." clause.
// Both sides are parenthesized so a top-level OR in either one cannot escape
// its scope when the two are AND-combined.
func combineWhere(keywordCond, where string) string {
	if where == "" {
		return " WHERE " + keywordCond
	}
	// Strip the leading " WHERE " and AND-combine.
	return " WHERE " + joinAnd(keywordCond, strings.TrimPrefix(where, " WHERE "))
}

// applyMinScore retains documents whose score is at least minScore.
// Non-positive thresholds disable filtering.
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
	where, args, err := vs.buildDeleteWhere(cfg.DocumentIDs, cfg.Filter)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf("ALTER TABLE %s DELETE%s", vs.option.tableName, where)
	if err := vs.client.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("clickhouse: delete by filter: %w", err)
	}
	return nil
}

// buildDeleteWhere builds a " WHERE ..." clause for delete operations from IDs
// and/or a metadata filter.
func (vs *VectorStore) buildDeleteWhere(ids []string, filter map[string]any) (string, []any, error) {
	var parts []string
	var args []any
	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		parts = append(parts, fmt.Sprintf("%s IN (%s)", vs.option.idFieldName, strings.Join(placeholders, ", ")))
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if len(filter) > 0 {
		expr, err := vs.metadataMapToExpr(filter)
		if err != nil {
			return "", nil, err
		}
		if expr != "" {
			parts = append(parts, expr)
		}
	}
	if len(parts) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args, nil
}

// deleteAll clears the whole table. It is destructive and requires
// WithAllowDestructiveDeleteAll(true).
func (vs *VectorStore) deleteAll(ctx context.Context) error {
	if !vs.option.allowDestructiveDeleteAll {
		return errors.New(
			"clickhouse: DeleteAll is destructive (clears all documents); enable it explicitly via " +
				"WithAllowDestructiveDeleteAll(true)")
	}
	// ClickHouse has no TRUNCATE; use a broad ALTER TABLE DELETE mutation.
	sql := fmt.Sprintf("ALTER TABLE %s DELETE WHERE 1", vs.option.tableName)
	if err := vs.client.Exec(ctx, sql); err != nil {
		return fmt.Errorf("clickhouse: delete all: %w", err)
	}
	return nil
}

// Count returns the number of matching documents.
func (vs *VectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	cfg := vectorstore.ApplyCountOptions(opts...)
	where := ""
	if len(cfg.Filter) > 0 {
		expr, err := vs.metadataMapToExpr(cfg.Filter)
		if err != nil {
			return 0, err
		}
		if expr != "" {
			where = " WHERE " + expr
		}
	}
	sql := fmt.Sprintf("SELECT count() FROM %s FINAL%s", vs.option.tableName, where)
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
		idMap, err := vs.queryMetadataOnce(ctx, cfg.IDs, cfg.Filter, cfg.Limit, cfg.Offset)
		if err != nil {
			return nil, err
		}
		for id, md := range idMap {
			out[id] = vectorstore.DocumentMetadata{Metadata: md}
		}
		return out, nil
	}

	// Limit == -1 retrieves all pages.
	var offset int
	for {
		idMap, err := vs.queryMetadataOnce(ctx, cfg.IDs, cfg.Filter, defaultBatchSize, offset)
		if err != nil {
			return nil, err
		}
		if len(idMap) == 0 {
			break
		}
		for id, md := range idMap {
			out[id] = vectorstore.DocumentMetadata{Metadata: md}
		}
		if len(idMap) < defaultBatchSize {
			break
		}
		offset += len(idMap)
	}
	return out, nil
}

// queryMetadataOnce retrieves one page of (id, metadata) pairs.
func (vs *VectorStore) queryMetadataOnce(
	ctx context.Context,
	ids []string,
	filter map[string]any,
	limit, offset int,
) (map[string]map[string]any, error) {
	where, args, err := vs.buildMetadataWhere(ids, filter)
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf("%s%s LIMIT ? OFFSET ?", vs.buildMetadataSelectSQL(), where)
	args = append(args, limit, offset)

	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: get metadata: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		id, md, err := vs.scanMetadataRow(rows)
		if err != nil {
			return nil, err
		}
		out[id] = md
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: get metadata: %w", err)
	}
	return out, nil
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
// "SELECT id, metadata [, filter fields] FROM <table> FINAL ORDER BY id".
//
// The ORDER BY is required for the LIMIT/OFFSET pagination in GetMetadata:
// without it ClickHouse returns rows in an arbitrary, non-deterministic order,
// so successive pages can overlap or skip IDs.
func (vs *VectorStore) buildMetadataSelectSQL() string {
	return fmt.Sprintf("SELECT %s FROM %s FINAL ORDER BY %s",
		strings.Join(vs.metadataColumns(), ", "), vs.option.tableName, vs.option.idFieldName)
}

// buildMetadataWhere builds a " WHERE ..." clause for metadata queries.
func (vs *VectorStore) buildMetadataWhere(ids []string, filter map[string]any) (string, []any, error) {
	return vs.buildDeleteWhere(ids, filter)
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
	md, err := unmarshalMetadata(metadataStr)
	if err != nil {
		return "", nil, fmt.Errorf("clickhouse: decode metadata: %w", err)
	}
	vs.mergeFilterDests(md, filterDests)
	return id, md, nil
}

// UpdateByFilter partially updates documents selected by DocumentIDs and/or a
// FilterCondition. It reads the matching rows, applies the field updates in
// memory, and re-inserts each updated document, returning the number updated.
func (vs *VectorStore) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	cfg, err := vectorstore.ApplyUpdateByFilterOptions(opts...)
	if err != nil {
		return 0, err
	}

	// Resolve the matching document IDs.
	where, args, err := vs.buildUpdateWhere(cfg.DocumentIDs, cfg.FilterCondition)
	if err != nil {
		return 0, err
	}
	if where == "" {
		return 0, errors.New("clickhouse: UpdateByFilter requires DocumentIDs or FilterCondition")
	}
	sql := fmt.Sprintf("SELECT %s FROM %s FINAL%s", vs.option.idFieldName, vs.option.tableName, where)
	rows, err := vs.client.Query(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("clickhouse: update by filter: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("clickhouse: scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("clickhouse: update by filter: %w", err)
	}
	rows.Close()

	for _, id := range ids {
		doc, embedding, err := vs.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		newDoc, newEmbedding, err := applyUpdatesToDoc(doc, embedding, cfg.Updates)
		if err != nil {
			return 0, err
		}
		if err := vs.Update(ctx, newDoc, newEmbedding); err != nil {
			return 0, err
		}
	}
	return int64(len(ids)), nil
}

// buildUpdateWhere builds a " WHERE ..." clause for update-by-filter from IDs
// and/or a filter condition.
func (vs *VectorStore) buildUpdateWhere(ids []string, cond *searchfilter.UniversalFilterCondition) (string, []any, error) {
	var parts []string
	var args []any
	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		parts = append(parts, fmt.Sprintf("%s IN (%s)", vs.option.idFieldName, strings.Join(placeholders, ", ")))
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if cond != nil {
		expr, err := buildFilterExpr(cond, vs.allowedFilterFields())
		if err != nil {
			return "", nil, err
		}
		if expr != "" {
			parts = append(parts, expr)
		}
	}
	if len(parts) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args, nil
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
