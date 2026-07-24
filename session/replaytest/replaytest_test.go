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
	"time"

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
			t.Cleanup(func() {
				require.NoError(t, candidate.Session.Close())
				require.NoError(t, candidate.Memory.Close())
			})

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

func TestNormalizeUsesPersistedSummaryOwner(t *testing.T) {
	ctx := context.Background()
	tc := summaryCase()
	cfg := RunConfig{
		AppName:   "replaytest-owner",
		UserID:    "user",
		SessionID: "summary-owner",
	}
	baseline := inMemoryReplayBackend()
	t.Cleanup(func() { require.NoError(t, baseline.Session.Close()) })
	candidateSession := NewJSONSessionService(
		WithJSONSessionSummarizer(staticSummary{}),
		WithJSONSessionSummaryFilterAllowlist("agent/main"),
		WithJSONSessionCascadeFullSessionSummary(false),
	)
	candidate := Backend{
		Name:    "json_persistent",
		Session: candidateSession,
		Memory:  NewJSONMemoryService(),
	}
	t.Cleanup(func() {
		require.NoError(t, candidate.Session.Close())
		require.NoError(t, candidate.Memory.Close())
	})

	baseSnap, err := Run(ctx, baseline, cfg, tc)
	require.NoError(t, err)
	_, err = Run(ctx, candidate, cfg, tc)
	require.NoError(t, err)
	err = candidateSession.withSessionStore(true, func(store *jsonSessionStore) error {
		owners := ensureJSONSummaryOwners(store, session.Key{
			AppName:   cfg.AppName,
			UserID:    cfg.UserID,
			SessionID: cfg.SessionID,
		})
		owners["agent/main"] = "wrong-session"
		return nil
	})
	require.NoError(t, err)
	candSnap, err := Normalize(ctx, candidate, cfg, tc)
	require.NoError(t, err)

	diffs := CompareSnapshots(baseSnap, candSnap)
	require.True(t, hasBlockingDiff(diffs), "expected summary owner diff, got %#v", diffs)
	require.True(t, hasDiffAtPath(diffs, "/summaries/agent~1main/session_id"))
}

func TestSummaryCriticalDiffsAreBlocking(t *testing.T) {
	ctx := context.Background()
	tc := summaryCase()
	backend := inMemoryReplayBackend()
	t.Cleanup(func() { require.NoError(t, backend.Session.Close()) })
	cfg := RunConfig{
		AppName:   "replaytest-summary-critical",
		UserID:    "user",
		SessionID: "summary-critical",
	}
	baseline, err := Run(ctx, backend, cfg, tc)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*Snapshot)
		path   string
	}{
		{
			name: "summary lost",
			mutate: func(s *Snapshot) {
				delete(s.Summaries, "agent/main")
			},
			path: "/summaries/agent~1main",
		},
		{
			name: "summary filter key wrong",
			mutate: func(s *Snapshot) {
				sum := s.Summaries["agent/main"]
				delete(s.Summaries, "agent/main")
				sum.FilterKey = "agent/wrong"
				s.Summaries["agent/wrong"] = sum
			},
			path: "/summaries/agent~1main",
		},
		{
			name: "summary owner wrong",
			mutate: func(s *Snapshot) {
				sum := s.Summaries["agent/main"]
				sum.SessionID = "wrong-session"
				s.Summaries["agent/main"] = sum
			},
			path: "/summaries/agent~1main/session_id",
		},
		{
			name: "summary overwrite wrong",
			mutate: func(s *Snapshot) {
				sum := s.Summaries["agent/main"]
				sum.Text = "stale summary"
				s.Summaries["agent/main"] = sum
			},
			path: "/summaries/agent~1main/summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshot(t, baseline)
			tt.mutate(&candidate)
			diffs := CompareSnapshots(baseline, candidate)
			require.True(t, hasBlockingDiff(diffs), "expected blocking diff, got %#v", diffs)
			require.True(t, hasDiffAtPath(diffs, tt.path), "expected path %s in %#v", tt.path, diffs)
		})
	}
}

func TestMemorySearchOrderDiffIsBlocking(t *testing.T) {
	ctx := context.Background()
	tc := memoryCase()
	backend := inMemoryReplayBackend()
	t.Cleanup(func() { require.NoError(t, backend.Session.Close()) })
	cfg := RunConfig{
		AppName:   "replaytest-memory-order",
		UserID:    "user",
		SessionID: "memory-order",
	}
	baseline, err := Run(ctx, backend, cfg, tc)
	require.NoError(t, err)
	candidate := cloneSnapshot(t, baseline)
	search := candidate.MemorySearches["User"]
	require.GreaterOrEqual(t, len(search), 2)
	search[0], search[1] = search[1], search[0]
	candidate.MemorySearches["User"] = search

	diffs := CompareSnapshots(baseline, candidate)
	require.True(t, hasBlockingDiff(diffs), "expected memory search order diff, got %#v", diffs)
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
			CapabilityEventPaging: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "inmemory supports event window filtering but not offset event paging",
			},
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
			CapabilityEventPaging: {
				Supported:   false,
				AllowedDiff: true,
				Explanation: "JSON replay backend supports event windows but not offset event paging",
			},
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
		snap.Events[1], snap.Events[2] = snap.Events[2], snap.Events[1]
	case 2:
		snap.Events[2].Extensions[event.ToolCallArgsExtensionKey] = NormalizedValue{
			Value: map[string]any{"call-weather-1": map[string]any{"city": "Guangzhou"}},
		}
	case 3:
		snap.State["theme"] = NormalizedValue{Value: "wrong"}
	case 4:
		snap.MemorySearches["concise answers"][0].Content = "wrong memory"
	case 5:
		sum := snap.Summaries["agent/main"]
		delete(snap.Summaries, "agent/main")
		sum.SessionID = "wrong-session"
		snap.Summaries["agent/main"] = sum
	case 6:
		snap.Summaries[""].Boundary.LastEventID = "event#wrong"
	case 7:
		snap.Tracks["tool/weather"][0].Timestamp = baseTime.Add(time.Hour).UTC().Format(time.RFC3339Nano)
	case 8:
		snap.Events[1], snap.Events[2] = snap.Events[2], snap.Events[1]
	case 9:
		if snap.State == nil {
			snap.State = make(map[string]NormalizedValue)
		}
		snap.State[session.StateAppPrefix+"dirty"] = NormalizedValue{Value: "should-not-persist"}
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

func hasDiffAtPath(diffs []Diff, path string) bool {
	for _, diff := range diffs {
		if diff.Path == path {
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
