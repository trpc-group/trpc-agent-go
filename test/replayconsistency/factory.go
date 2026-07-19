//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replayconsistency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sessionpostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

const (
	sqliteBusyTimeoutQuery      = "?_busy_timeout=5000"
	redisReplayEnvironment      = "TRPC_AGENT_GO_REPLAY_REDIS_DSN"
	postgresReplayEnvironment   = "TRPC_AGENT_GO_REPLAY_POSTGRES_DSN"
	mysqlReplayEnvironment      = "TRPC_AGENT_GO_REPLAY_MYSQL_DSN"
	clickHouseReplayEnvironment = "TRPC_AGENT_GO_REPLAY_CLICKHOUSE_DSN"
)

type backendFactory struct {
	Name        string
	Environment string
	New         func(BackendConfig) replaytest.Backend
}

type BackendConfig struct {
	Connection string
	SessionTTL time.Duration
}

type serviceCreator func(*replaySummarizer) (session.Service, memory.Service, error)

type serviceBackendOptions struct {
	Supported   []replaytest.Capability
	Unsupported []replaytest.Capability
}

func externalBackendFactories() []backendFactory {
	return []backendFactory{
		redisBackendFactory(),
		postgresBackendFactory(),
		mySQLBackendFactory(),
		clickHouseBackendFactory(),
	}
}

func validateBackendFactories(factories []backendFactory) error {
	names := make(map[string]struct{}, len(factories))
	environments := make(map[string]struct{}, len(factories))
	for i, factory := range factories {
		if factory.Name == "" || factory.Environment == "" || factory.New == nil {
			return fmt.Errorf("backend factory %d is invalid", i)
		}
		if _, exists := names[factory.Name]; exists {
			return fmt.Errorf("backend factory name %q is duplicated", factory.Name)
		}
		if _, exists := environments[factory.Environment]; exists {
			return fmt.Errorf("backend environment %q is duplicated", factory.Environment)
		}
		names[factory.Name] = struct{}{}
		environments[factory.Environment] = struct{}{}
	}
	return nil
}

func serviceBackend(
	name string,
	create serviceCreator,
	options serviceBackendOptions,
) replaytest.Backend {
	return replaytest.Backend{
		Name: name,
		New: func(context.Context, string) (replaytest.Fixture, error) {
			summarizer := &replaySummarizer{}
			sessionService, memoryService, err := create(summarizer)
			if err != nil {
				return nil, err
			}
			return newReplayFixture(replayFixtureConfig{
				name: name, sessionService: sessionService, memoryService: memoryService,
				summarizer: summarizer, supported: options.Supported,
				unsupported: options.Unsupported,
			}), nil
		},
	}
}

func newInMemoryBackend() replaytest.Backend {
	return newInMemoryBackendWithConfig(BackendConfig{})
}

func newInMemoryBackendWithConfig(config BackendConfig) replaytest.Backend {
	return replaytest.Backend{
		Name: "inmemory",
		New: func(context.Context, string) (replaytest.Fixture, error) {
			summarizer := &replaySummarizer{}
			options := []sessioninmemory.ServiceOpt{
				sessioninmemory.WithSummarizer(summarizer),
				sessioninmemory.WithSummaryFilterAllowlist(filterKeyMain),
			}
			if config.SessionTTL > 0 {
				options = append(options, sessioninmemory.WithSessionTTL(config.SessionTTL))
			}
			return newReplayFixture(replayFixtureConfig{
				name:           "inmemory",
				sessionService: sessioninmemory.NewSessionService(options...),
				memoryService:  memoryinmemory.NewMemoryService(),
				summarizer:     summarizer,
			}), nil
		},
	}
}

func newSQLiteBackend(directory string) replaytest.Backend {
	return newSQLiteBackendWithConfig(directory, BackendConfig{})
}

