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
	return s.db.WithContext(ctx).Table(s.tableName).AutoMigrate(&memoryRow{})
}

func (s *Service) memoryTable(ctx context.Context) *gorm.DB {
	q := s.db.WithContext(ctx).Table(s.tableName)
	if !s.opts.softDelete {
		return q.Unscoped()
	}
	return q
}

func wrapDBErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("gorm memory service %s failed: %w", op, err)
}
