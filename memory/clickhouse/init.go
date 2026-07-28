//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"fmt"
)

const createMemoriesTable = `
	CREATE TABLE IF NOT EXISTS %s (
		app_name String,
		user_id String,
		memory_id String,
		memory_data String,
		created_at DateTime64(6),
		updated_at DateTime64(6),
		deleted_at Nullable(DateTime64(6))
	) ENGINE = ReplacingMergeTree(updated_at)
	PARTITION BY (app_name, cityHash64(user_id) %% 64)
	ORDER BY (app_name, user_id, memory_id)
	SETTINGS allow_nullable_key = 1`

func (s *Service) initDB(ctx context.Context) error {
	if err := s.client.Exec(ctx, fmt.Sprintf(createMemoriesTable, s.tableName)); err != nil {
		return fmt.Errorf("create table %s: %w", s.tableName, err)
	}
	return nil
}
