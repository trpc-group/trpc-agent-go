//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	smysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	spostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	ssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	ssummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// fakeSummarizer implements summary.SessionSummarizer for deterministic testing.
type fakeSummarizer struct{}

func (f *fakeSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (f *fakeSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", nil
	}
	return fmt.Sprintf("summary-of-%d-events", len(sess.Events)), nil
}

func (f *fakeSummarizer) SetPrompt(string)         {}
func (f *fakeSummarizer) SetModel(model.Model)     {}
func (f *fakeSummarizer) Metadata() map[string]any { return nil }

var _ ssummary.SessionSummarizer = (*fakeSummarizer)(nil)

// defaultSessKey returns the default session key for replay test backends.
func defaultSessKey() session.Key {
	return session.Key{AppName: "replay-test", UserID: "user", SessionID: "session"}
}

// defaultWarmUp is the standard warm-up function for external backends.
// It creates, reads, and deletes a session to verify basic connectivity.
func defaultWarmUp(ctx context.Context, b Backend) error {
	key := b.SessKey()
	sess, err := b.Sess.CreateSession(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("warmup create: %w", err)
	}
	if _, err := b.Sess.GetSession(ctx, key); err != nil {
		return fmt.Errorf("warmup get: %w", err)
	}
	if err := b.Sess.DeleteSession(ctx, key); err != nil {
		return fmt.Errorf("warmup delete: %w", err)
	}
	_ = sess
	return nil
}

// inMemoryFactory creates an InMemory session backend.
type inMemoryFactory struct{}

func (inMemoryFactory) Kind() string               { return "inmemory" }
func (inMemoryFactory) Capabilities() Capabilities { return AllCapabilities() }
func (inMemoryFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	svc := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(&fakeSummarizer{}),
	)
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory session service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "inmemory",
		Sess:    svc,
		Track:   svc,
		Mem:     memSvc,
		Caps:    AllCapabilities(),
		SessKey: defaultSessKey,
	}
}

// sqliteFactory creates an in-memory SQLite session backend.
type sqliteFactory struct{}

func (sqliteFactory) Kind() string               { return "sqlite" }
func (sqliteFactory) Capabilities() Capabilities { return AllCapabilities() }
func (sqliteFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("warning: closing sqlite db: %v", closeErr)
		}
	})
	svc, err := ssqlite.NewService(db,
		ssqlite.WithSummarizer(&fakeSummarizer{}),
	)
	if err != nil {
		t.Fatalf("create sqlite service: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing sqlite service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "sqlite",
		Sess:    svc,
		Track:   svc,
		Mem:     memSvc,
		Caps:    AllCapabilities(),
		SessKey: defaultSessKey,
	}
}

// miniredisFactory creates a Redis session backend backed by an in-process miniredis server.
// Always available — no external Redis instance needed.
type miniredisFactory struct{}

