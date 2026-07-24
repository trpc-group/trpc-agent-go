//go:build replay_sqlite

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replay_consistency

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestReplayConsistencyLightweight(t *testing.T) {
	ctx := context.Background()
	cases := replaytest.StandardCases()
	var allDiffs []replaytest.Diff

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			cfg := replaytest.RunConfig{
				AppName:   "replay-consistency",
				UserID:    "user-" + tc.Name,
				SessionID: "session-" + tc.Name,
			}
			baseline := newInMemoryBackend()
			t.Cleanup(func() {
				require.NoError(t, baseline.Session.Close())
				require.NoError(t, baseline.Memory.Close())
			})
			candidate := newSQLiteBackend(t)
			t.Cleanup(func() {
				require.NoError(t, candidate.Session.Close())
				require.NoError(t, candidate.Memory.Close())
			})

			baseSnap, err := replaytest.Run(ctx, baseline, cfg, tc)
			require.NoError(t, err)
			candSnap, err := replaytest.Run(ctx, candidate, cfg, tc)
			require.NoError(t, err)

			diffs := replaytest.CompareSnapshots(baseSnap, candSnap)
			blocking := blockingDiffs(diffs)
			if len(blocking) > 0 {
				report := replaytest.BuildReport(cases, diffs)
				reportPath := filepath.Join(t.TempDir(), "session_memory_summary_track_diff_report.json")
				require.NoError(t, replaytest.WriteReport(reportPath, report))
				t.Fatalf("replay diffs found, report: %s\n%+v", reportPath, blocking)
			}
			allDiffs = append(allDiffs, diffs...)
		})
	}

	report := replaytest.BuildReport(cases, allDiffs)
	reportPath := filepath.Join(t.TempDir(), "session_memory_summary_track_diff_report.json")
	require.NoError(t, replaytest.WriteReport(reportPath, report))
}

func newInMemoryBackend() replaytest.Backend {
	return replaytest.Backend{
		Name: "inmemory",
		Session: sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(replaySummary{}),
			sessioninmemory.WithSummaryFilterAllowlist("agent/main"),
			sessioninmemory.WithCascadeFullSessionSummary(false),
		),
		Memory: memoryinmemory.NewMemoryService(),
		Capabilities: replayCapabilities(map[string]replaytest.CapabilityStatus{
			replaytest.CapabilityEventPaging: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "inmemory supports event window filtering but not offset event paging",
			},
		}),
	}
}

func newSQLiteBackend(t *testing.T) replaytest.Backend {
	t.Helper()
	sessionDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	sessionSvc, err := sessionsqlite.NewService(
		sessionDB,
		sessionsqlite.WithSummarizer(replaySummary{}),
		sessionsqlite.WithSummaryFilterAllowlist("agent/main"),
		sessionsqlite.WithCascadeFullSessionSummary(false),
	)
	require.NoError(t, err)

	memoryDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "memory.db"))
	require.NoError(t, err)
	memorySvc, err := memorysqlite.NewService(memoryDB)
	require.NoError(t, err)

	return replaytest.Backend{
		Name:    "sqlite",
		Session: sessionSvc,
		Memory:  memorySvc,
		Capabilities: replayCapabilities(map[string]replaytest.CapabilityStatus{
			replaytest.CapabilityEventPaging: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "sqlite service does not support session.WithGetSessionEventPage",
			},
		}),
	}
}

func replayCapabilities(overrides map[string]replaytest.CapabilityStatus) map[string]replaytest.CapabilityStatus {
	caps := map[string]replaytest.CapabilityStatus{
		replaytest.CapabilityMemory:  {Supported: true},
		replaytest.CapabilitySummary: {Supported: true},
		replaytest.CapabilityTrack:   {Supported: true},
		replaytest.CapabilityTTL:     {Supported: true},
	}
	for k, v := range overrides {
		caps[k] = v
	}
	return caps
}

type replaySummary struct{}

var _ summary.SessionSummarizer = replaySummary{}

func (replaySummary) ShouldSummarize(*session.Session) bool {
	return true
}

func (replaySummary) Summarize(_ context.Context, sess *session.Session) (string, error) {
	last := ""
	if len(sess.Events) > 0 {
		last = sess.Events[len(sess.Events)-1].ID
	}
	return "summary:" + sess.ID + ":last=" + last, nil
}

func (replaySummary) SetPrompt(string) {}

func (replaySummary) SetModel(model.Model) {}

func (replaySummary) Metadata() map[string]any {
	return map[string]any{"model_available": true}
}

func blockingDiffs(diffs []replaytest.Diff) []replaytest.Diff {
	out := []replaytest.Diff{}
	for _, diff := range diffs {
		if !diff.AllowedDiff {
			out = append(out, diff)
		}
	}
	return out
}
