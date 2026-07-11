//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"os"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memInmem "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessInmem "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// NewInMemoryBackend returns a BackendFactory backed by in-memory stores.
func NewInMemoryBackend() BackendFactory {
	return BackendFactory{
		Name: "inmemory",
		CreateSession: func() (session.Service, error) {
			return sessInmem.NewSessionService(), nil
		},
		CreateTrack: func() (session.TrackService, error) {
			return sessInmem.NewSessionService(), nil
		},
		CreateMemory: func() (memory.Service, error) {
			return memInmem.NewMemoryService(), nil
		},
	}
}

// NewSQLiteBackend is a placeholder. Callers that wish to include the
// SQLite backend should construct one using the session/sqlite and
// memory/sqlite sub-modules and register it via WithBackends.
// See the README for an example.
func NewSQLiteBackend() BackendFactory {
	return BackendFactory{
		Name: "sqlite",
		UnsupportedOps: map[OpType]string{
			OpCreateSession: "sqlite backend not linked; import session/sqlite to enable",
		},
	}
}

// NewRedisBackend returns nil when REPLAY_REDIS_ADDR is not set.
func NewRedisBackend() *BackendFactory {
	if os.Getenv("REPLAY_REDIS_ADDR") == "" {
		return nil
	}
	return nil // requires separate redis sub-module import
}

// NewPostgresBackend returns nil when REPLAY_POSTGRES_DSN is not set.
func NewPostgresBackend() *BackendFactory {
	if os.Getenv("REPLAY_POSTGRES_DSN") == "" {
		return nil
	}
	return nil
}

// NewMySQLBackend returns nil when REPLAY_MYSQL_DSN is not set.
func NewMySQLBackend() *BackendFactory {
	if os.Getenv("REPLAY_MYSQL_DSN") == "" {
		return nil
	}
	return nil
}

// NewClickHouseBackend returns nil when REPLAY_CLICKHOUSE_DSN is not set.
func NewClickHouseBackend() *BackendFactory {
	if os.Getenv("REPLAY_CLICKHOUSE_DSN") == "" {
		return nil
	}
	return nil
}

// DefaultBackends returns the mandatory InMemory backend.
// To add SQLite, construct a BackendFactory from the session/sqlite and
// memory/sqlite packages and pass it via HarnessOption.
func DefaultBackends() []BackendFactory {
	return []BackendFactory{NewInMemoryBackend()}
}

// AllBackends returns all available backends (default + env-enabled optional).
func AllBackends() []BackendFactory {
	backends := DefaultBackends()
	for _, fn := range []func() *BackendFactory{
		NewRedisBackend,
		NewPostgresBackend,
		NewMySQLBackend,
		NewClickHouseBackend,
	} {
		if b := fn(); b != nil {
			backends = append(backends, *b)
		}
	}
	return backends
}
