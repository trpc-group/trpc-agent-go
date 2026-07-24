//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite binds the SQLite session and memory backends to the
// session/replaytest harness. Both the harness and this binding live in the
// session/replaytest Go module, so binding a backend never requires
// touching the backend's own module.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	msqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	ssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// Target pairs the SQLite session service with the SQLite memory service.
// Each Reset creates fresh database files so cases are fully isolated.
type Target struct {
	name string
	dir  string
	seq  int

	sessDB  *sql.DB
	memDB   *sql.DB
	sessSvc *ssqlite.Service
	memSvc  *msqlite.Service
}

// NewTarget creates a SQLite target with its own temporary directory.
func NewTarget(name string) (*Target, error) {
	dir, err := os.MkdirTemp("", "replaytest-sqlite-*")
	if err != nil {
		return nil, err
	}
	t := &Target{name: name, dir: dir}
	if err := t.Reset(context.Background()); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return t, nil
}

// Name returns the target name.
func (t *Target) Name() string { return t.name }

// Caps returns the full capability set: SQLite supports every dimension.
func (t *Target) Caps() replaytest.Capability { return replaytest.CapAll }

// SessionService returns the SQLite session service.
func (t *Target) SessionService() session.Service { return t.sessSvc }

// MemoryService returns the SQLite memory service.
func (t *Target) MemoryService() memory.Service { return t.memSvc }

// Reset recreates both services on fresh database files.
func (t *Target) Reset(ctx context.Context) error {
	t.closeServices()
	t.seq++
	sessDB, err := sql.Open("sqlite3", t.dbPath("session"))
	if err != nil {
		return fmt.Errorf("open session db: %w", err)
	}
	memDB, err := sql.Open("sqlite3", t.dbPath("memory"))
	if err != nil {
		sessDB.Close()
		return fmt.Errorf("open memory db: %w", err)
	}
	sessSvc, err := ssqlite.NewService(sessDB,
		ssqlite.WithSummarizer(replaytest.NewFakeSummarizer()))
	if err != nil {
		sessDB.Close()
		memDB.Close()
		return fmt.Errorf("create session service: %w", err)
	}
	memSvc, err := msqlite.NewService(memDB)
	if err != nil {
		sessSvc.Close()
		sessDB.Close()
		memDB.Close()
		return fmt.Errorf("create memory service: %w", err)
	}
	t.sessDB, t.memDB = sessDB, memDB
	t.sessSvc, t.memSvc = sessSvc, memSvc
	return nil
}

// Close releases services, databases and the temporary directory.
func (t *Target) Close() error {
	t.closeServices()
	return os.RemoveAll(t.dir)
}

// dbPath returns the database file path for this reset round.
func (t *Target) dbPath(kind string) string {
	return filepath.Join(t.dir, fmt.Sprintf("replay-%s-%04d.db", kind, t.seq))
}

// closeServices closes services and their databases.
func (t *Target) closeServices() {
	if t.sessSvc != nil {
		t.sessSvc.Close()
	}
	if t.memSvc != nil {
		t.memSvc.Close()
	}
	if t.sessDB != nil {
		t.sessDB.Close()
	}
	if t.memDB != nil {
		t.memDB.Close()
	}
	t.sessSvc, t.memSvc = nil, nil
	t.sessDB, t.memDB = nil, nil
}
