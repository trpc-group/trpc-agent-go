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
