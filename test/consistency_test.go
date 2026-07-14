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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/test/replaytest"
)

func TestLoadReplayCases(t *testing.T) {
	cases, err := replaytest.LoadReplayCasesFromDir("testdata")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cases), 2)

	for _, rc := range cases {
		t.Run(rc.Name, func(t *testing.T) {
			require.NotEmpty(t, rc.Name)
			require.NotEmpty(t, rc.AppName)
			require.NotEmpty(t, rc.UserID)
			require.NotEmpty(t, rc.SessionID)
			require.NotEmpty(t, rc.Steps)
		})
	}
}

func TestReplayConsistency_BasicCases(t *testing.T) {
	ctx := context.Background()
	backends := replaytest.NewReplayBackends(t)
	require.Len(t, backends, 2)

	cases, err := replaytest.LoadReplayCasesFromDir("testdata")
	require.NoError(t, err)
	require.NotEmpty(t, cases)

	reportPath := filepath.Join(t.TempDir(), "diff_report.json")
	t.Setenv(replaytest.EnvReportPath, reportPath)

	var allDiffs []replaytest.DiffEntry
	for _, rc := range cases {
		t.Run(rc.Name, func(t *testing.T) {
			var results []*replaytest.ReplayResult
			for _, b := range backends {
				results = append(results,
					replaytest.RunReplayCase(t, ctx, b, rc))
			}
			require.Len(t, results, 2)

			diffs := replaytest.CompareSnapshots(
				rc.Name,
				results[0].Snapshot,
				results[1].Snapshot,
				rc.AllowedDiffs,
			)
			for _, d := range diffs {
				t.Logf("diff [%s] %s %s: left=%v right=%v allowed=%v",
					d.Section, d.Path, d.BackendA+" vs "+d.BackendB,
					d.Left, d.Right, d.Allowed)
			}
			require.Falsef(t, replaytest.HasUnexpectedDiffs(diffs),
				"unexpected replay diffs for case %s: %+v", rc.Name, diffs)
			allDiffs = append(allDiffs, diffs...)
		})
	}

	require.NoError(t, replaytest.WriteDiffReport("", allDiffs))

	raw, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var parsed []map[string]any
	require.NoError(t, json.Unmarshal(raw, &parsed))
	t.Logf("diff report written: %s (%d entries)", reportPath, len(parsed))
}

func TestReplayConsistency_VerifyFields(t *testing.T) {
	ctx := context.Background()
	backends := replaytest.NewReplayBackends(t)

	cases, err := replaytest.LoadReplayCasesFromDir("testdata")
	require.NoError(t, err)

	var singleTurn *replaytest.ReplayCase
	for _, rc := range cases {
		if rc.Name == "single_turn" {
			singleTurn = rc
			break
		}
	}
	require.NotNil(t, singleTurn, "case single_turn not found")

	var results []*replaytest.ReplayResult
	for _, b := range backends {
		results = append(results, replaytest.RunReplayCase(t, ctx, b, singleTurn))
	}

	for _, result := range results {
		t.Run(result.Backend, func(t *testing.T) {
			snap := result.Snapshot
			require.Equal(t, singleTurn.SessionID, snap.Session.ID)
			require.Equal(t, singleTurn.AppName, snap.Session.App)
			require.Equal(t, singleTurn.UserID, snap.Session.UserID)
			require.Len(t, snap.Events, 2)

			author0, _ := snap.Events[0]["author"].(string)
			require.Equal(t, "user", author0)
			author1, _ := snap.Events[1]["author"].(string)
			require.Equal(t, "agent", author1)

			role0 := extractRole(t, snap.Events[0])
			role1 := extractRole(t, snap.Events[1])
			require.Equal(t, "user", role0)
			require.Equal(t, "assistant", role1)

			content0 := extractContent(t, snap.Events[0])
			content1 := extractContent(t, snap.Events[1])
			require.Equal(t, "hello replay", content0)
			require.Equal(t, "hello from agent", content1)

			require.Contains(t, snap.State, "seed")
			require.Contains(t, snap.State, "turn")
			require.Empty(t, snap.Tracks)
			require.Empty(t, snap.Summaries)
			require.Empty(t, snap.Memories)

			t.Logf("backend %s snapshot verified: events=%d state_keys=%d",
				result.Backend, len(snap.Events), len(snap.State))
		})
	}

	diffs := replaytest.CompareSnapshots(
		singleTurn.Name,
		results[0].Snapshot,
		results[1].Snapshot,
		singleTurn.AllowedDiffs,
	)
	require.Empty(t, diffs, "cross-backend diffs for single_turn must be zero")
}

func extractRole(t *testing.T, evt map[string]any) string {
	t.Helper()
	choices, _ := evt["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	if msg == nil {
		return ""
	}
	role, _ := msg["role"].(string)
	return role
}

func extractContent(t *testing.T, evt map[string]any) string {
	t.Helper()
	choices, _ := evt["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	if msg == nil {
		return ""
	}
	content, _ := msg["content"].(string)
	return strings.TrimSpace(content)
}
