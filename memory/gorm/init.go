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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const maxIndexNameLength = 63

func (s *Service) initDB(ctx context.Context) error {
	if err := s.db.WithContext(ctx).Table(s.tableName).AutoMigrate(&memoryRow{}); err != nil {
		return err
	}
	return s.ensureMemoryIndexes(ctx)
}

func (s *Service) ensureMemoryIndexes(ctx context.Context) error {
	indexes := []struct {
		suffix  string
		columns []string
	}{
		{suffix: "app_user", columns: []string{"app_name", "user_id"}},
		{suffix: "updated_at", columns: []string{"updated_at"}},
		{suffix: "deleted_at", columns: []string{"deleted_at"}},
	}

	db := s.db.WithContext(ctx).Table(s.tableName)
	migrator := db.Migrator()
	for _, idx := range indexes {
		name := boundIndexName(fmt.Sprintf("idx_%s_%s", s.tableName, idx.suffix))
		if migrator.HasIndex(&memoryRow{}, name) {
			continue
		}
		if err := createMemoryIndex(db, s.tableName, name, idx.columns); err != nil {
			return fmt.Errorf("create index %s: %w", name, err)
		}
	}
	return nil
}

// createMemoryIndex builds a dialect-safe CREATE INDEX statement.
// It avoids "IF NOT EXISTS" so MySQL can initialize the same path as Postgres/SQLite.
// Identifiers are quoted through the dialector so reserved table names like "order" work.
func createMemoryIndex(db *gorm.DB, tableName, name string, columns []string) error {
	quotedName := quoteIdent(db, name)
	quotedTable := quoteIdent(db, tableName)
	quotedCols := make([]string, len(columns))
	for i, col := range columns {
		quotedCols[i] = quoteIdent(db, col)
	}
	sql := fmt.Sprintf(
		"CREATE INDEX %s ON %s (%s)",
		quotedName,
		quotedTable,
		strings.Join(quotedCols, ", "),
	)
	return db.Exec(sql).Error
}

func quoteIdent(db *gorm.DB, name string) string {
	var b strings.Builder
	db.Dialector.QuoteTo(&b, name)
	return b.String()
}

// boundIndexName keeps generated index names within common identifier limits.
func boundIndexName(name string) string {
	if len(name) <= maxIndexNameLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(sum[:8])
	keep := maxIndexNameLength - 1 - len(suffix)
	if keep < 1 {
		return suffix
	}
	return name[:keep] + "_" + suffix
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
