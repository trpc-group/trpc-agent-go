//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlitevec

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const (
	defaultTableName = "memories"

	sqlCheckVecVersion = `SELECT vec_version();`

	sqlCreateMemoriesTable = `
CREATE VIRTUAL TABLE IF NOT EXISTS {{TABLE_NAME}} USING vec0(
  memory_id text primary key,
  embedding float[{{DIMENSION}}] distance_metric=cosine,
  app_name text,
  user_id text,
  created_at integer,
  updated_at integer,
  deleted_at integer,
  +memory_content text,
  +topics text,
  +memory_kind text,
  +event_time integer,
  +participants text,
  +location text
);`
)

func (s *Service) initDB(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, sqlCheckVecVersion); err != nil {
		return fmt.Errorf("sqlite-vec is not available: %w", err)
	}

	tableSQL := strings.ReplaceAll(
		sqlCreateMemoriesTable,
		"{{TABLE_NAME}}",
		s.tableName,
	)
	tableSQL = strings.ReplaceAll(
		tableSQL,
		"{{DIMENSION}}",
		fmt.Sprintf("%d", s.opts.indexDimension),
	)
	if _, err := s.db.ExecContext(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s: %w", s.tableName, err)
	}

	if err := s.ensureSchemaColumns(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Service) ensureSchemaColumns(ctx context.Context) error {
	const pragma = `PRAGMA table_info(%s);`
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(pragma, s.tableName))
	if err != nil {
		return fmt.Errorf("inspect table %s schema: %w", s.tableName, err)
	}
	defer rows.Close()

	required := map[string]struct{}{
		"memory_id":      {},
		"embedding":      {},
		"app_name":       {},
		"user_id":        {},
		"created_at":     {},
		"updated_at":     {},
		"deleted_at":     {},
		"memory_content": {},
		"topics":         {},
		"memory_kind":    {},
		"event_time":     {},
		"participants":   {},
		"location":       {},
	}
	found := make(map[string]struct{}, len(required))

	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan table %s schema: %w", s.tableName, err)
		}
		found[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table %s schema: %w", s.tableName, err)
	}

	missing := make([]string, 0)
	for column := range required {
		if _, ok := found[column]; !ok {
			missing = append(missing, column)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return fmt.Errorf(
		"sqlitevec table %s has outdated schema; recreate it to add columns: %s",
		s.tableName,
		strings.Join(missing, ", "),
	)
}
