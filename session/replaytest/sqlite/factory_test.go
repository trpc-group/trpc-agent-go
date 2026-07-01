//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

func TestFactoryRunsReplayAgainstInMemory(t *testing.T) {
	for _, tc := range crossBackendReplayCases() {
		t.Run(tc.Name, func(t *testing.T) {
			report := runCrossBackendCase(t, tc)
			if report.TotalCases != 1 {
				t.Fatalf("total cases = %d, want 1", report.TotalCases)
			}
			if report.PassedCases != 1 {
				t.Fatalf("passed cases = %d, want 1: %#v", report.PassedCases, report.Results)
			}
			if report.FailedCases != 0 {
				t.Fatalf("failed cases = %d, want 0: %#v", report.FailedCases, report.Results)
			}
			if report.SkippedCases != 0 {
				t.Fatalf("skipped cases = %d, want 0: %#v", report.SkippedCases, report.Results)
			}
		})
	}
}

func runCrossBackendCase(t *testing.T, tc replaytest.ReplayCase) *replaytest.Report {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	sqliteFactory := NewFactory(db, WithSessionOpts(
		sessionsqlite.WithSummarizer(replaytest.NewFakeSummarizer()),
		sessionsqlite.WithAsyncSummaryNum(1),
		sessionsqlite.WithSummaryJobTimeout(time.Second),
	))
	sqliteSession, sqliteMemory, sqliteProfile, err := sqliteFactory()
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = sqliteSession.Close()
		_ = sqliteMemory.Close()
		_ = db.Close()
	}()

	inMemorySession := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(replaytest.NewFakeSummarizer()),
		sessioninmemory.WithAsyncSummaryNum(1),
		sessioninmemory.WithSummaryJobTimeout(time.Second),
	)
	inMemoryMemory := memoryinmemory.NewMemoryService()
	defer inMemorySession.Close()
	defer inMemoryMemory.Close()

	h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
	h.AddBackend(replaytest.NamedBackend{
		Name:           "inmemory",
		Profile:        replaytest.InMemoryProfile(),
		SessionService: inMemorySession,
		MemoryService:  inMemoryMemory,
	})
	h.AddBackend(replaytest.NamedBackend{
		Name:           "sqlite",
		Profile:        sqliteProfile,
		SessionService: sqliteSession,
		MemoryService:  sqliteMemory,
	})

	report, err := h.Run([]replaytest.ReplayCase{tc})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func crossBackendReplayCases() []replaytest.ReplayCase {
	cases := make([]replaytest.ReplayCase, 0, len(replaytest.AllCases()))
	for _, tc := range replaytest.AllCases() {
		if tc.RequiredCaps.NeedsMemory || tc.RequiredCaps.NeedsAsyncSummary {
			continue
		}
		cases = append(cases, tc)
	}
	return cases
}
