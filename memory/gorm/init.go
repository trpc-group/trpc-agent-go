//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gormmemory

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

func (s *Service) initDB(ctx context.Context) error {
	if err := s.db.WithContext(ctx).Table(s.tableName).AutoMigrate(&memoryRow{}); err != nil {
		return err
	}
	return s.ensureMemoryIndexes(ctx)
}

func (s *Service) ensureMemoryIndexes(ctx context.Context) error {
	indexes := []struct {
		name    string
		columns string
	}{
		{name: fmt.Sprintf("idx_%s_app_user", s.tableName), columns: "(app_name, user_id)"},
		{name: fmt.Sprintf("idx_%s_updated_at", s.tableName), columns: "(updated_at)"},
		{name: fmt.Sprintf("idx_%s_deleted_at", s.tableName), columns: "(deleted_at)"},
	}
	for _, idx := range indexes {
		sql := fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s %s",
			idx.name,
			s.tableName,
			idx.columns,
		)
		if err := s.db.WithContext(ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("create index %s: %w", idx.name, err)
		}
	}
	return nil
}

func (s *Service) memoryTable(ctx context.Context) *gorm.DB {
	return s.memoryTableWithDB(ctx, s.db)
}

func (s *Service) memoryTableWithDB(ctx context.Context, db *gorm.DB) *gorm.DB {
	q := db.WithContext(ctx).Table(s.tableName)
	if !s.opts.softDelete {
		return q.Unscoped()
	}
	return q.Model(&memoryRow{})
}

func wrapDBErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("gorm memory service %s failed: %w", op, err)
}
