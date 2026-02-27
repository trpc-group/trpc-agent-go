//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const (
	defaultTableName = "memories"

	sqlCreateMemoriesTable = `
CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  memory_id TEXT PRIMARY KEY,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  memory_data BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER DEFAULT NULL
);`

	sqlCreateMemoriesAppUserIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(app_name, user_id);`

	sqlCreateMemoriesUpdatedAtIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(updated_at DESC);`

	sqlCreateMemoriesDeletedAtIndex = `
CREATE INDEX IF NOT EXISTS {{INDEX_NAME}}
ON {{TABLE_NAME}}(deleted_at)
WHERE deleted_at IS NOT NULL;`
)

const (
	indexSuffixAppUser   = "app_user"
	indexSuffixUpdatedAt = "updated_at"
	indexSuffixDeletedAt = "deleted_at"
)

func (s *Service) initDB(ctx context.Context) error {
	tableSQL := strings.ReplaceAll(
		sqlCreateMemoriesTable,
		"{{TABLE_NAME}}",
		s.tableName,
	)
	if _, err := s.db.ExecContext(ctx, tableSQL); err != nil {
		return fmt.Errorf("create table %s: %w", s.tableName, err)
	}

	indexes := []struct {
		suffix   string
		template string
	}{
		{indexSuffixAppUser, sqlCreateMemoriesAppUserIndex},
		{indexSuffixUpdatedAt, sqlCreateMemoriesUpdatedAtIndex},
		{indexSuffixDeletedAt, sqlCreateMemoriesDeletedAtIndex},
	}

	for _, idx := range indexes {
		indexName := sqldb.BuildIndexName("", s.tableName, idx.suffix)
		indexSQL := strings.ReplaceAll(idx.template, "{{TABLE_NAME}}",
			s.tableName)
		indexSQL = strings.ReplaceAll(indexSQL, "{{INDEX_NAME}}",
			indexName)
		if _, err := s.db.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("create index %s: %w", indexName, err)
		}
	}

	return nil
}