func newSQLiteBackendWithConfig(directory string, config BackendConfig) replaytest.Backend {
	var sequence atomic.Uint64
	return replaytest.Backend{
		Name: "sqlite",
		New: func(_ context.Context, caseName string) (replaytest.Fixture, error) {
			id := sequence.Add(1)
			prefix := fmt.Sprintf("%s-%d", sanitizeName(caseName), id)
			sessionDB, err := openSQLite(filepath.Join(directory, prefix+"-session.db"))
			if err != nil {
				return nil, err
			}
			memoryDB, err := openSQLite(filepath.Join(directory, prefix+"-memory.db"))
			if err != nil {
				return nil, errors.Join(err, sessionDB.Close())
			}
			return newSQLiteFixture(sessionDB, memoryDB, config)
		},
	}
}

func newSQLiteFixture(
	sessionDB *sql.DB,
	memoryDB *sql.DB,
	config BackendConfig,
) (replaytest.Fixture, error) {
	summarizer := &replaySummarizer{}
	options := []sessionsqlite.ServiceOpt{
		sessionsqlite.WithSummarizer(summarizer),
		sessionsqlite.WithSummaryFilterAllowlist(filterKeyMain),
	}
	if config.SessionTTL > 0 {
		options = append(options, sessionsqlite.WithSessionTTL(config.SessionTTL))
	}
	sessionService, err := sessionsqlite.NewService(sessionDB, options...)
	if err != nil {
		createErr := fmt.Errorf("create sqlite session service: %w", err)
		return nil, errors.Join(createErr, sessionDB.Close(), memoryDB.Close())
	}
	memoryService, err := memorysqlite.NewService(memoryDB)
	if err != nil {
		createErr := fmt.Errorf("create sqlite memory service: %w", err)
		return nil, errors.Join(createErr, sessionService.Close(), memoryDB.Close())
	}
	return newReplayFixture(replayFixtureConfig{
		name: "sqlite", sessionService: sessionService, memoryService: memoryService,
		summarizer: summarizer,
	}), nil
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+sqliteBusyTimeoutQuery)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := db.Ping(); err != nil {
		pingErr := fmt.Errorf("ping sqlite database: %w", err)
		return nil, errors.Join(pingErr, db.Close())
	}
	return db, nil
}

func sanitizeName(name string) string {
	return strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(name)
}

func redisBackendFactory() backendFactory {
	return backendFactory{
		Name: "redis", Environment: redisReplayEnvironment,
		New: func(config BackendConfig) replaytest.Backend { return newRedisBackend(config) },
	}
}

func newRedisBackend(config BackendConfig) replaytest.Backend {
	return serviceBackend("redis", func(summarizer *replaySummarizer) (
		session.Service,
		memory.Service,
		error,
	) {
		options := []sessionredis.ServiceOpt{
			sessionredis.WithRedisClientURL(config.Connection),
			sessionredis.WithSummarizer(summarizer),
			sessionredis.WithSummaryFilterAllowlist(filterKeyMain),
		}
		if config.SessionTTL > 0 {
			options = append(options, sessionredis.WithSessionTTL(config.SessionTTL))
		}
		sessionService, err := sessionredis.NewService(options...)
		if err != nil {
			return nil, nil, fmt.Errorf("create redis session service: %w", err)
		}
		memoryService, err := memoryredis.NewService(
			memoryredis.WithRedisClientURL(config.Connection),
		)
		if err != nil {
			createErr := fmt.Errorf("create redis memory service: %w", err)
			return nil, nil, errors.Join(createErr, sessionService.Close())
		}
		return sessionService, memoryService, nil
	}, serviceBackendOptions{})
}

func postgresBackendFactory() backendFactory {
	return backendFactory{
		Name: "postgres", Environment: postgresReplayEnvironment,
		New: func(config BackendConfig) replaytest.Backend { return newPostgresBackend(config) },
	}
}

