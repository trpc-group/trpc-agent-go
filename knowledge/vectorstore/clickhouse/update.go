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
