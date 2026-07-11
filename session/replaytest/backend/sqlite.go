//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package backend provides temporary backend fixtures for replay tests.
package backend

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// NewSQLiteService creates a temporary SQLite session service and cleans it up after the test.
func NewSQLiteService(t *testing.T, opts ...sqlite.ServiceOpt) session.Service {
	t.Helper()

	f, err := os.CreateTemp("", "trpc-agent-go-replaytest-*.db")
	if err != nil {
		t.Fatalf("创建临时 sqlite 数据库失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭临时 sqlite 数据库失败: %v", err)
	}

	db, err := sql.Open("sqlite3", f.Name())
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatalf("打开 sqlite 数据库失败: %v", err)
	}

	svc, err := sqlite.NewService(db, opts...)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatalf("创建 sqlite 服务失败: %v", err)
	}

	t.Cleanup(func() {
		_ = svc.Close()
		_ = os.Remove(f.Name())
	})

	return svc
}