func newPostgresBackend(config BackendConfig) replaytest.Backend {
	return serviceBackend("postgres", func(summarizer *replaySummarizer) (
		session.Service,
		memory.Service,
		error,
	) {
		options := []sessionpostgres.ServiceOpt{
			sessionpostgres.WithPostgresClientDSN(config.Connection),
			sessionpostgres.WithSummarizer(summarizer),
			sessionpostgres.WithSummaryFilterAllowlist(filterKeyMain),
		}
		if config.SessionTTL > 0 {
			options = append(options, sessionpostgres.WithSessionTTL(config.SessionTTL))
		}
		sessionService, err := sessionpostgres.NewService(options...)
		if err != nil {
			return nil, nil, fmt.Errorf("create postgres session service: %w", err)
		}
		memoryService, err := memorypostgres.NewService(
			memorypostgres.WithPostgresClientDSN(config.Connection),
		)
		if err != nil {
			createErr := fmt.Errorf("create postgres memory service: %w", err)
			return nil, nil, errors.Join(createErr, sessionService.Close())
		}
		return sessionService, memoryService, nil
	}, serviceBackendOptions{
		Supported: []replaytest.Capability{replaytest.CapabilityEventPaging},
	})
}

func mySQLBackendFactory() backendFactory {
	return backendFactory{
		Name: "mysql", Environment: mysqlReplayEnvironment,
		New: func(config BackendConfig) replaytest.Backend { return newMySQLBackend(config) },
	}
}

func newMySQLBackend(config BackendConfig) replaytest.Backend {
	return serviceBackend("mysql", func(summarizer *replaySummarizer) (
		session.Service,
		memory.Service,
		error,
	) {
		options := []sessionmysql.ServiceOpt{
			sessionmysql.WithMySQLClientDSN(config.Connection),
			sessionmysql.WithSummarizer(summarizer),
			sessionmysql.WithSummaryFilterAllowlist(filterKeyMain),
		}
		if config.SessionTTL > 0 {
			options = append(options, sessionmysql.WithSessionTTL(config.SessionTTL))
		}
		sessionService, err := sessionmysql.NewService(options...)
		if err != nil {
			return nil, nil, fmt.Errorf("create mysql session service: %w", err)
		}
		memoryService, err := memorymysql.NewService(
			memorymysql.WithMySQLClientDSN(config.Connection),
		)
		if err != nil {
			createErr := fmt.Errorf("create mysql memory service: %w", err)
			return nil, nil, errors.Join(createErr, sessionService.Close())
		}
		return sessionService, memoryService, nil
	}, serviceBackendOptions{
		Supported: []replaytest.Capability{replaytest.CapabilityEventPaging},
	})
}

func clickHouseBackendFactory() backendFactory {
	return backendFactory{
		Name: "clickhouse", Environment: clickHouseReplayEnvironment,
		New: func(config BackendConfig) replaytest.Backend { return newClickHouseBackend(config) },
	}
}

func newClickHouseBackend(config BackendConfig) replaytest.Backend {
	return serviceBackend("clickhouse", func(summarizer *replaySummarizer) (
		session.Service,
		memory.Service,
		error,
	) {
		options := []sessionclickhouse.ServiceOpt{
			sessionclickhouse.WithClickHouseDSN(config.Connection),
			sessionclickhouse.WithSummarizer(summarizer),
			sessionclickhouse.WithSummaryFilterAllowlist(filterKeyMain),
		}
		if config.SessionTTL > 0 {
			options = append(options, sessionclickhouse.WithSessionTTL(config.SessionTTL))
		}
		sessionService, err := sessionclickhouse.NewService(options...)
		if err != nil {
			return nil, nil, fmt.Errorf("create clickhouse session service: %w", err)
		}
		return sessionService, memoryinmemory.NewMemoryService(), nil
	}, serviceBackendOptions{
		Unsupported: []replaytest.Capability{
			replaytest.CapabilitySummary,
			replaytest.CapabilityTrack,
			replaytest.CapabilityTTL,
		},
	})
}
