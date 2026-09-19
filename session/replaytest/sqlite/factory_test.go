//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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
	openDB     func(context.Context, string) (*sql.DB, error)
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
		Capabilities: replaytest.PortableCapabilities(),
		Open: func(ctx context.Context, caseName string) (*replaytest.Services, error) {
			if root == "" {
				return nil, errors.New("replaytest sqlite: root is required")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			caseDir, err := os.MkdirTemp(root, sanitize(caseName)+"-")
			if err != nil {
				return nil, fmt.Errorf("create case directory: %w", err)
			}
			cleanup := func() error { return os.RemoveAll(caseDir) }

			sessionDB, err := dependencies.openDB(ctx, filepath.Join(caseDir, "session.db"))
			if err != nil {
				return nil, errors.Join(fmt.Errorf("open session database: %w", err), cleanup())
			}
			sessionService, err := dependencies.newSession(sessionDB)
			if err != nil {
				return nil, errors.Join(
					fmt.Errorf("create session service: %w", err),
					closeServiceOrDB(sessionService, sessionDB),
					cleanup(),
				)
			}
			if isNilService(sessionService) {
				return nil, errors.Join(
					errors.New("create session service: factory returned nil service"),
					sessionDB.Close(),
					cleanup(),
				)
			}

			memoryDB, err := dependencies.openDB(ctx, filepath.Join(caseDir, "memory.db"))
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
					closeServiceOrDB(memoryService, memoryDB),
					sessionService.Close(),
					cleanup(),
				)
			}
			if isNilService(memoryService) {
				return nil, errors.Join(
					errors.New("create memory service: factory returned nil service"),
					memoryDB.Close(),
					sessionService.Close(),
					cleanup(),
				)
			}
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(
					err,
					memoryService.Close(),
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

func closeServiceOrDB(service interface{ Close() error }, db *sql.DB) error {
	if !isNilService(service) {
		return service.Close()
	}
	return db.Close()
}

func isNilService(service any) bool {
	if service == nil {
		return true
	}
	value := reflect.ValueOf(service)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func openDB(ctx context.Context, path string) (*sql.DB, error) {
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
	if err := db.PingContext(ctx); err != nil {
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

func TestSQLiteBackendHonorsCanceledContext(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	services, err := sqliteBackend(root).Open(ctx, "canceled")
	if services != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() = (%v, %v), want (nil, context.Canceled)", services, err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("canceled Open() left %d entries, error = %v", len(entries), readErr)
	}
}

func TestSQLiteBackendCleansCancellationDuringFinalSetup(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	dependencies := defaultBackendDependencies()
	newMemory := dependencies.newMemory
	dependencies.newMemory = func(db *sql.DB) (memory.Service, error) {
		service, err := newMemory(db)
		cancel()
		return service, err
	}

	services, err := newBackend(root, dependencies).Open(ctx, "canceled-final-setup")
	if services != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() = (%v, %v), want (nil, context.Canceled)", services, err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("canceled Open() left %d entries, error = %v", len(entries), readErr)
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
	db, err := openDB(context.Background(), path)
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
	if db, err := openDB(context.Background(), missingParent); err == nil {
		_ = db.Close()
		t.Fatal("openDB() unexpectedly created a missing parent directory")
	}

	escapedParent := filepath.Join(t.TempDir(), "dsn # %")
	if err := os.MkdirAll(escapedParent, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	escapedPath := filepath.Join(escapedParent, "database # %.sqlite")
	escapedDB, err := openDB(context.Background(), escapedPath)
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

func TestOpenDBHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db, err := openDB(ctx, filepath.Join(t.TempDir(), "canceled.sqlite"))
	if db != nil {
		_ = db.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("openDB() error = %v, want context.Canceled", err)
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
				dependencies.openDB = func(context.Context, string) (*sql.DB, error) { return nil, testErr }
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
				dependencies.openDB = func(ctx context.Context, path string) (*sql.DB, error) {
					calls++
					if calls == 2 {
						return nil, testErr
					}
					return openDB(ctx, path)
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

func TestSQLiteBackendClosesServicesReturnedWithConstructionErrors(t *testing.T) {
	testErr := errors.New("injected construction failure")
	tests := []struct {
		name   string
		mutate func(*backendDependencies, *int)
	}{
		{
			name: "session service",
			mutate: func(dependencies *backendDependencies, closeCalls *int) {
				dependencies.newSession = func(db *sql.DB) (session.Service, error) {
					return &closeTrackingSessionService{close: func() error {
						*closeCalls++
						return db.Close()
					}}, testErr
				}
			},
		},
		{
			name: "memory service",
			mutate: func(dependencies *backendDependencies, closeCalls *int) {
				dependencies.newMemory = func(db *sql.DB) (memory.Service, error) {
					return &closeTrackingMemoryService{close: func() error {
						*closeCalls++
						return db.Close()
					}}, testErr
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dependencies := defaultBackendDependencies()
			closeCalls := 0
			test.mutate(&dependencies, &closeCalls)
			if _, err := newBackend(root, dependencies).Open(context.Background(), "case"); !errors.Is(err, testErr) {
				t.Fatalf("Open() error = %v, want injected failure", err)
			}
			if closeCalls != 1 {
				t.Fatalf("service Close() calls = %d, want 1", closeCalls)
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

type closeTrackingSessionService struct {
	session.Service
	close func() error
}

func (s *closeTrackingSessionService) Close() error { return s.close() }

type closeTrackingMemoryService struct {
	memory.Service
	close func() error
}

func (s *closeTrackingMemoryService) Close() error { return s.close() }

func TestSQLiteBackendRejectsNilServices(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*backendDependencies)
		want   string
	}{
		{
			name: "session",
			mutate: func(dependencies *backendDependencies) {
				dependencies.newSession = func(*sql.DB) (session.Service, error) { return nil, nil }
			},
			want: "session service: factory returned nil service",
		},
		{
			name: "memory",
			mutate: func(dependencies *backendDependencies) {
				dependencies.newMemory = func(*sql.DB) (memory.Service, error) { return nil, nil }
			},
			want: "memory service: factory returned nil service",
		},
		{
			name: "typed nil session",
			mutate: func(dependencies *backendDependencies) {
				var service *closeTrackingSessionService
				dependencies.newSession = func(*sql.DB) (session.Service, error) { return service, nil }
			},
			want: "session service: factory returned nil service",
		},
		{
			name: "typed nil memory",
			mutate: func(dependencies *backendDependencies) {
				var service *closeTrackingMemoryService
				dependencies.newMemory = func(*sql.DB) (memory.Service, error) { return service, nil }
			},
			want: "memory service: factory returned nil service",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dependencies := defaultBackendDependencies()
			test.mutate(&dependencies)
			_, err := newBackend(root, dependencies).Open(context.Background(), "case")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Open() error = %v, want %q", err, test.want)
			}
			entries, readErr := os.ReadDir(root)
			if readErr != nil {
				t.Fatalf("ReadDir() error = %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("nil service construction left %d entries", len(entries))
			}
		})
	}
}
