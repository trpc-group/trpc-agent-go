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
// External backend factories are environment-gated placeholders:
// REDIS_ADDR, POSTGRES_DSN, MYSQL_DSN, and CLICKHOUSE_DSN declare that the
// corresponding integration backend should be configured by the caller. A
// factory that lacks configuration returns ErrBackendNotConfigured instead of
// silently participating in a run.
//
// Design note: replaytest drives every backend with the same typed ReplayCase
// sequence, then normalizes raw snapshots before comparison. Normalization
// replaces generated event IDs with stable replay keys, converts timestamps to
// UTC, canonicalizes JSON payloads, removes private state keys, and sorts memory
// topics and participants. Summary replay uses a deterministic fake summarizer
// so tests check persistence, filter-key ownership, overwrite behavior, and the
// asynchronous enqueue pipeline without calling an external model. Track replay
// compares normalized track names, payload JSON, timestamps, and event counts.
// Memory comparison is strict for identical retrieval profiles and sentinel
// based for different retrieval algorithms. AllowedDiff rules are explicit and
// path-scoped, supporting ignore, same-type, not-empty, and numeric delta
// matches. Harness reports keep passed, failed, skipped, and allowed
// differences separate, with a named reference backend by default.
package replaytest
