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
  +topics text
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

	return nil
}
