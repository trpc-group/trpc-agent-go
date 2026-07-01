//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"errors"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// ErrBackendNotConfigured indicates an optional backend factory lacks configuration.
var ErrBackendNotConfigured = errors.New("replaytest backend is not configured")

// BackendFactory creates session and memory services with their backend profile.
type BackendFactory func() (session.Service, memory.Service, BackendProfile, error)

// InMemoryFactory returns a factory for the built-in in-memory backends.
func InMemoryFactory() BackendFactory {
	return func() (session.Service, memory.Service, BackendProfile, error) {
		return sessioninmemory.NewSessionService(),
			memoryinmemory.NewMemoryService(),
			InMemoryProfile(),
			nil
	}
}

// RedisFactory returns a placeholder factory for Redis backends.
func RedisFactory() BackendFactory {
	return optionalBackendFactory("redis", "REDIS_ADDR")
}

// PostgresFactory returns a placeholder factory for PostgreSQL backends.
func PostgresFactory() BackendFactory {
	return optionalBackendFactory("postgres", "POSTGRES_DSN")
}

// MySQLFactory returns a placeholder factory for MySQL backends.
func MySQLFactory() BackendFactory {
	return optionalBackendFactory("mysql", "MYSQL_DSN")
}

// ClickHouseFactory returns a placeholder factory for ClickHouse backends.
func ClickHouseFactory() BackendFactory {
	return optionalBackendFactory("clickhouse", "CLICKHOUSE_DSN")
}

func optionalBackendFactory(name, envKey string) BackendFactory {
	return func() (session.Service, memory.Service, BackendProfile, error) {
		if os.Getenv(envKey) == "" {
			return nil, nil, BackendProfile{Name: name}, ErrBackendNotConfigured
		}
		return nil, nil, BackendProfile{Name: name}, ErrBackendNotConfigured
	}
}
