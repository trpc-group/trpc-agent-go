//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sessionpostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

const (
	replayAppName   = "replay-app"
	replayUserID    = "replay-user"
	replayBaseUser  = "replay-user"
	replaySessionID = "replay-session"

	replaySessionPrefix = "replay_"
	replayMemoryTable   = "replay_memories"

	replayEnableRedis      = "REPLAY_ENABLE_REDIS"
	replayEnablePostgres   = "REPLAY_ENABLE_POSTGRES"
	replayEnableMySQL      = "REPLAY_ENABLE_MYSQL"
	replayEnableClickHouse = "REPLAY_ENABLE_CLICKHOUSE"
)

type replaySummarizer struct{}

func (replaySummarizer) ShouldSummarize(sess *session.Session) bool {
	return sess != nil && len(sess.Events) > 0
}

func (replaySummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", session.ErrNilSession
	}
	stateSize := 0
	if state := sess.SnapshotState(); len(state) > 0 {
		stateSize = len(state)
	}
	return fmt.Sprintf("replay-summary:%s:events=%d:state=%d", sess.ID, len(sess.Events), stateSize), nil
}

func (replaySummarizer) SetPrompt(string) {}

func (replaySummarizer) SetModel(model.Model) {}

func (replaySummarizer) Metadata() map[string]any {
	return map[string]any{"name": "replay_summarizer"}
}

type replayBackend struct {
	name         string
	kind         BackendKind
	sessionSvc   session.Service
	memorySvc    memory.Service
	trackSvc     session.TrackService
	cleanup      []func() error
	trackSupport bool
}

func (b *replayBackend) Name() string { return b.name }

func (b *replayBackend) Kind() BackendKind { return b.kind }

func (b *replayBackend) Supports(feature string) bool {
	switch strings.ToLower(strings.TrimSpace(feature)) {
	case "track":
		return b.trackSupport
	case "summary":
		return b.sessionSvc != nil
	case "memory":
		return b.memorySvc != nil
	default:
		return true
	}
}

