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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

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
