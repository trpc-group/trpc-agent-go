//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replayconsistency assembles concrete backends for the reusable
// session/replaytest harness.
package replayconsistency

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// LightweightFactories returns the credential-free InMemory and SQLite
// backends used by the default replay suite.
func LightweightFactories() []replaytest.BackendFactory {
	return []replaytest.BackendFactory{
		inMemoryFactory(),
		sqliteFactory(),
	}
}

func inMemoryFactory() replaytest.BackendFactory {
	capabilities := replaytest.CoreCapabilities()
	capabilities.TTL = true
	return replaytest.BackendFactory{
		Name:         "inmemory",
		Capabilities: capabilities,
		Open: func(
			ctx context.Context,
			replayCase replaytest.ReplayCase,
		) (*replaytest.Backend, error) {
			eventLimit := replayCase.EventLimit
			if eventLimit == 0 {
				eventLimit = 1000
			}
			sessionService := sessioninmemory.NewSessionService(
				sessioninmemory.WithSessionEventLimit(eventLimit),
				sessioninmemory.WithSummarizer(
					replaytest.NewTranscriptSummarizer(),
				),
				sessioninmemory.WithSummaryFilterAllowlist(
					replaytest.SummaryFilterKeys(replayCase)...,
				),
				sessioninmemory.WithCascadeFullSessionSummary(false),
			)
			memoryService := memoryinmemory.NewMemoryService(
				memoryinmemory.WithMinSearchScore(0),
				memoryinmemory.WithMaxResults(100),
			)
			return &replaytest.Backend{
				Name:         "inmemory",
				Session:      sessionService,
				Memory:       memoryService,
				Capabilities: capabilities,
				Close: func() error {
					memoryErr := memoryService.Close()
					sessionErr := sessionService.Close()
					if memoryErr != nil {
						return memoryErr
					}
					return sessionErr
				},
			}, nil
		},
	}
}

func sqliteFactory() replaytest.BackendFactory {
	capabilities := replaytest.CoreCapabilities()
	capabilities.TTL = true
	return replaytest.BackendFactory{
		Name:         "sqlite",
		Capabilities: capabilities,
		Open: func(
			ctx context.Context,
			replayCase replaytest.ReplayCase,
		) (*replaytest.Backend, error) {
			root, err := os.MkdirTemp("", "trpc-replay-sqlite-*")
			if err != nil {
				return nil, fmt.Errorf("create sqlite temp directory: %w", err)
			}
			cleanupOnError := func() {
				_ = os.RemoveAll(root)
			}

			sessionDB, err := openSQLite(
				filepath.Join(root, "session.db"),
			)
			if err != nil {
				cleanupOnError()
				return nil, err
			}
			eventLimit := replayCase.EventLimit
			if eventLimit == 0 {
				eventLimit = 1000
			}
			sessionService, err := sessionsqlite.NewService(
				sessionDB,
				sessionsqlite.WithSessionEventLimit(eventLimit),
				sessionsqlite.WithSummarizer(
					replaytest.NewTranscriptSummarizer(),
				),
				sessionsqlite.WithSummaryFilterAllowlist(
					replaytest.SummaryFilterKeys(replayCase)...,
				),
				sessionsqlite.WithCascadeFullSessionSummary(false),
			)
			if err != nil {
				_ = sessionDB.Close()
				cleanupOnError()
				return nil, fmt.Errorf(
					"create sqlite session service: %w",
					err,
				)
			}

			memoryDB, err := openSQLite(
				filepath.Join(root, "memory.db"),
			)
			if err != nil {
				_ = sessionService.Close()
				cleanupOnError()
				return nil, err
			}
			memoryService, err := memorysqlite.NewService(
				memoryDB,
				memorysqlite.WithMinSearchScore(0),
				memorysqlite.WithMaxResults(100),
			)
			if err != nil {
				_ = memoryDB.Close()
				_ = sessionService.Close()
				cleanupOnError()
				return nil, fmt.Errorf(
					"create sqlite memory service: %w",
					err,
				)
			}

			return &replaytest.Backend{
				Name:         "sqlite",
				Session:      sessionService,
				Memory:       memoryService,
				Capabilities: capabilities,
				Close: func() error {
					memoryErr := memoryService.Close()
					sessionErr := sessionService.Close()
					removeErr := os.RemoveAll(root)
					switch {
					case memoryErr != nil:
						return memoryErr
					case sessionErr != nil:
						return sessionErr
					default:
						return removeErr
					}
				},
			}, nil
		},
	}
}

func openSQLite(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on",
		path,
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}
	return db, nil
}
