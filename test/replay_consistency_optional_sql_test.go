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
	"fmt"
	"os"
	"testing"
	"time"

	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memmysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	mempostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	sessclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sesspostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestReplayConsistencyPostgresBackend(t *testing.T) {
	dsn := os.Getenv("TRPC_REPLAY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRPC_REPLAY_POSTGRES_DSN is not set")
	}
	runOptionalSQLReplay(t, "postgres", replayCasesWithUniqueApp("postgres"), newPostgresReplayBackend(dsn))
}

func TestReplayConsistencyMySQLBackend(t *testing.T) {
	dsn := os.Getenv("TRPC_REPLAY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TRPC_REPLAY_MYSQL_DSN is not set")
	}
	runOptionalSQLReplay(t, "mysql", replayCasesWithUniqueApp("mysql"), newMySQLReplayBackend(dsn))
}

func TestReplayConsistencyClickHouseSessionBackend(t *testing.T) {
	dsn := os.Getenv("TRPC_REPLAY_CLICKHOUSE_DSN")
	if dsn == "" {
		t.Skip("TRPC_REPLAY_CLICKHOUSE_DSN is not set")
	}
	runOptionalSQLReplay(t, "clickhouse", replayCasesWithUniqueApp("clickhouse"), newClickHouseSessionReplayBackend(dsn))
}

func runOptionalSQLReplay(t *testing.T, name string, cases []replaytest.ReplayCase, backend replaytest.Backend) {
	t.Helper()
	report, err := replaytest.Run(context.Background(), cases, []replaytest.Backend{
		replaytest.NewInMemoryBackend(),
		backend,
	})
	if err != nil {
		t.Fatalf("Run(%s) error = %v", name, err)
	}
	if replaytest.HasBlockingDiff(report) {
		data, _ := replaytest.MarshalReport(report)
		t.Fatalf("%s replay consistency diff:\n%s", name, data)
	}
}

func replayCasesWithUniqueApp(backend string) []replaytest.ReplayCase {
	cases := replaytest.PublicCases()
	appNamePrefix := fmt.Sprintf("replay-%s-%d", backend, time.Now().UnixNano())
	for i := range cases {
		cases[i].Key.AppName = fmt.Sprintf("%s-%02d", appNamePrefix, i)
	}
	return cases
}

func TestReplayCasesWithUniqueAppIsolatesMemoryScope(t *testing.T) {
	cases := replayCasesWithUniqueApp("unit")
	seen := make(map[string]string, len(cases))
	for _, c := range cases {
		scope := c.Key.AppName + "/" + c.Key.UserID
		if previous := seen[scope]; previous != "" {
			t.Fatalf("cases %q and %q share memory scope %q", previous, c.Name, scope)
		}
		seen[scope] = c.Name
	}
	if len(seen) != len(cases) {
		t.Fatalf("unique memory scopes = %d, want %d", len(seen), len(cases))
	}
}

func newPostgresReplayBackend(dsn string) replaytest.Backend {
	return replaytest.NewServiceBackend(
		"session/postgres+memory/postgres",
		func(_ context.Context, c replaytest.ReplayCase) (*replaytest.ServiceBundle, error) {
			sessionSvc, err := sesspostgres.NewService(
				sesspostgres.WithPostgresClientDSN(dsn),
				sesspostgres.WithSummarizer(replaytest.NewDeterministicSummarizer()),
				sesspostgres.WithEnableAsyncPersist(false),
			)
			if err != nil {
				return nil, fmt.Errorf("create postgres session service: %w", err)
			}
			memorySvc, err := mempostgres.NewService(
				mempostgres.WithPostgresClientDSN(dsn),
				mempostgres.WithMaxResults(100),
				mempostgres.WithMinSearchScore(0),
			)
			if err != nil {
				_ = sessionSvc.Close()
				return nil, fmt.Errorf("create postgres memory service: %w", err)
			}
			return &replaytest.ServiceBundle{
				SessionService: sessionSvc,
				MemoryService:  memorySvc,
				TrackService:   sessionSvc,
				TTLProbe: func(ctx context.Context) error {
					ttlSvc, err := sesspostgres.NewService(
						sesspostgres.WithPostgresClientDSN(dsn),
						sesspostgres.WithSessionTTL(100*time.Millisecond),
						sesspostgres.WithEnableAsyncPersist(false),
					)
					if err != nil {
						return err
					}
					defer ttlSvc.Close()
					key := c.Key
					key.SessionID = fmt.Sprintf("%s-ttl-probe-%d", key.SessionID, time.Now().UnixNano())
					return replaytest.ProbeSessionTTLExpiration(ctx, ttlSvc, key, 220*time.Millisecond)
				},
				Close: func() error {
					sessErr := sessionSvc.Close()
					memErr := memorySvc.Close()
					if sessErr != nil {
						return sessErr
					}
					return memErr
				},
			}, nil
		},
		replaytest.WithSupportedCapabilities(
			replaytest.CapabilityEventPage,
			replaytest.CapabilityMemorySearch,
			replaytest.CapabilityTTL,
			replaytest.CapabilityTrack,
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateDelete,
			"session.Service exposes merge-only UpdateSessionState and no session-state key delete API",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateClear,
			"session.Service exposes merge-only UpdateSessionState and no session-state clear API",
		),
	)
}

