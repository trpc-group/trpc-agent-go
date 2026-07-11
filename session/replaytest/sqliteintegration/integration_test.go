//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build cgo

package sqliteintegration

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memSQLite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessSQLite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// newSQLiteBackend creates a BackendFactory using in-memory SQLite.
func newSQLiteBackend() replaytest.BackendFactory {
	return replaytest.BackendFactory{
		Name: "sqlite",
		CreateSession: func() (session.Service, error) {
			db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
			if err != nil {
				return nil, err
			}
			return sessSQLite.NewService(db)
		},
		CreateTrack: func() (session.TrackService, error) {
			db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
			if err != nil {
				return nil, err
			}
			return sessSQLite.NewService(db)
		},
		CreateMemory: func() (memory.Service, error) {
			db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
			if err != nil {
				return nil, err
			}
			return memSQLite.NewService(db)
		},
	}
}

// TestInMemoryVsSQLite runs all replay cases comparing InMemory vs SQLite.
func TestInMemoryVsSQLite(t *testing.T) {
	inmem := replaytest.NewInMemoryBackend()
	sqlite := newSQLiteBackend()

	harness := replaytest.NewHarness(
		replaytest.WithBackends(inmem, sqlite),
	)
	cases := replaytest.AllReplayCases()

	ctx := context.Background()
	report, err := harness.Run(ctx, cases)
	require.NoError(t, err)

	// Write the report for inspection.
	reportPath := t.TempDir() + "/inmemory_vs_sqlite_report.json"
	err = replaytest.WriteReport(reportPath, report)
	require.NoError(t, err)
	t.Logf("Report: %s", report.Summary())
	t.Logf("Report file: %s", reportPath)

	// We expect no real (non-allowed) diffs for these cases.
	// Some allowed diffs (timestamps, IDs) are acceptable.
	if report.FailCases > 0 {
		for _, cr := range report.CaseResults {
			if cr.HasDiff {
				t.Errorf("Case %q: %d diffs (%d allowed)", cr.CaseName, cr.DiffCount, cr.AllowedDiffCount)
				for _, d := range cr.Differences {
					if !d.AllowedDiff {
						t.Logf("  DIFF: %s base=%v other=%v (%s)",
							d.FieldPath, d.BaseValue, d.CompareValue, d.Explanation)
					}
				}
			}
		}
	}
}

// TestSQLiteOnlyBaseline runs the harness with two independent SQLite backends.
func TestSQLiteOnlyBaseline(t *testing.T) {
	sqlite1 := newSQLiteBackend()
	sqlite1.Name = "sqlite-A"
	sqlite2 := newSQLiteBackend()
	sqlite2.Name = "sqlite-B"

	harness := replaytest.NewHarness(
		replaytest.WithBackends(sqlite1, sqlite2),
	)
	cases := replaytest.AllReplayCases()

	ctx := context.Background()
	report, err := harness.Run(ctx, cases)
	require.NoError(t, err)
	t.Logf("Report: %s", report.Summary())

	// Two identical SQLite implementations should produce no real diffs.
	if report.FailCases > 0 {
		for _, cr := range report.CaseResults {
			if cr.HasDiff {
				for _, d := range cr.Differences {
					if !d.AllowedDiff {
						t.Errorf("Case %q: %s base=%v other=%v",
							cr.CaseName, d.FieldPath, d.BaseValue, d.CompareValue)
					}
				}
			}
		}
	}
}

// TestGenerateExampleReport generates the example diff report JSON file.
func TestGenerateExampleReport(t *testing.T) {
	inmem := replaytest.NewInMemoryBackend()
	sqlite := newSQLiteBackend()

	harness := replaytest.NewHarness(
		replaytest.WithBackends(inmem, sqlite),
	)
	cases := replaytest.AllReplayCases()

	ctx := context.Background()
	report, err := harness.Run(ctx, cases)
	require.NoError(t, err)

	// Write to a well-known location for the deliverables.
	reportPath := "session_memory_summary_track_diff_report.json"
	err = replaytest.WriteReport(reportPath, report)
	require.NoError(t, err)
	t.Logf("Example report written to: %s", reportPath)
}
