// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"errors"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// ErrBackendNotConfigured indicates an optional backend is not configured.
var ErrBackendNotConfigured = errors.New("replaytest backend is not configured")

// BackendFactory creates session and memory services with a capability profile.
type BackendFactory func() (session.Service, memory.Service, BackendProfile, error)

// InMemoryFactory returns a factory for built-in in-memory backends with a
// deterministic FakeSummarizer installed.
func InMemoryFactory() BackendFactory {
	return func() (session.Service, memory.Service, BackendProfile, error) {
		sess := sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(NewFakeSummarizer()),
		)
		mem := memoryinmemory.NewMemoryService()
		return sess, mem, InMemoryProfile(), nil
	}
}

// RedisEnvFactory is an env-gated placeholder for Redis integration.
// Set REPLAYTEST_REDIS_ADDR and wire a real adapter to enable it.
func RedisEnvFactory() BackendFactory {
	return envGatedFactory("redis", "REPLAYTEST_REDIS_ADDR")
}

// PostgresEnvFactory is an env-gated placeholder for PostgreSQL.
func PostgresEnvFactory() BackendFactory {
	return envGatedFactory("postgres", "REPLAYTEST_POSTGRES_DSN")
}

// MySQLEnvFactory is an env-gated placeholder for MySQL.
func MySQLEnvFactory() BackendFactory {
	return envGatedFactory("mysql", "REPLAYTEST_MYSQL_DSN")
}

// ClickHouseEnvFactory is an env-gated placeholder for ClickHouse.
func ClickHouseEnvFactory() BackendFactory {
	return envGatedFactory("clickhouse", "REPLAYTEST_CLICKHOUSE_DSN")
}

func envGatedFactory(name, envKey string) BackendFactory {
	return func() (session.Service, memory.Service, BackendProfile, error) {
		if os.Getenv(envKey) == "" {
			return nil, nil, BackendProfile{Name: name}, ErrBackendNotConfigured
		}
		return nil, nil, BackendProfile{Name: name}, fmt.Errorf(
			"%w: %s adapter is not wired in this package (use a dedicated module)",
			ErrBackendNotConfigured, name,
		)
	}
}