func newMySQLReplayBackend(dsn string) replaytest.Backend {
	return replaytest.NewServiceBackend(
		"session/mysql+memory/mysql",
		func(_ context.Context, c replaytest.ReplayCase) (*replaytest.ServiceBundle, error) {
			sessionSvc, err := sessmysql.NewService(
				sessmysql.WithMySQLClientDSN(dsn),
				sessmysql.WithSummarizer(replaytest.NewDeterministicSummarizer()),
				sessmysql.WithEnableAsyncPersist(false),
			)
			if err != nil {
				return nil, fmt.Errorf("create mysql session service: %w", err)
			}
			memorySvc, err := memmysql.NewService(
				memmysql.WithMySQLClientDSN(dsn),
				memmysql.WithMaxResults(100),
				memmysql.WithMinSearchScore(0),
			)
			if err != nil {
				_ = sessionSvc.Close()
				return nil, fmt.Errorf("create mysql memory service: %w", err)
			}
			return &replaytest.ServiceBundle{
				SessionService: sessionSvc,
				MemoryService:  memorySvc,
				TrackService:   sessionSvc,
				TTLProbe: func(ctx context.Context) error {
					ttlSvc, err := sessmysql.NewService(
						sessmysql.WithMySQLClientDSN(dsn),
						sessmysql.WithSessionTTL(100*time.Millisecond),
						sessmysql.WithEnableAsyncPersist(false),
					)
					if err != nil {
						return err
					}
					defer ttlSvc.Close()
					key := c.Key
					key.SessionID = fmt.Sprintf("%s-ttl-probe-%d", key.SessionID, time.Now().UnixNano())
					return replaytest.ProbeSessionTTLExpiration(ctx, ttlSvc, key, 220*time.Millisecond)
				},
				Close: func() error {
					sessErr := sessionSvc.Close()
					memErr := memorySvc.Close()
					if sessErr != nil {
						return sessErr
					}
					return memErr
				},
			}, nil
		},
		replaytest.WithSupportedCapabilities(
			replaytest.CapabilityEventPage,
			replaytest.CapabilityMemorySearch,
			replaytest.CapabilityTTL,
			replaytest.CapabilityTrack,
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateDelete,
			"session.Service exposes merge-only UpdateSessionState and no session-state key delete API",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateClear,
			"session.Service exposes merge-only UpdateSessionState and no session-state clear API",
		),
	)
}

func newClickHouseSessionReplayBackend(dsn string) replaytest.Backend {
	return replaytest.NewServiceBackend(
		"session/clickhouse+memory/inmemory",
		func(_ context.Context, c replaytest.ReplayCase) (*replaytest.ServiceBundle, error) {
			sessionSvc, err := sessclickhouse.NewService(
				sessclickhouse.WithClickHouseDSN(dsn),
				sessclickhouse.WithSummarizer(replaytest.NewDeterministicSummarizer()),
				sessclickhouse.WithEnableAsyncPersist(false),
			)
			if err != nil {
				return nil, fmt.Errorf("create clickhouse session service: %w", err)
			}
			memorySvc := meminmemory.NewMemoryService()
			return &replaytest.ServiceBundle{
				SessionService: sessionSvc,
				MemoryService:  memorySvc,
				TTLProbe: func(ctx context.Context) error {
					ttlSvc, err := sessclickhouse.NewService(
						sessclickhouse.WithClickHouseDSN(dsn),
						sessclickhouse.WithSessionTTL(100*time.Millisecond),
						sessclickhouse.WithEnableAsyncPersist(false),
					)
					if err != nil {
						return err
					}
					defer ttlSvc.Close()
					key := c.Key
					key.SessionID = fmt.Sprintf("%s-ttl-probe-%d", key.SessionID, time.Now().UnixNano())
					return replaytest.ProbeSessionTTLExpiration(ctx, ttlSvc, key, 220*time.Millisecond)
				},
				Close: func() error {
					sessErr := sessionSvc.Close()
					memErr := memorySvc.Close()
					if sessErr != nil {
						return sessErr
					}
					return memErr
				},
			}, nil
		},
		replaytest.WithSupportedCapabilities(
			replaytest.CapabilityMemorySearch,
			replaytest.CapabilityTTL,
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityEventPage,
			"session/clickhouse GetSession returns ErrEventPageUnsupported for strict event pages",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateDelete,
			"session.Service exposes merge-only UpdateSessionState and no session-state key delete API",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateClear,
			"session.Service exposes merge-only UpdateSessionState and no session-state clear API",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityTrack,
			"session/clickhouse does not expose session.TrackService in this repository",
		),
	)
}
