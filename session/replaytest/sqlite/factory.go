//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytestsqlite connects the replay contract harness to the
// file-backed SQLite session and memory implementations.
package replaytestsqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// NewBackend returns a file-backed SQLite backend. Root must be a writable
// directory; each case gets an isolated temporary subdirectory.
func NewBackend(root string) replaytest.Backend {
	return replaytest.Backend{
		Name:         "sqlite",
		Capabilities: replaytest.FullCapabilities(),
		Open: func(_ context.Context, caseName string) (*replaytest.Services, error) {
			if root == "" {
				return nil, errors.New("replaytest sqlite: root is required")
			}
			caseDir, err := os.MkdirTemp(root, sanitize(caseName)+"-")
			if err != nil {
				return nil, fmt.Errorf("create case directory: %w", err)
			}
			cleanup := func() error { return os.RemoveAll(caseDir) }

			sessionDB, err := openDB(filepath.Join(caseDir, "session.db"))
			if err != nil {
				return nil, errors.Join(fmt.Errorf("open session database: %w", err), cleanup())
			}
			sessionService, err := sessionsqlite.NewService(
				sessionDB,
				sessionsqlite.WithEnableAsyncPersist(false),
				sessionsqlite.WithSummarizer(&replaytest.DeterministicSummarizer{}),
				sessionsqlite.WithSummaryFilterAllowlist("agent/weather", "agent/research"),
				sessionsqlite.WithCascadeFullSessionSummary(false),
			)
			if err != nil {
				return nil, errors.Join(
					fmt.Errorf("create session service: %w", err),
					sessionDB.Close(),
					cleanup(),
				)
			}

			memoryDB, err := openDB(filepath.Join(caseDir, "memory.db"))
			if err != nil {
				return nil, errors.Join(
					fmt.Errorf("open memory database: %w", err),
					sessionService.Close(),
					cleanup(),
				)
			}
			memoryService, err := memorysqlite.NewService(memoryDB)
			if err != nil {
				return nil, errors.Join(
					fmt.Errorf("create memory service: %w", err),
					memoryDB.Close(),
					sessionService.Close(),
					cleanup(),
				)
			}
			return &replaytest.Services{
				Session: sessionService,
				Memory:  memoryService,
				Cleanup: cleanup,
			}, nil
		},
	}
}

func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func sanitize(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, value)
	if value == "" {
		return "case"
	}
	return value
}
