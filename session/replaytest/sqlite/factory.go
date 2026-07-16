// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// Open creates isolated SQLite session and memory services under dir.
// When dir is empty, a temporary directory is created and removed by cleanup.
// cleanup closes both services and removes a temp directory when Open created one.
func Open(dir string) (session.Service, memory.Service, replaytest.BackendProfile, func(), error) {
	var err error
	ownedTemp := false
	if dir == "" {
		dir, err = os.MkdirTemp("", "replaytest-sqlite-*")
		if err != nil {
			return nil, nil, replaytest.BackendProfile{}, nil, err
		}
		ownedTemp = true
	} else if err = os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}

	sessionDB, err := sql.Open("sqlite3", filepath.Join(dir, "session.db"))
	if err != nil {
		if ownedTemp {
			_ = os.RemoveAll(dir)
		}
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}
	memoryDB, err := sql.Open("sqlite3", filepath.Join(dir, "memory.db"))
	if err != nil {
		_ = sessionDB.Close()
		if ownedTemp {
			_ = os.RemoveAll(dir)
		}
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}

	sess, err := sessionsqlite.NewService(
		sessionDB,
		sessionsqlite.WithSummarizer(replaytest.NewFakeSummarizer()),
	)
	if err != nil {
		_ = sessionDB.Close()
		_ = memoryDB.Close()
		if ownedTemp {
			_ = os.RemoveAll(dir)
		}
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}
	mem, err := memorysqlite.NewService(memoryDB)
	if err != nil {
		_ = sess.Close()
		_ = memoryDB.Close()
		if ownedTemp {
			_ = os.RemoveAll(dir)
		}
		return nil, nil, replaytest.BackendProfile{}, nil, err
	}

	profile := replaytest.SQLiteProfile()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = sess.Close()
			_ = mem.Close()
			if ownedTemp {
				_ = os.RemoveAll(dir)
			}
		})
	}
	return sess, mem, profile, cleanup, nil
}

// sessionCloser wraps session.Service.Close to also run cleanup.
type sessionCloser struct {
	session.Service
	cleanup func()
}

func (s sessionCloser) Close() error {
	err := s.Service.Close()
	if s.cleanup != nil {
		s.cleanup()
	}
	return err
}

// Factory returns a replaytest.BackendFactory backed by temporary SQLite files.
// Closing the returned session service also closes the memory service and
// removes the temporary directory created by Open.
func Factory() replaytest.BackendFactory {
	return func() (session.Service, memory.Service, replaytest.BackendProfile, error) {
		sess, mem, profile, cleanup, err := Open("")
		if err != nil {
			return nil, nil, profile, err
		}
		return sessionCloser{Service: sess, cleanup: cleanup}, mem, profile, nil
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
