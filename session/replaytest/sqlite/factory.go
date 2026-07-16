//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// Open creates isolated SQLite session and memory services under dir.
// When dir is empty, a temporary directory is created.
// cleanup closes both services.
func Open(dir string) (session.Service, memory.Service, replaytest.BackendProfile, func(), error) {
	var err error
	if dir == "" {
		dir, err = os.MkdirTemp("", "replaytest-sqlite-*")
		if err != nil {
			return nil, nil, replaytest.BackendProfile{}, nil, err
		}
	} else if err = os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}

	sessionDB, err := sql.Open("sqlite3", filepath.Join(dir, "session.db"))
	if err != nil {
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}
	memoryDB, err := sql.Open("sqlite3", filepath.Join(dir, "memory.db"))
	if err != nil {
		_ = sessionDB.Close()
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}

	sess, err := sessionsqlite.NewService(
		sessionDB,
		sessionsqlite.WithSummarizer(replaytest.NewFakeSummarizer()),
	)
	if err != nil {
		_ = sessionDB.Close()
		_ = memoryDB.Close()
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}
	mem, err := memorysqlite.NewService(memoryDB)
	if err != nil {
		_ = sess.Close()
		_ = memoryDB.Close()
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}

	profile := replaytest.SQLiteProfile()
	cleanup := func() {
		_ = sess.Close()
		_ = mem.Close()
	}
	return sess, mem, profile, cleanup, nil
}

// Factory returns a replaytest.BackendFactory backed by temporary SQLite files.
func Factory() replaytest.BackendFactory {
	return func() (session.Service, memory.Service, replaytest.BackendProfile, error) {
		sess, mem, profile, cleanup, err := Open("")
		if err != nil {
			return nil, nil, profile, err
		}
		// Wrap cleanup into Close when possible; callers should still Close services.
		_ = cleanup
		return sess, mem, profile, nil
	}
}

// NamedBackend builds a NamedBackend for the harness.
func NamedBackend(name string, sess session.Service, mem memory.Service, profile replaytest.BackendProfile) replaytest.NamedBackend {
	if name == "" {
		name = profile.Name
	}
	return replaytest.NamedBackend{
		Name:           name,
		Profile:        profile,
		SessionService: sess,
		MemoryService:  mem,
	}
}