func (miniredisFactory) Kind() string               { return "miniredis" }
func (miniredisFactory) Capabilities() Capabilities { return AllCapabilities() }
func (miniredisFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(func() { mr.Close() })

	svc, err := sredis.NewService(
		sredis.WithRedisClientURL("redis://"+mr.Addr()),
		sredis.WithSummarizer(&fakeSummarizer{}),
	)
	if err != nil {
		t.Fatalf("create redis session service: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing miniredis session service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "miniredis",
		Sess:    svc,
		Track:   svc,
		Mem:     memSvc,
		Caps:    AllCapabilities(),
		SessKey: defaultSessKey,
		Probe: func(ctx context.Context) error {
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			defer client.Close()
			return client.Ping(ctx).Err()
		},
	}
}

// redisFactory creates a Redis session backend gated by TRPC_AGENT_REPLAY_REDIS_URL.
type redisFactory struct{}

func (redisFactory) Kind() string               { return "redis" }
func (redisFactory) Capabilities() Capabilities { return AllCapabilities() }
func (redisFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	url := os.Getenv("TRPC_AGENT_REPLAY_REDIS_URL")
	if url == "" {
		t.Skip("TRPC_AGENT_REPLAY_REDIS_URL not set, skipping Redis backend")
	}
	svc, err := sredis.NewService(
		sredis.WithRedisClientURL(url),
		sredis.WithSummarizer(&fakeSummarizer{}),
	)
	if err != nil {
		t.Fatalf("create redis session service: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing redis session service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "redis",
		Sess:    svc,
		Track:   svc,
		Mem:     memSvc,
		Caps:    AllCapabilities(),
		SessKey: defaultSessKey,
		Probe: func(ctx context.Context) error {
			opts, err := redis.ParseURL(url)
			if err != nil {
				return fmt.Errorf("parse redis URL: %w", err)
			}
			client := redis.NewClient(opts)
			defer client.Close()
			return client.Ping(ctx).Err()
		},
		WarmUp: defaultWarmUp,
	}
}

// postgresFactory creates a Postgres session backend gated by TRPC_AGENT_REPLAY_POSTGRES_DSN.
type postgresFactory struct{}

func (postgresFactory) Kind() string { return "postgres" }
func (postgresFactory) Capabilities() Capabilities {
	return Capabilities{
		CapEvents:              {Supported: true},
		CapState:               {Supported: true},
		CapMemory:              {Supported: true},
		CapSummary:             {Supported: true},
		CapTrack:               {Supported: true},
		CapEventStateDeltaNull: {Supported: true},
	}
}
func (postgresFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	dsn := os.Getenv("TRPC_AGENT_REPLAY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRPC_AGENT_REPLAY_POSTGRES_DSN not set, skipping Postgres backend")
	}
	svc, err := spostgres.NewService(
		spostgres.WithPostgresClientDSN(dsn),
		spostgres.WithSummarizer(&fakeSummarizer{}),
	)
	if err != nil {
		t.Fatalf("create postgres session service: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing postgres session service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "postgres",
		Sess:    svc,
		Track:   svc,
		Mem:     memSvc,
		Caps:    AllCapabilities(),
		SessKey: defaultSessKey,
		Probe: func(ctx context.Context) error {
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return db.PingContext(pingCtx)
		},
		WarmUp: defaultWarmUp,
	}
}

// mysqlFactory creates a MySQL session backend gated by TRPC_AGENT_REPLAY_MYSQL_DSN.
type mysqlFactory struct{}

func (mysqlFactory) Kind() string { return "mysql" }
func (mysqlFactory) Capabilities() Capabilities {
	return Capabilities{
		CapEvents:              {Supported: true},
		CapState:               {Supported: true},
		CapMemory:              {Supported: true},
		CapSummary:             {Supported: true},
		CapTrack:               {Supported: true},
		CapEventStateDeltaNull: {Supported: true},
	}
}
func (mysqlFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	dsn := os.Getenv("TRPC_AGENT_REPLAY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TRPC_AGENT_REPLAY_MYSQL_DSN not set, skipping MySQL backend")
	}
	svc, err := smysql.NewService(
		smysql.WithMySQLClientDSN(dsn),
		smysql.WithSummarizer(&fakeSummarizer{}),
	)
	if err != nil {
		t.Fatalf("create mysql session service: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing mysql session service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "mysql",
		Sess:    svc,
		Track:   svc,
		Mem:     memSvc,
		Caps:    AllCapabilities(),
		SessKey: defaultSessKey,
		Probe: func(ctx context.Context) error {
			db, err := sql.Open("mysql", dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return db.PingContext(pingCtx)
		},
		WarmUp: defaultWarmUp,
	}
}

// clickhouseFactory creates a ClickHouse session backend gated by TRPC_AGENT_REPLAY_CLICKHOUSE_DSN.
// ClickHouse does NOT implement session.TrackService, so CapTrack is unsupported.
type clickhouseFactory struct{}

func (clickhouseFactory) Kind() string { return "clickhouse" }
func (clickhouseFactory) Capabilities() Capabilities {
	return Capabilities{
		CapEvents:              {Supported: true},
		CapState:               {Supported: true},
		CapMemory:              {Supported: true},
		CapSummary:             {Supported: true},
		CapTrack:               {Supported: false, Reason: "ClickHouse does not implement TrackService"},
		CapEventStateDeltaNull: {Supported: true},
	}
}
func (clickhouseFactory) Create(_ context.Context, t *testing.T) *Backend {
	t.Helper()
	dsn := os.Getenv("TRPC_AGENT_REPLAY_CLICKHOUSE_DSN")
	if dsn == "" {
		t.Skip("TRPC_AGENT_REPLAY_CLICKHOUSE_DSN not set, skipping ClickHouse backend")
	}
	svc, err := sclickhouse.NewService(
		sclickhouse.WithClickHouseDSN(dsn),
		sclickhouse.WithSummarizer(&fakeSummarizer{}),
	)
	if err != nil {
		t.Fatalf("create clickhouse session service: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := svc.Close(); closeErr != nil {
			t.Logf("warning: closing clickhouse session service: %v", closeErr)
		}
	})
	memSvc := inmemory.NewMemoryService()
	t.Cleanup(func() {
		if closeErr := memSvc.Close(); closeErr != nil {
			t.Logf("warning: closing inmemory memory service: %v", closeErr)
		}
	})
	return &Backend{
		Name:    "clickhouse",
		Sess:    svc,
		Track:   nil,
		Mem:     memSvc,
		Caps:    clickhouseFactory{}.Capabilities(),
		SessKey: defaultSessKey,
		Probe: func(ctx context.Context) error {
			db, err := sql.Open("clickhouse", dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return db.PingContext(pingCtx)
		},
		WarmUp: defaultWarmUp,
	}
}

// ResolveBackends returns the list of backend factories to test.
// Always includes InMemory and SQLite. Adds miniredis, and external
// backends if their environment variables are set.
func ResolveBackends(t *testing.T) []BackendFactory {
	t.Helper()
	factories := []BackendFactory{
		inMemoryFactory{},
		sqliteFactory{},
		miniredisFactory{},
	}
	// Add external backends gated by environment variables.
	if os.Getenv("TRPC_AGENT_REPLAY_REDIS_URL") != "" {
		factories = append(factories, redisFactory{})
	}
	if os.Getenv("TRPC_AGENT_REPLAY_POSTGRES_DSN") != "" {
		factories = append(factories, postgresFactory{})
	}
	if os.Getenv("TRPC_AGENT_REPLAY_MYSQL_DSN") != "" {
		factories = append(factories, mysqlFactory{})
	}
	if os.Getenv("TRPC_AGENT_REPLAY_CLICKHOUSE_DSN") != "" {
		factories = append(factories, clickhouseFactory{})
	}
	fmt.Fprintf(os.Stderr, "replaytest: backends=%v\n", backendNames(factories))
	return factories
}

// ResolvePair returns the primary (always InMemory) and target backend factories.
func ResolvePair(t *testing.T) (primary, target BackendFactory) {
	t.Helper()
	primary = inMemoryFactory{}
	backend := os.Getenv("REPLAY_BACKEND")
	switch strings.ToLower(backend) {
	case "", "sqlite":
		target = sqliteFactory{}
	case "inmemory":
		target = inMemoryFactory{}
	case "miniredis":
		target = miniredisFactory{}
	case "redis":
		target = redisFactory{}
	case "postgres":
		target = postgresFactory{}
	case "mysql":
		target = mysqlFactory{}
	case "clickhouse":
		target = clickhouseFactory{}
	default:
		t.Skipf("unsupported REPLAY_BACKEND=%q; supported: inmemory, sqlite, miniredis, redis, postgres, mysql, clickhouse", backend)
	}
	fmt.Fprintf(os.Stderr, "replaytest: primary=%s target=%s\n", primary.Kind(), target.Kind())
	return primary, target
}

func backendNames(factories []BackendFactory) []string {
	names := make([]string, len(factories))
	for i, f := range factories {
		names[i] = f.Kind()
	}
	return names
}
