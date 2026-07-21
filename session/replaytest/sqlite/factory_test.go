//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytestsqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

type backendDependencies struct {
	openDB     func(string) (*sql.DB, error)
	newSession func(*sql.DB) (session.Service, error)
	newMemory  func(*sql.DB) (memory.Service, error)
}

func defaultBackendDependencies() backendDependencies {
	return backendDependencies{
		openDB: openDB,
		newSession: func(db *sql.DB) (session.Service, error) {
			return sessionsqlite.NewService(
				db,
				sessionsqlite.WithEnableAsyncPersist(false),
				sessionsqlite.WithSummarizer(&replaytest.DeterministicSummarizer{}),
				sessionsqlite.WithSummaryFilterAllowlist("agent/weather", "agent/research"),
				sessionsqlite.WithCascadeFullSessionSummary(false),
			)
		},
		newMemory: func(db *sql.DB) (memory.Service, error) {
			return memorysqlite.NewService(db)
		},
	}
}

func sqliteBackend(root string) replaytest.Backend {
	return newBackend(root, defaultBackendDependencies())
}

func newBackend(root string, dependencies backendDependencies) replaytest.Backend {
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

			sessionDB, err := dependencies.openDB(filepath.Join(caseDir, "session.db"))
			if err != nil {
				return nil, errors.Join(fmt.Errorf("open session database: %w", err), cleanup())
			}
			sessionService, err := dependencies.newSession(sessionDB)
			if err != nil {
				return nil, errors.Join(
					fmt.Errorf("create session service: %w", err),
					sessionDB.Close(),
					cleanup(),
				)
			}

			memoryDB, err := dependencies.openDB(filepath.Join(caseDir, "memory.db"))
			if err != nil {
				return nil, errors.Join(
					fmt.Errorf("open memory database: %w", err),
					sessionService.Close(),
					cleanup(),
				)
			}
			memoryService, err := dependencies.newMemory(memoryDB)
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
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	dsn := url.URL{Scheme: "file", Path: uriPath}
	query := dsn.Query()
	query.Set("_busy_timeout", "5000")
	query.Set("_foreign_keys", "on")
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("sqlite3", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
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

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Simple-CASE_1", want: "Simple-CASE_1"},
		{input: "../../case:name", want: "------case-name"},
		{input: "", want: "case"},
		{input: "中文", want: "--"},
	}
	for _, test := range tests {
		if got := sanitize(test.input); got != test.want {
			t.Errorf("sanitize(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestSQLiteBackendRejectsInvalidRoot(t *testing.T) {
	if _, err := sqliteBackend("").Open(context.Background(), "case"); err == nil {
		t.Fatal("Open() unexpectedly accepted an empty root")
	}
	rootFile := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(rootFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := sqliteBackend(rootFile).Open(context.Background(), "case"); err == nil {
		t.Fatal("Open() unexpectedly accepted a file as its root")
	}
}

func TestSQLiteBackendSanitizesAndCleansCaseDirectory(t *testing.T) {
	root := t.TempDir()
	services, err := sqliteBackend(root).Open(context.Background(), "../../case:name")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("case directories = %v, want one directory", entries)
	}
	if err := services.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() after close error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Close() left %d entries", len(entries))
	}
}

func TestOpenDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.sqlite")
	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB() error = %v", err)
	}
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	missingParent := filepath.Join(t.TempDir(), "missing", "database.sqlite")
	if db, err := openDB(missingParent); err == nil {
		_ = db.Close()
		t.Fatal("openDB() unexpectedly created a missing parent directory")
	}

	escapedParent := filepath.Join(t.TempDir(), "dsn # %")
	if err := os.MkdirAll(escapedParent, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	escapedPath := filepath.Join(escapedParent, "database # %.sqlite")
	escapedDB, err := openDB(escapedPath)
	if err != nil {
		t.Fatalf("openDB() with URI characters error = %v", err)
	}
	if err := escapedDB.Close(); err != nil {
		t.Fatalf("Close() escaped database error = %v", err)
	}
	if _, err := os.Stat(escapedPath); err != nil {
		t.Fatalf("escaped database was not created at the requested path: %v", err)
	}
}

func TestSQLiteBackendCleansConstructionFailures(t *testing.T) {
	testErr := errors.New("injected construction failure")
	tests := []struct {
		name   string
		mutate func(*backendDependencies)
	}{
		{
			name: "open session database",
			mutate: func(dependencies *backendDependencies) {
				dependencies.openDB = func(string) (*sql.DB, error) { return nil, testErr }
			},
		},
		{
			name: "create session service",
			mutate: func(dependencies *backendDependencies) {
				dependencies.newSession = func(*sql.DB) (session.Service, error) { return nil, testErr }
			},
		},
		{
			name: "open memory database",
			mutate: func(dependencies *backendDependencies) {
				calls := 0
				dependencies.openDB = func(path string) (*sql.DB, error) {
					calls++
					if calls == 2 {
						return nil, testErr
					}
					return openDB(path)
				}
			},
		},
		{
			name: "create memory service",
			mutate: func(dependencies *backendDependencies) {
				dependencies.newMemory = func(*sql.DB) (memory.Service, error) { return nil, testErr }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dependencies := defaultBackendDependencies()
			test.mutate(&dependencies)
			if _, err := newBackend(root, dependencies).Open(context.Background(), "case"); !errors.Is(err, testErr) {
				t.Fatalf("Open() error = %v, want injected failure", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatalf("ReadDir() error = %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed construction left %d entries", len(entries))
			}
		})
	}
}
