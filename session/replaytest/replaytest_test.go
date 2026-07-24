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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestStandardCasesDetectInjectedInconsistencies(t *testing.T) {
	ctx := context.Background()
	for idx, tc := range StandardCases() {
		t.Run(tc.Name, func(t *testing.T) {
			backend := inMemoryReplayBackend()
			t.Cleanup(func() { require.NoError(t, backend.Session.Close()) })
			cfg := RunConfig{
				AppName:   "replaytest-unit",
				UserID:    "user",
				SessionID: tc.Name,
			}
			baseline, err := Run(ctx, backend, cfg, tc)
			require.NoError(t, err)

			candidate := cloneSnapshot(t, baseline)
			injectMismatch(t, idx, &candidate)

			diffs := CompareSnapshots(baseline, candidate)
			require.NotEmpty(t, diffs)
			require.True(t, hasBlockingDiff(diffs), "expected non-allowed diff, got %#v", diffs)
		})
	}
}

func TestIdenticalSnapshotHasNoDiff(t *testing.T) {
	ctx := context.Background()
	tc := singleTurnCase()
	backend := inMemoryReplayBackend()
	t.Cleanup(func() { require.NoError(t, backend.Session.Close()) })
	cfg := RunConfig{
		AppName:   "replaytest-unit",
		UserID:    "user",
		SessionID: "identical",
	}
	baseline, err := Run(ctx, backend, cfg, tc)
	require.NoError(t, err)
	candidate := cloneSnapshot(t, baseline)
	require.Empty(t, CompareSnapshots(baseline, candidate))
}

func TestStandardCasesMatchJSONPersistentBackend(t *testing.T) {
	ctx := context.Background()
	for _, tc := range StandardCases() {
		t.Run(tc.Name, func(t *testing.T) {
			cfg := RunConfig{
				AppName:   "replaytest-json",
				UserID:    "user",
				SessionID: tc.Name,
			}
			baseline := inMemoryReplayBackend()
			t.Cleanup(func() { require.NoError(t, baseline.Session.Close()) })
			candidate := jsonReplayBackend()
			t.Cleanup(func() { require.NoError(t, candidate.Session.Close()) })

			baseSnap, err := Run(ctx, baseline, cfg, tc)
			require.NoError(t, err)
			candSnap, err := Run(ctx, candidate, cfg, tc)
			require.NoError(t, err)

			diffs := CompareSnapshots(baseSnap, candSnap)
			blocking := hasBlockingDiff(diffs)
			if blocking {
				t.Logf("diffs: %+v", diffs)
			}
			require.False(t, blocking)
		})
	}
}

func TestReportWriter(t *testing.T) {
	report := BuildReport(StandardCases()[:1], []Diff{{
		CaseName:         "case",
		BaselineBackend:  "a",
		CandidateBackend: "b",
		Path:             "/events/0/content",
		Baseline:         "hello",
		Candidate:        "hola",
	}})
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, WriteReport(path, report))
	data := map[string]any{}
	raw := mustReadFile(t, path)
	require.NoError(t, json.Unmarshal(raw, &data))
	require.Equal(t, "trpc-agent-go/session/replaytest", data["generated_by"])
}

func inMemoryReplayBackend() Backend {
	return Backend{
		Name: "inmemory",
		Session: sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(staticSummary{}),
			sessioninmemory.WithSummaryFilterAllowlist("agent/main"),
			sessioninmemory.WithCascadeFullSessionSummary(false),
		),
		Memory: memoryinmemory.NewMemoryService(),
		Capabilities: map[string]CapabilityStatus{
			CapabilityMemory:  {Supported: true},
			CapabilitySummary: {Supported: true},
			CapabilityTrack:   {Supported: true},
		},
	}
}

func jsonReplayBackend() Backend {
	return Backend{
		Name: "json_persistent",
		Session: NewJSONSessionService(
			WithJSONSessionSummarizer(staticSummary{}),
			WithJSONSessionSummaryFilterAllowlist("agent/main"),
			WithJSONSessionCascadeFullSessionSummary(false),
		),
		Memory: NewJSONMemoryService(),
		Capabilities: map[string]CapabilityStatus{
			CapabilityMemory:  {Supported: true},
			CapabilitySummary: {Supported: true},
			CapabilityTrack:   {Supported: true},
			CapabilityTTL: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "JSON replay backend is a serialization persistence simulator without TTL expiry",
			},
		},
	}
}

type staticSummary struct{}

var _ summary.SessionSummarizer = staticSummary{}

func (staticSummary) ShouldSummarize(*session.Session) bool {
	return true
}

func (staticSummary) Summarize(_ context.Context, sess *session.Session) (string, error) {
	last := ""
	if len(sess.Events) > 0 {
		last = sess.Events[len(sess.Events)-1].ID
	}
	return "summary:" + sess.ID + ":last=" + last, nil
}

func (staticSummary) SetPrompt(string) {}

func (staticSummary) SetModel(model.Model) {}

func (staticSummary) Metadata() map[string]any {
	return map[string]any{"model_available": true}
}

func cloneSnapshot(t *testing.T, in Snapshot) Snapshot {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out Snapshot
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func injectMismatch(t *testing.T, idx int, snap *Snapshot) {
	t.Helper()
	switch idx {
	case 0:
		snap.Events[1].Content = "wrong answer"
	case 1:
		snap.Events[2].Content = "wrong order"
	case 2:
		snap.Events[2].Extensions[event.ToolCallArgsExtensionKey] = NormalizedValue{
			Value: map[string]any{"call-weather-1": map[string]any{"city": "Guangzhou"}},
		}
	case 3:
		snap.State["theme"] = NormalizedValue{Value: "wrong"}
	case 4:
		snap.Memories[0].Content = "wrong memory"
	case 5:
		sum := snap.Summaries["agent/main"]
		delete(snap.Summaries, "agent/main")
		snap.Summaries["agent/wrong"] = sum
	case 6:
		snap.Summaries[""].Boundary.LastEventID = "event#wrong"
	case 7:
		snap.Tracks["subtask/research"][0].Error = "different error"
	case 8:
		snap.Events[0].FilterKey = "tools/wrong"
	case 9:
		snap.Memories = append(snap.Memories, snap.Memories[0])
	default:
		t.Fatalf("missing mismatch injector for case %d", idx)
	}
}

func hasBlockingDiff(diffs []Diff) bool {
	for _, diff := range diffs {
		if !diff.AllowedDiff {
			return true
		}
	}
	return false
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}
