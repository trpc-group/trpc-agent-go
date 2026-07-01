//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides deterministic replay consistency tests for
// session, memory, summary, and track backends.
//
// Lightweight mode uses the built-in in-memory session and memory services:
//
//	factory := replaytest.InMemoryFactory()
//	sessionSvc, memorySvc, profile, err := factory()
//	if err != nil {
//		return err
//	}
//	defer sessionSvc.Close()
//	defer memorySvc.Close()
//
//	h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
//	h.AddBackend(replaytest.NamedBackend{
//		Name:           "inmemory",
//		Profile:        profile,
//		SessionService: sessionSvc,
//		MemoryService:  memorySvc,
//	})
//	report, err := h.Run(replaytest.AllCases())
//	if err != nil {
//		return err
//	}
//	return replaytest.NewReporter(os.Stdout).Write(report)
//
// Integration mode adds optional backend factories before running the same
// cases. SQLite lives in the separate
// trpc.group/trpc-go/trpc-agent-go/session/replaytest/sqlite module so the
// root module does not take a mandatory CGO dependency:
//
//	db, err := sql.Open("sqlite3", ":memory:")
//	if err != nil {
//		return err
//	}
//	sqliteFactory := replaytestsqlite.NewFactory(db)
//	sqliteSession, sqliteMemory, sqliteProfile, err := sqliteFactory()
//	if err != nil {
//		return err
//	}
//	h.AddBackend(replaytest.NamedBackend{
//		Name:           "sqlite",
//		Profile:        sqliteProfile,
//		SessionService: sqliteSession,
//		MemoryService:  sqliteMemory,
//	})
//
// External backend factories are not yet implemented. RedisFactory,
// PostgresFactory, MySQLFactory, and ClickHouseFactory are stub placeholders
// that currently return ErrBackendNotConfigured even when REDIS_ADDR,
// POSTGRES_DSN, MYSQL_DSN, or CLICKHOUSE_DSN is set. Real integration modules
// should create concrete services first, then pass them through NamedBackend.
//
// Design rationale: see DESIGN.md in this directory.
package replaytest
