//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sessionpostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	replayRedisEnv      = "TRPC_AGENT_REPLAY_REDIS_URL"
	replayPostgresEnv   = "TRPC_AGENT_REPLAY_POSTGRES_DSN"
	replayMySQLEnv      = "TRPC_AGENT_REPLAY_MYSQL_DSN"
	replayClickHouseEnv = "TRPC_AGENT_REPLAY_CLICKHOUSE_DSN"
)

var optionalReplaySequence atomic.Uint64

func TestReplayConsistencyOptionalBackends(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		factory func(string) replaytest.BackendFactory
	}{
		{name: "redis", env: replayRedisEnv, factory: redisReplayFactory},
		{name: "postgres", env: replayPostgresEnv, factory: postgresReplayFactory},
		{name: "mysql", env: replayMySQLEnv, factory: mysqlReplayFactory},
		{name: "clickhouse", env: replayClickHouseEnv, factory: clickHouseReplayFactory},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			connection := os.Getenv(test.env)
			if connection == "" {
				t.Skipf("%s is not set", test.env)
			}
			prefix := optionalReplayPrefix(test.name)
			factories := []replaytest.BackendFactory{
				lightweightReplayFactories(t.TempDir())[0],
				test.factory(connection + "\x00" + prefix),
			}
			started := time.Now()
			report, err := replaytest.RunSuite(
				context.Background(),
				replaytest.PublicCases(),
				factories,
			)
			require.NoError(t, err)
			require.Truef(t, report.Healthy(),
				"%s replay differences: %+v", test.name, report.Cases)
			require.Less(t, time.Since(started), 2*time.Minute)
		})
	}
}

func redisReplayFactory(config string) replaytest.BackendFactory {
	connection, prefix := splitReplayConfig(config)
	capabilities := replayCapabilities()
	capabilities[replaytest.CapabilityEventStateDeltaNull] =
		replaytest.Capability{
			AllowedDiff: true,
			Reason:      "Redis hash-index persistence treats nil event stateDelta values as no-ops",
		}
	return replaytest.BackendFactory{
		Name: "redis", Capabilities: capabilities.Clone(),
		Create: func(
			_ context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			service, err := sessionredis.NewService(
				sessionredis.WithRedisClientURL(connection),
				sessionredis.WithKeyPrefix(prefix),
				sessionredis.WithSessionTTL(0),
				sessionredis.WithSummarizer(&deterministicReplaySummarizer{}),
				sessionredis.WithSummaryFilterAllowlist(replaySummaryFilterKey),
				sessionredis.WithCascadeFullSessionSummary(false),
			)
			if err != nil {
				return replaytest.Backend{}, nil, err
			}
			if err := probeReplaySessionService(service); err != nil {
				service.Close()
				return replaytest.Backend{}, nil, err
			}
			return backendWithInMemoryMemory(
				"redis",
				service,
				service,
				replaySessionKey(caseName),
				capabilities,
			)
		},
	}
}

func postgresReplayFactory(config string) replaytest.BackendFactory {
	connection, prefix := splitReplayConfig(config)
	capabilities := replayCapabilities()
	return replaytest.BackendFactory{
		Name: "postgres", Capabilities: capabilities.Clone(),
		Create: func(
			_ context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			service, err := sessionpostgres.NewService(
				sessionpostgres.WithPostgresClientDSN(connection),
				sessionpostgres.WithTablePrefix(prefix),
				sessionpostgres.WithSessionTTL(0),
				sessionpostgres.WithSummarizer(&deterministicReplaySummarizer{}),
				sessionpostgres.WithSummaryFilterAllowlist(replaySummaryFilterKey),
				sessionpostgres.WithCascadeFullSessionSummary(false),
			)
			if err != nil {
				return replaytest.Backend{}, nil, err
			}
			if err := probeReplaySessionService(service); err != nil {
				service.Close()
				return replaytest.Backend{}, nil, err
			}
			return backendWithInMemoryMemory(
				"postgres",
				service,
				service,
				replaySessionKey(caseName),
				capabilities,
			)
		},
	}
}

