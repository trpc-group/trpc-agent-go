//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"database/sql"
	"fmt"
	"os"

	// Register the sqlite3 driver for both session and memory sqlite services.
	_ "github.com/mattn/go-sqlite3"

	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// newInMemoryBackend constructs the baseline in-memory backend.
func newInMemoryBackend(summarizer summary.SessionSummarizer) (*Backend, error) {
	return &Backend{
		Name:              "inmemory",
		Session:           inmemory.NewSessionService(inmemory.WithSummarizer(summarizer)),
		Memory:            meminmemory.NewMemoryService(),
		SupportsEventPage: false,
		SupportsTTL:       true,
	}, nil
}

// newSQLiteBackend constructs the sqlite-backed persistent backend, creating a
// temp DB file for the session store and another for the memory store.
func newSQLiteBackend(summarizer summary.SessionSummarizer) (*Backend, error) {
	sessDB, sessPath, err := openTempSQLite()
	if err != nil {
		return nil, fmt.Errorf("open session sqlite: %w", err)
	}
	memDB, memPath, err := openTempSQLite()
	if err != nil {
		_ = sessDB.Close()
		_ = os.Remove(sessPath)
		return nil, fmt.Errorf("open memory sqlite: %w", err)
	}

	sessSvc, err := sqlite.NewService(sessDB, sqlite.WithSummarizer(summarizer))
	if err != nil {
		_ = sessDB.Close()
		_ = memDB.Close()
		_ = os.Remove(sessPath)
		_ = os.Remove(memPath)
		return nil, fmt.Errorf("new session sqlite service: %w", err)
	}
	memSvc, err := memsqlite.NewService(memDB)
	if err != nil {
		_ = sessSvc.Close()
		_ = memDB.Close()
		_ = os.Remove(sessPath)
		_ = os.Remove(memPath)
		return nil, fmt.Errorf("new memory sqlite service: %w", err)
	}

	return &Backend{
		Name:              "sqlite",
		Session:           sessSvc,
		Memory:            memSvc,
		SupportsEventPage: false,
		SupportsTTL:       false,
		cleanup: func() {
			_ = os.Remove(sessPath)
			_ = os.Remove(memPath)
		},
	}, nil
}

// openTempSQLite opens a fresh sqlite database backed by a temp file.
func openTempSQLite() (*sql.DB, string, error) {
	f, err := os.CreateTemp("", "trpc-replaytest-*.db")
	if err != nil {
		return nil, "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, "", err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		_ = os.Remove(path)
		return nil, "", err
	}
	return db, path, nil
}
