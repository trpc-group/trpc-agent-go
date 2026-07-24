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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestReplayConsistencyJSONLightweight(t *testing.T) {
	ctx := context.Background()
	cases := replaytest.StandardCases()
	var allDiffs []replaytest.Diff

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			cfg := replaytest.RunConfig{
				AppName:   "replay-consistency-json",
				UserID:    "user-" + tc.Name,
				SessionID: "session-" + tc.Name,
			}
			baseline := newJSONBaselineBackend()
			t.Cleanup(func() {
				require.NoError(t, baseline.Session.Close())
				require.NoError(t, baseline.Memory.Close())
			})
			candidate := newJSONPersistentBackend(t)
			t.Cleanup(func() {
				require.NoError(t, candidate.Session.Close())
				require.NoError(t, candidate.Memory.Close())
			})

			baseSnap, err := replaytest.Run(ctx, baseline, cfg, tc)
			require.NoError(t, err)
			candSnap, err := replaytest.Run(ctx, candidate, cfg, tc)
			require.NoError(t, err)

			diffs := replaytest.CompareSnapshots(baseSnap, candSnap)
			blocking := jsonBlockingDiffs(diffs)
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

func TestExampleDiffReportListsStandardCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "session_memory_summary_track_diff_report.json"))
	require.NoError(t, err)
	var report replaytest.Report
	require.NoError(t, json.Unmarshal(raw, &report))

	want := make([]string, 0, len(replaytest.StandardCases()))
	for _, tc := range replaytest.StandardCases() {
		want = append(want, tc.Name)
	}
	require.Equal(t, want, report.Cases)
}

func newJSONBaselineBackend() replaytest.Backend {
	return replaytest.Backend{
		Name: "inmemory",
		Session: sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(jsonReplaySummary{}),
			sessioninmemory.WithSummaryFilterAllowlist("agent/main"),
			sessioninmemory.WithCascadeFullSessionSummary(false),
		),
		Memory: memoryinmemory.NewMemoryService(),
		Capabilities: jsonReplayCapabilities(map[string]replaytest.CapabilityStatus{
			replaytest.CapabilityEventPaging: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "inmemory supports event window filtering but not offset event paging",
			},
		}),
	}
}

func newJSONPersistentBackend(t *testing.T) replaytest.Backend {
	t.Helper()
	dir := t.TempDir()
	return replaytest.Backend{
		Name: "json_persistent",
		Session: replaytest.NewJSONSessionService(
			replaytest.WithJSONSessionPath(filepath.Join(dir, "session.json")),
			replaytest.WithJSONSessionSummarizer(jsonReplaySummary{}),
			replaytest.WithJSONSessionSummaryFilterAllowlist("agent/main"),
			replaytest.WithJSONSessionCascadeFullSessionSummary(false),
		),
		Memory: replaytest.NewJSONMemoryService(
			replaytest.WithJSONMemoryPath(filepath.Join(dir, "memory.json")),
		),
		Capabilities: jsonReplayCapabilities(map[string]replaytest.CapabilityStatus{
			replaytest.CapabilityEventPaging: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "JSON replay backend supports event windows but not offset event paging",
			},
			replaytest.CapabilityTTL: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "JSON replay backend is a local persistence simulator without TTL expiry",
			},
		}),
	}
}

func jsonReplayCapabilities(overrides map[string]replaytest.CapabilityStatus) map[string]replaytest.CapabilityStatus {
	caps := map[string]replaytest.CapabilityStatus{
		replaytest.CapabilityMemory:  {Supported: true},
		replaytest.CapabilitySummary: {Supported: true},
		replaytest.CapabilityTrack:   {Supported: true},
	}
	for k, v := range overrides {
		caps[k] = v
	}
	return caps
}

type jsonReplaySummary struct{}

var _ summary.SessionSummarizer = jsonReplaySummary{}

func (jsonReplaySummary) ShouldSummarize(*session.Session) bool {
	return true
}

func (jsonReplaySummary) Summarize(_ context.Context, sess *session.Session) (string, error) {
	last := ""
	if len(sess.Events) > 0 {
		last = sess.Events[len(sess.Events)-1].ID
	}
	return "summary:" + sess.ID + ":last=" + last, nil
}

func (jsonReplaySummary) SetPrompt(string) {}

func (jsonReplaySummary) SetModel(model.Model) {}

func (jsonReplaySummary) Metadata() map[string]any {
	return map[string]any{"model_available": true}
}

func jsonBlockingDiffs(diffs []replaytest.Diff) []replaytest.Diff {
	out := []replaytest.Diff{}
	for _, diff := range diffs {
		if !diff.AllowedDiff {
			out = append(out, diff)
		}
	}
	return out
}