func mysqlReplayFactory(config string) replaytest.BackendFactory {
	connection, prefix := splitReplayConfig(config)
	capabilities := replayCapabilities()
	return replaytest.BackendFactory{
		Name: "mysql", Capabilities: capabilities.Clone(),
		Create: func(
			_ context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			service, err := sessionmysql.NewService(
				sessionmysql.WithMySQLClientDSN(connection),
				sessionmysql.WithTablePrefix(prefix),
				sessionmysql.WithSessionTTL(0),
				sessionmysql.WithSummarizer(&deterministicReplaySummarizer{}),
				sessionmysql.WithSummaryFilterAllowlist(replaySummaryFilterKey),
				sessionmysql.WithCascadeFullSessionSummary(false),
			)
			if err != nil {
				return replaytest.Backend{}, nil, err
			}
			if err := probeReplaySessionService(service); err != nil {
				service.Close()
				return replaytest.Backend{}, nil, err
			}
			return backendWithInMemoryMemory(
				"mysql",
				service,
				service,
				replaySessionKey(caseName),
				capabilities,
			)
		},
	}
}

func clickHouseReplayFactory(config string) replaytest.BackendFactory {
	connection, prefix := splitReplayConfig(config)
	capabilities := replayCapabilities()
	capabilities[replaytest.CapabilityTracks] = replaytest.Capability{
		AllowedDiff: true,
		Reason:      "the ClickHouse Session service does not implement session.TrackService",
	}
	return replaytest.BackendFactory{
		Name: "clickhouse", Capabilities: capabilities.Clone(),
		Create: func(
			_ context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			service, err := sessionclickhouse.NewService(
				sessionclickhouse.WithClickHouseDSN(connection),
				sessionclickhouse.WithTablePrefix(prefix),
				sessionclickhouse.WithSessionTTL(0),
				sessionclickhouse.WithSummarizer(&deterministicReplaySummarizer{}),
				sessionclickhouse.WithSummaryFilterAllowlist(replaySummaryFilterKey),
				sessionclickhouse.WithCascadeFullSessionSummary(false),
			)
			if err != nil {
				return replaytest.Backend{}, nil, err
			}
			if err := probeReplaySessionService(service); err != nil {
				service.Close()
				return replaytest.Backend{}, nil, err
			}
			return backendWithInMemoryMemory(
				"clickhouse",
				service,
				nil,
				replaySessionKey(caseName),
				capabilities,
			)
		},
	}
}

func backendWithInMemoryMemory(
	name string,
	sessionService session.Service,
	trackService session.TrackService,
	key session.Key,
	capabilities replaytest.CapabilitySet,
) (replaytest.Backend, func() error, error) {
	memoryService := memoryinmemory.NewMemoryService(
		memoryinmemory.WithMinSearchScore(0),
		memoryinmemory.WithMaxResults(0),
	)
	backend := replaytest.Backend{
		Name: name, Session: sessionService, Memory: memoryService,
		Track: trackService, SessionKey: key,
		Capabilities: capabilities.Clone(),
	}
	cleanup := func() error {
		return errors.Join(memoryService.Close(), sessionService.Close())
	}
	return backend, cleanup, nil
}

func probeReplaySessionService(service session.Service) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := service.ListAppStates(ctx, "__trpc_replay_probe__")
	if err != nil {
		return fmt.Errorf("replay backend probe: %w", err)
	}
	return nil
}

func optionalReplayPrefix(backend string) string {
	sequence := optionalReplaySequence.Add(1)
	return fmt.Sprintf(
		"replay_%s_%s_%s",
		replaySafeName(backend),
		strconv.FormatInt(time.Now().UnixNano(), 36),
		strconv.FormatUint(sequence, 36),
	)
}

func splitReplayConfig(config string) (string, string) {
	for i := range config {
		if config[i] == 0 {
			return config[:i], config[i+1:]
		}
	}
	return config, optionalReplayPrefix("backend")
}
