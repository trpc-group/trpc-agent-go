//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"fmt"
	"os"
	"strings"

	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	sessionclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sessionpostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	// EnvRedisURL enables Redis Session and Memory replay.
	EnvRedisURL = "REPLAY_REDIS_URL"
	// EnvPostgresDSN enables Postgres Session and Memory replay.
	EnvPostgresDSN = "REPLAY_POSTGRES_DSN"
	// EnvMySQLDSN enables MySQL Session and Memory replay.
	EnvMySQLDSN = "REPLAY_MYSQL_DSN"
	// EnvClickHouseDSN enables ClickHouse Session replay.
	EnvClickHouseDSN = "REPLAY_CLICKHOUSE_DSN"
)

// IntegrationFactoriesFromEnvironment returns Redis, Postgres, MySQL, and
// ClickHouse factories. Missing environment variables produce disabled
// factories so integration reports document unsupported configurations.
func IntegrationFactoriesFromEnvironment() []replaytest.BackendFactory {
	return []replaytest.BackendFactory{
		redisFactory(strings.TrimSpace(os.Getenv(EnvRedisURL))),
		postgresFactory(strings.TrimSpace(os.Getenv(EnvPostgresDSN))),
		mySQLFactory(strings.TrimSpace(os.Getenv(EnvMySQLDSN))),
		clickHouseFactory(strings.TrimSpace(os.Getenv(EnvClickHouseDSN))),
	}
}

// AllFactoriesFromEnvironment combines lightweight and optional integration
// factories.
func AllFactoriesFromEnvironment() []replaytest.BackendFactory {
	factories := LightweightFactories()
	return append(factories, IntegrationFactoriesFromEnvironment()...)
}