func (b *replayBackend) Close() error {
	var errs []error
	if b.sessionSvc != nil {
		if err := b.sessionSvc.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if b.memorySvc != nil {
		if err := b.memorySvc.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(b.cleanup) - 1; i >= 0; i-- {
		if err := b.cleanup[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func newDefaultReplayBackends(opts HarnessOptions) ([]Backend, error) {
	backends := make([]Backend, 0, 6)

	inMemoryBackend, err := newInMemoryReplayBackend()
	if err != nil {
		return nil, err
	}
	backends = append(backends, inMemoryBackend)

	sqliteBackend, err := newSQLiteReplayBackend()
	if err != nil {
		return nil, err
	}
	backends = append(backends, sqliteBackend)

	if opts.LightMode || opts.SkipEnv {
		return backends, nil
	}

	if enabled(replayEnableRedis) {
		backend, err := newRedisReplayBackend()
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}
	if enabled(replayEnablePostgres) {
		backend, err := newPostgresReplayBackend()
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}
	if enabled(replayEnableMySQL) {
		backend, err := newMySQLReplayBackend()
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}
	if enabled(replayEnableClickHouse) {
		backend, err := newClickHouseReplayBackend()
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}

	return backends, nil
}

func newInMemoryReplayBackend() (Backend, error) {
	return &replayBackend{
		name: "inmemory",
		kind: BackendKindSession,
		sessionSvc: sessioninmemory.NewSessionService(
			sessioninmemory.WithSessionEventLimit(256),
			sessioninmemory.WithSummarizer(replaySummarizer{}),
			sessioninmemory.WithSummaryFilterAllowlist("branch-a", "branch-b"),
		),
		memorySvc:    memoryinmemory.NewMemoryService(),
		trackSupport: true,
	}, nil
}

func newSQLiteReplayBackend() (Backend, error) {
	sessionDB, sessionCleanup, err := openTempSQLiteDB("replay-session-*.db")
	if err != nil {
		return nil, err
	}
	memoryDB, memoryCleanup, err := openTempSQLiteDB("replay-memory-*.db")
	if err != nil {
		_ = sessionCleanup()
		return nil, err
	}

	sessionSvc, err := sessionsqlite.NewService(
		sessionDB,
		sessionsqlite.WithSessionEventLimit(256),
		sessionsqlite.WithSummarizer(replaySummarizer{}),
		sessionsqlite.WithSummaryFilterAllowlist("branch-a", "branch-b"),
	)
	if err != nil {
		_ = sessionCleanup()
		_ = memoryCleanup()
		return nil, err
	}
	memorySvc, err := memorysqlite.NewService(
		memoryDB,
		memorysqlite.WithTableName(replayMemoryTable),
	)
	if err != nil {
		_ = sessionSvc.Close()
		_ = sessionCleanup()
		_ = memoryCleanup()
		return nil, err
	}

	return &replayBackend{
		name:         "sqlite",
		kind:         BackendKindSession,
		sessionSvc:   sessionSvc,
		memorySvc:    memorySvc,
		trackSupport: true,
		cleanup:      []func() error{sessionCleanup, memoryCleanup},
	}, nil
}

func newRedisReplayBackend() (Backend, error) {
	addr := os.Getenv("REDIS_ADDR")
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("%s is enabled but REDIS_ADDR is empty", replayEnableRedis)
	}
	redisURL := "redis://" + addr

	sessionSvc, err := sessionredis.NewService(
		sessionredis.WithRedisClientURL(redisURL),
		sessionredis.WithSessionEventLimit(256),
		sessionredis.WithKeyPrefix("replay"),
		sessionredis.WithSummarizer(replaySummarizer{}),
		sessionredis.WithSummaryFilterAllowlist("branch-a", "branch-b"),
	)
	if err != nil {
		return nil, err
	}
	memorySvc, err := memoryredis.NewService(
		memoryredis.WithRedisClientURL(redisURL),
		memoryredis.WithKeyPrefix("replay"),
	)
	if err != nil {
		_ = sessionSvc.Close()
		return nil, err
	}

	return &replayBackend{
		name:         "redis",
		kind:         BackendKindSession,
		sessionSvc:   sessionSvc,
		memorySvc:    memorySvc,
		trackSupport: true,
	}, nil
}

func newPostgresReplayBackend() (Backend, error) {
	host := strings.TrimSpace(os.Getenv("PG_HOST"))
	if host == "" {
		return nil, fmt.Errorf("%s is enabled but PG_HOST is empty", replayEnablePostgres)
	}
	port := getEnvOrDefault("PG_PORT", "5432")
	user := getEnvOrDefault("PG_USER", "root")
	password := os.Getenv("PG_PASSWORD")
	database := getEnvOrDefault("PG_DATABASE", "trpc_agent_go")
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, database)

	sessionSvc, err := sessionpostgres.NewService(
		sessionpostgres.WithPostgresClientDSN(dsn),
		sessionpostgres.WithTablePrefix(replaySessionPrefix),
		sessionpostgres.WithSessionEventLimit(256),
		sessionpostgres.WithSummarizer(replaySummarizer{}),
		sessionpostgres.WithSummaryFilterAllowlist("branch-a", "branch-b"),
	)
	if err != nil {
		return nil, err
	}
	memorySvc, err := memorypostgres.NewService(
		memorypostgres.WithPostgresClientDSN(dsn),
		memorypostgres.WithTableName(replayMemoryTable),
	)
	if err != nil {
		_ = sessionSvc.Close()
		return nil, err
	}

	return &replayBackend{
		name:         "postgres",
		kind:         BackendKindSession,
		sessionSvc:   sessionSvc,
		memorySvc:    memorySvc,
		trackSupport: true,
	}, nil
}

func newMySQLReplayBackend() (Backend, error) {
	host := strings.TrimSpace(os.Getenv("MYSQL_HOST"))
	if host == "" {
		return nil, fmt.Errorf("%s is enabled but MYSQL_HOST is empty", replayEnableMySQL)
	}
	port := getEnvOrDefault("MYSQL_PORT", "3306")
	user := getEnvOrDefault("MYSQL_USER", "root")
	password := os.Getenv("MYSQL_PASSWORD")
	database := getEnvOrDefault("MYSQL_DATABASE", "trpc_agent_go")
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4", user, password, host, port, database)

	sessionSvc, err := sessionmysql.NewService(
		sessionmysql.WithMySQLClientDSN(dsn),
		sessionmysql.WithTablePrefix(replaySessionPrefix),
		sessionmysql.WithSessionEventLimit(256),
		sessionmysql.WithSummarizer(replaySummarizer{}),
		sessionmysql.WithSummaryFilterAllowlist("branch-a", "branch-b"),
	)
	if err != nil {
		return nil, err
	}
	memorySvc, err := memorymysql.NewService(
		memorymysql.WithMySQLClientDSN(dsn),
		memorymysql.WithTableName(replayMemoryTable),
	)
	if err != nil {
		_ = sessionSvc.Close()
		return nil, err
	}

	return &replayBackend{
		name:         "mysql",
		kind:         BackendKindSession,
		sessionSvc:   sessionSvc,
		memorySvc:    memorySvc,
		trackSupport: true,
	}, nil
}

func newClickHouseReplayBackend() (Backend, error) {
	host := strings.TrimSpace(os.Getenv("CLICKHOUSE_HOST"))
	if host == "" {
		return nil, fmt.Errorf("%s is enabled but CLICKHOUSE_HOST is empty", replayEnableClickHouse)
	}
	port := getEnvOrDefault("CLICKHOUSE_PORT", "9000")
	user := getEnvOrDefault("CLICKHOUSE_USER", "default")
	password := os.Getenv("CLICKHOUSE_PASSWORD")
	database := getEnvOrDefault("CLICKHOUSE_DATABASE", "trpc_agent_go")
	dsn := fmt.Sprintf("clickhouse://%s:%s@%s:%s/%s", user, password, host, port, database)

	sessionSvc, err := sessionclickhouse.NewService(
		sessionclickhouse.WithClickHouseDSN(dsn),
		sessionclickhouse.WithTablePrefix(replaySessionPrefix),
		sessionclickhouse.WithSessionEventLimit(256),
		sessionclickhouse.WithSummarizer(replaySummarizer{}),
		sessionclickhouse.WithSummaryFilterAllowlist("branch-a", "branch-b"),
	)
	if err != nil {
		return nil, err
	}
	memoryDB, memoryCleanup, err := openTempSQLiteDB("replay-clickhouse-memory-*.db")
	if err != nil {
		_ = sessionSvc.Close()
		return nil, err
	}
	memorySvc, err := memorysqlite.NewService(
		memoryDB,
		memorysqlite.WithTableName(replayMemoryTable),
	)
	if err != nil {
		_ = sessionSvc.Close()
		_ = memoryCleanup()
		return nil, err
	}

	return &replayBackend{
		name:         "clickhouse",
		kind:         BackendKindSession,
		sessionSvc:   sessionSvc,
		memorySvc:    memorySvc,
		trackSupport: false,
		cleanup:      []func() error{memoryCleanup},
	}, nil
}

func openTempSQLiteDB(pattern string) (*sql.DB, func() error, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, nil, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, nil, err
	}
	db, err := sql.Open("sqlite3", name)
	if err != nil {
		_ = os.Remove(name)
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	cleanup := func() error {
		var errs []error
		if err := db.Close(); err != nil {
			errs = append(errs, err)
		}
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	return db, cleanup, nil
}

func enabled(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	value = strings.ToLower(value)
	return value != "0" && value != "false" && value != "no"
}

func getEnvOrDefault(name, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return defaultValue
}