func redisFactory(url string) replaytest.BackendFactory {
	capabilities := replaytest.CoreCapabilities()
	capabilities.TTL = true
	factory := replaytest.BackendFactory{
		Name:         "redis",
		Capabilities: capabilities,
	}
	if url == "" {
		factory.DisabledReason = EnvRedisURL + " is not set"
		return factory
	}
	factory.Open = func(
		ctx context.Context,
		replayCase replaytest.ReplayCase,
	) (*replaytest.Backend, error) {
		eventLimit := replayEventLimit(replayCase)
		sessionService, err := sessionredis.NewService(
			sessionredis.WithRedisClientURL(url),
			sessionredis.WithKeyPrefix("trpc-replay"),
			sessionredis.WithSessionEventLimit(eventLimit),
			sessionredis.WithSummarizer(
				replaytest.NewTranscriptSummarizer(),
			),
			sessionredis.WithSummaryFilterAllowlist(
				replaytest.SummaryFilterKeys(replayCase)...,
			),
			sessionredis.WithCascadeFullSessionSummary(false),
		)
		if err != nil {
			return nil, fmt.Errorf("create redis session service: %w", err)
		}
		memoryService, err := memoryredis.NewService(
			memoryredis.WithRedisClientURL(url),
			memoryredis.WithKeyPrefix("trpc-replay"),
			memoryredis.WithMinSearchScore(0),
			memoryredis.WithMaxResults(100),
		)
		if err != nil {
			_ = sessionService.Close()
			return nil, fmt.Errorf("create redis memory service: %w", err)
		}
		return &replaytest.Backend{
			Name:         "redis",
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
	}
	return factory
}

func postgresFactory(dsn string) replaytest.BackendFactory {
	capabilities := replaytest.CoreCapabilities()
	capabilities.EventPaging = true
	capabilities.TTL = true
	factory := replaytest.BackendFactory{
		Name:         "postgres",
		Capabilities: capabilities,
	}
	if dsn == "" {
		factory.DisabledReason = EnvPostgresDSN + " is not set"
		return factory
	}
	factory.Open = func(
		ctx context.Context,
		replayCase replaytest.ReplayCase,
	) (*replaytest.Backend, error) {
		sessionService, err := sessionpostgres.NewService(
			sessionpostgres.WithPostgresClientDSN(dsn),
			sessionpostgres.WithTablePrefix("trpc_replay"),
			sessionpostgres.WithSessionEventLimit(
				replayEventLimit(replayCase),
			),
			sessionpostgres.WithSummarizer(
				replaytest.NewTranscriptSummarizer(),
			),
			sessionpostgres.WithSummaryFilterAllowlist(
				replaytest.SummaryFilterKeys(replayCase)...,
			),
			sessionpostgres.WithCascadeFullSessionSummary(false),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create postgres session service: %w",
				err,
			)
		}
		memoryService, err := memorypostgres.NewService(
			memorypostgres.WithPostgresClientDSN(dsn),
			memorypostgres.WithTableName("trpc_replay_memories"),
			memorypostgres.WithMinSearchScore(0),
			memorypostgres.WithMaxResults(100),
		)
		if err != nil {
			_ = sessionService.Close()
			return nil, fmt.Errorf(
				"create postgres memory service: %w",
				err,
			)
		}
		return &replaytest.Backend{
			Name:         "postgres",
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
	}
	return factory
}

func mySQLFactory(dsn string) replaytest.BackendFactory {
	capabilities := replaytest.CoreCapabilities()
	capabilities.EventPaging = true
	capabilities.TTL = true
	factory := replaytest.BackendFactory{
		Name:         "mysql",
		Capabilities: capabilities,
	}
	if dsn == "" {
		factory.DisabledReason = EnvMySQLDSN + " is not set"
		return factory
	}
	factory.Open = func(
		ctx context.Context,
		replayCase replaytest.ReplayCase,
	) (*replaytest.Backend, error) {
		sessionService, err := sessionmysql.NewService(
			sessionmysql.WithMySQLClientDSN(dsn),
			sessionmysql.WithTablePrefix("trpc_replay"),
			sessionmysql.WithSessionEventLimit(
				replayEventLimit(replayCase),
			),
			sessionmysql.WithSummarizer(
				replaytest.NewTranscriptSummarizer(),
			),
			sessionmysql.WithSummaryFilterAllowlist(
				replaytest.SummaryFilterKeys(replayCase)...,
			),
			sessionmysql.WithCascadeFullSessionSummary(false),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create mysql session service: %w",
				err,
			)
		}
		memoryService, err := memorymysql.NewService(
			memorymysql.WithMySQLClientDSN(dsn),
			memorymysql.WithTableName("trpc_replay_memories"),
			memorymysql.WithMinSearchScore(0),
			memorymysql.WithMaxResults(100),
		)
		if err != nil {
			_ = sessionService.Close()
			return nil, fmt.Errorf(
				"create mysql memory service: %w",
				err,
			)
		}
		return &replaytest.Backend{
			Name:         "mysql",
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
	}
	return factory
}

func clickHouseFactory(dsn string) replaytest.BackendFactory {
	capabilities := replaytest.Capabilities{
		Events: true,
		State:  true,
		TTL:    true,
	}
	factory := replaytest.BackendFactory{
		Name:         "clickhouse",
		Capabilities: capabilities,
	}
	if dsn == "" {
		factory.DisabledReason = EnvClickHouseDSN + " is not set"
		return factory
	}
	factory.Open = func(
		ctx context.Context,
		replayCase replaytest.ReplayCase,
	) (*replaytest.Backend, error) {
		sessionService, err := sessionclickhouse.NewService(
			sessionclickhouse.WithClickHouseDSN(dsn),
			sessionclickhouse.WithTablePrefix("trpc_replay"),
			sessionclickhouse.WithSessionEventLimit(
				replayEventLimit(replayCase),
			),
			sessionclickhouse.WithSummarizer(
				replaytest.NewTranscriptSummarizer(),
			),
			sessionclickhouse.WithSummaryFilterAllowlist(
				replaytest.SummaryFilterKeys(replayCase)...,
			),
			sessionclickhouse.WithCascadeFullSessionSummary(false),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create clickhouse session service: %w",
				err,
			)
		}
		return &replaytest.Backend{
			Name:         "clickhouse",
			Session:      sessionService,
			Capabilities: capabilities,
			Close:        sessionService.Close,
		}, nil
	}
	return factory
}

func replayEventLimit(replayCase replaytest.ReplayCase) int {
	if replayCase.EventLimit > 0 {
		return replayCase.EventLimit
	}
	return 1000
}
