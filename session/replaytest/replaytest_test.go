//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type staticSummarizer struct{}

func (staticSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (staticSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	events := sess.GetEvents()
	if len(events) == 0 || events[len(events)-1].Response == nil ||
		len(events[len(events)-1].Choices) == 0 {
		return "replay summary: empty", nil
	}
	return fmt.Sprintf("replay summary: %s", events[len(events)-1].Choices[0].Message.Content), nil
}

func (staticSummarizer) SetPrompt(string) {}

func (staticSummarizer) SetModel(model.Model) {}

func (staticSummarizer) Metadata() map[string]any { return nil }

var _ summary.SessionSummarizer = staticSummarizer{}

func TestStandardCasesAreConsistentAcrossLightweightBackends(t *testing.T) {
	backends := newLightweightBackends(t)
	report, err := Run(context.Background(), backends, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
}

func TestLightweightSuiteCompletesWithinThirtySeconds(t *testing.T) {
	started := time.Now()
	report, err := Run(context.Background(), newLightweightBackends(t), StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences())
	require.Less(t, time.Since(started), 30*time.Second)
}

func TestStandardCasesDetectInjectedDifferences(t *testing.T) {
	for _, replayCase := range StandardCases() {
		t.Run(replayCase.Name, func(t *testing.T) {
			backend := newInMemoryBackend(t)
			baseline, err := replayCase.Replay(context.Background(), backend)
			require.NoError(t, err)
			actual := cloneSnapshot(t, baseline)
			actual.Data["injected_mismatch"] = replayCase.Name

			diffs := Compare(replayCase.Name, "injected", baseline, actual)
			require.Len(t, diffs, 1)
			require.Equal(t, "data.injected_mismatch", diffs[0].Path)
		})
	}
}

func TestCaptureNormalizesVolatileFieldsAndPreservesMemoryID(t *testing.T) {
	backend := newInMemoryBackend(t)
	key := session.Key{AppName: replayApp, UserID: replayUser, SessionID: "normalize"}
	sess, err := backend.Session.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	require.NoError(t, backend.Session.AppendEvent(context.Background(), sess,
		newReplayEvent("generated-id", model.RoleUser, "hello", "")))
	require.NoError(t, backend.Memory.AddMemory(context.Background(), memory.UserKey{
		AppName: replayApp,
		UserID:  replayUser,
	}, "keep memory identity", nil))

	snapshot, err := Capture(context.Background(), backend, key)
	require.NoError(t, err)
	events := snapshot.Data["events"].([]any)
	require.NotContains(t, events[0].(map[string]any), "id")
	memories := snapshot.Data["memories"].([]any)
	require.NotEmpty(t, memories[0].(map[string]any)["id"])
	scope := memories[0].(map[string]any)["scope"].(map[string]any)
	require.Equal(t, replayApp, scope["app_name"])
	require.Equal(t, replayUser, scope["user_id"])
}

func TestRunValidatesBackends(t *testing.T) {
	_, err := Run(context.Background(), []Backend{{Name: "only"}}, StandardCases())
	require.EqualError(t, err, "replay requires at least two backends")
}

func TestSummaryDifferencesIdentifyLossOverwriteScopeAndSession(t *testing.T) {
	backend := newInMemoryBackend(t)
	var summaryCase Case
	for _, replayCase := range StandardCases() {
		if replayCase.Name == "summary_update" {
			summaryCase = replayCase
			break
		}
	}
	baseline, err := summaryCase.Replay(context.Background(), backend)
	require.NoError(t, err)
	summaries := baseline.Data["summaries"].(map[string]any)
	require.Contains(t, summaries, "branch-a")
	summary := summaries["branch-a"].(map[string]any)
	require.Equal(t, "replay summary: add this too", summary["summary"])
	require.Equal(t, "normalized", summary["updated_at"])
	boundary := summary["boundary"].(map[string]any)
	require.Equal(t, float64(session.SummaryBoundaryVersion), boundary["version"])
	require.Equal(t, "branch-a", boundary["filter_key"])

	lost := cloneSnapshot(t, baseline)
	delete(lost.Data["summaries"].(map[string]any), "branch-a")
	require.NotEmpty(t, Compare("summary_update", "injected", baseline, lost))

	overwritten := cloneSnapshot(t, baseline)
	overwritten.Data["summaries"].(map[string]any)["branch-a"].(map[string]any)["summary"] = "wrong summary"
	require.NotEmpty(t, Compare("summary_update", "injected", baseline, overwritten))

	wrongScope := cloneSnapshot(t, baseline)
	wrongScope.Data["summaries"].(map[string]any)["other-branch"] = wrongScope.Data["summaries"].(map[string]any)["branch-a"]
	delete(wrongScope.Data["summaries"].(map[string]any), "branch-a")
	require.NotEmpty(t, Compare("summary_update", "injected", baseline, wrongScope))

	wrongSession := cloneSnapshot(t, baseline)
	wrongSession.SessionID = "another-session"
	diffs := Compare("summary_update", "injected", baseline, wrongSession)
	require.Equal(t, "session_id", diffs[0].Path)
}

func TestUnsupportedFieldsAreReportedAsAllowedDifferences(t *testing.T) {
	baseline := Snapshot{SessionID: "s", Data: map[string]any{"tracks": map[string]any{"tool": "present"}}}
	actual := Snapshot{SessionID: "s", Data: map[string]any{"tracks": map[string]any{}}}
	differences := Compare("tracks", "limited", baseline, actual)
	markAllowedUnsupported(differences, []Unsupported{{Path: "tracks", Reason: "backend has no track storage"}})
	require.NotEmpty(t, differences)
	require.True(t, differences[0].AllowedDiff)
	require.Equal(t, "backend has no track storage", differences[0].Reason)

	unsupported := unsupportedDifferences("tracks", "limited", "s", []Unsupported{{
		Path: "tracks", Reason: "backend has no track storage",
	}})
	require.Len(t, unsupported, 1)
	require.True(t, unsupported[0].AllowedDiff)
}

func TestTrackCasePreservesReplayFields(t *testing.T) {
	backend := newInMemoryBackend(t)
	var trackCase Case
	for _, replayCase := range StandardCases() {
		if replayCase.Name == "track_events" {
			trackCase = replayCase
			break
		}
	}
	snapshot, err := trackCase.Replay(context.Background(), backend)
	require.NoError(t, err)
	track := snapshot.Data["tracks"].(map[string]any)["tool"].(map[string]any)
	events := track["events"].([]any)
	require.Len(t, events, 2)
	payload := events[1].(map[string]any)["payload"].(map[string]any)
	require.Equal(t, "exception", payload["event_type"])
	require.Equal(t, "timeout", payload["error"])
	require.Equal(t, "normalized", payload["duration_ms"])
	require.Equal(t, "normalized", events[0].(map[string]any)["timestamp"])
	require.Equal(t, float64(0), events[0].(map[string]any)["sequence"])
	require.Equal(t, float64(1), events[1].(map[string]any)["sequence"])
}

func TestTrackNormalizationPreservesOrderAndIgnoresBackendTiming(t *testing.T) {
	baseline, err := normalize(map[string]any{"tracks": map[string]any{"tool": map[string]any{
		"events": []any{
			map[string]any{"timestamp": "2026-01-01T00:00:00Z", "payload": map[string]any{"duration_ms": 5}},
			map[string]any{"timestamp": "2026-01-01T00:00:01Z", "payload": map[string]any{"duration_ms": 10}},
		},
	}}}, nil)
	require.NoError(t, err)
	actual, err := normalize(map[string]any{"tracks": map[string]any{"tool": map[string]any{
		"events": []any{
			map[string]any{"timestamp": "2026-02-01T00:00:00Z", "payload": map[string]any{"duration_ms": 500}},
			map[string]any{"timestamp": "2026-02-01T00:01:00Z", "payload": map[string]any{"duration_ms": 1000}},
		},
	}}}, nil)
	require.NoError(t, err)
	require.Empty(t, Compare("tracks", "injected", Snapshot{Data: baseline.(map[string]any)}, Snapshot{Data: actual.(map[string]any)}))

	wrongOrder := cloneSnapshot(t, Snapshot{Data: actual.(map[string]any)})
	wrongOrder.Data["tracks"].(map[string]any)["tool"].(map[string]any)["events"].([]any)[1].(map[string]any)["sequence"] = float64(0)
	differences := Compare("tracks", "injected", Snapshot{Data: baseline.(map[string]any)}, wrongOrder)
	require.NotEmpty(t, differences)
	require.Equal(t, "data.tracks.tool.events[1].sequence", differences[0].Path)
}

func TestUnsupportedBackendSkipsTrackCaseWithAllowedReport(t *testing.T) {
	limited := newInMemoryBackend(t)
	limited.Name = "limited"
	limited.Unsupported = []Unsupported{{
		Path: "tracks", Reason: "backend has no track storage",
	}}
	report, err := Run(context.Background(), []Backend{newInMemoryBackend(t), limited}, StandardCases())
	require.NoError(t, err)
	require.False(t, report.HasDisallowedDifferences(), "report: %+v", report.Differences)
	var found bool
	for _, difference := range report.Differences {
		if difference.Case == "track_events" && difference.Path == "data.tracks" {
			found = true
			require.True(t, difference.AllowedDiff)
		}
	}
	require.True(t, found)
}

func TestMemoryDifferencesUseMemoryIDInPath(t *testing.T) {
	baseline := Snapshot{SessionID: "s", Data: map[string]any{
		"memories": []any{map[string]any{"id": "memory-1", "memory": "expected"}},
	}}
	actual := Snapshot{SessionID: "s", Data: map[string]any{
		"memories": []any{map[string]any{"id": "memory-1", "memory": "actual"}},
	}}
	differences := Compare("memory", "injected", baseline, actual)
	require.Len(t, differences, 1)
	require.Equal(t, "data.memories[memory_id=memory-1].memory", differences[0].Path)
}

func TestMemoryScopeDifferencesUseMemoryIDInPath(t *testing.T) {
	baseline := Snapshot{SessionID: "s", Data: map[string]any{
		"memories": []any{map[string]any{"id": "memory-1", "scope": map[string]any{"app_name": "app", "user_id": "user-a"}}},
	}}
	actual := cloneSnapshot(t, baseline)
	actual.Data["memories"].([]any)[0].(map[string]any)["scope"].(map[string]any)["user_id"] = "user-b"

	differences := Compare("memory", "injected", baseline, actual)
	require.Len(t, differences, 1)
	require.Equal(t, "data.memories[memory_id=memory-1].scope.user_id", differences[0].Path)
}

func TestStateAndMemoryCasesPreserveFinalStateAndRetrievalOrder(t *testing.T) {
	backend := newInMemoryBackend(t)
	cases := map[string]Case{}
	for _, replayCase := range StandardCases() {
		cases[replayCase.Name] = replayCase
	}
	stateSnapshot, err := cases["state_updates"].Replay(context.Background(), backend)
	require.NoError(t, err)
	state := stateSnapshot.Data["state"].(map[string]any)
	require.Equal(t, "ZmluYWw=", state["status"])

	memorySnapshot, err := cases["memory_read_write"].Replay(context.Background(), backend)
	require.NoError(t, err)
	searches := memorySnapshot.Data["memory_search"].(map[string]any)
	results := searches["prefers"].([]any)
	require.NotEmpty(t, results)
	require.Equal(t, "prefers concise answers", results[0].(map[string]any)["memory"].(map[string]any)["memory"])
}

func TestCaptureOmitsDeclaredPrivateMetadata(t *testing.T) {
	backend := newInMemoryBackend(t)
	backend.PrivateMetadataPaths = []string{"events.*.extensions.storage_private"}
	key := session.Key{AppName: replayApp, UserID: replayUser, SessionID: "private-metadata"}
	sess, err := backend.Session.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	event := newReplayEvent("event", model.RoleUser, "hello", "")
	if event.Extensions == nil {
		event.Extensions = make(map[string]json.RawMessage)
	}
	event.Extensions["storage_private"] = json.RawMessage(`{"connection":"one"}`)
	require.NoError(t, backend.Session.AppendEvent(context.Background(), sess, event))

	snapshot, err := Capture(context.Background(), backend, key)
	require.NoError(t, err)
	extensions := snapshot.Data["events"].([]any)[0].(map[string]any)["extensions"].(map[string]any)
	require.NotContains(t, extensions, "storage_private")
}

func TestToolCasePreservesBranchTagStateAndExtension(t *testing.T) {
	backend := newInMemoryBackend(t)
	var toolCase Case
	for _, replayCase := range StandardCases() {
		if replayCase.Name == "tool_call" {
			toolCase = replayCase
			break
		}
	}
	snapshot, err := toolCase.Replay(context.Background(), backend)
	require.NoError(t, err)
	events := snapshot.Data["events"].([]any)
	require.Len(t, events, 3)
	toolResult := events[2].(map[string]any)
	require.Equal(t, "tools", toolResult["filterKey"])
	require.Equal(t, "tool-response", toolResult["tag"])
	require.Equal(t, "d2VhdGhlci1yZWFk", toolResult["stateDelta"].(map[string]any)["event_state"])
	require.Contains(t, toolResult["extensions"].(map[string]any), event.ToolCallArgsExtensionKey)
}

func TestLoadOptionalBackendsSkipsAndEnablesFromEnvironment(t *testing.T) {
	t.Setenv(EnvRedisURL, "")
	backends, skipped, err := LoadOptionalBackends(context.Background(), OptionalBackend{
		Name: "redis", Environment: EnvRedisURL,
	})
	require.NoError(t, err)
	require.Empty(t, backends)
	require.Equal(t, []string{"redis: REPLAYTEST_REDIS_URL is not set"}, skipped)

	t.Setenv(EnvRedisURL, "redis://127.0.0.1:6379")
	backends, skipped, err = LoadOptionalBackends(context.Background(), OptionalBackend{
		Name:        "redis",
		Environment: EnvRedisURL,
		Factory: func(context.Context, string) (Backend, error) {
			return newInMemoryBackend(t), nil
		},
	})
	require.NoError(t, err)
	require.Len(t, backends, 1)
	require.Equal(t, "inmemory", backends[0].Name)
	require.Empty(t, skipped)
}

func TestReportFixtureDocumentsDiffAndAllowedDiff(t *testing.T) {
	fixture := readReportFixture(t)
	var report Report
	require.NoError(t, json.Unmarshal(fixture, &report))
	require.Len(t, report.Differences, 2)
	require.False(t, report.Differences[0].AllowedDiff)
	require.True(t, report.Differences[1].AllowedDiff)
	require.NotEmpty(t, report.Differences[0].SessionID)
	require.NotEmpty(t, report.Differences[0].Path)
	generated, err := report.JSON()
	require.NoError(t, err)
	require.JSONEq(t, string(fixture), string(generated))
}

func newLightweightBackends(t *testing.T) []Backend {
	t.Helper()
	return []Backend{newInMemoryBackend(t), newSQLiteBackend(t)}
}

func newInMemoryBackend(t *testing.T) Backend {
	t.Helper()
	sessionService := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(staticSummarizer{}),
		sessinmemory.WithAsyncSummaryNum(0),
	)
	memoryService := meminmemory.NewMemoryService()
	t.Cleanup(func() {
		require.NoError(t, memoryService.Close())
		require.NoError(t, sessionService.Close())
	})
	return Backend{Name: "inmemory", Session: sessionService, Memory: memoryService}
}

func newSQLiteBackend(t *testing.T) Backend {
	t.Helper()
	sessionDB := openSQLite(t, "session.db")
	sessionService, err := sesssqlite.NewService(sessionDB,
		sesssqlite.WithSummarizer(staticSummarizer{}),
		sesssqlite.WithAsyncSummaryNum(0),
	)
	require.NoError(t, err)
	memoryDB := openSQLite(t, "memory.db")
	memoryService, err := memsqlite.NewService(memoryDB)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, memoryService.Close())
		require.NoError(t, sessionService.Close())
	})
	return Backend{Name: "sqlite", Session: sessionService, Memory: memoryService}
}

func openSQLite(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), name))
	require.NoError(t, err)
	return db
}

func readReportFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	require.NoError(t, err)
	return data
}

func cloneSnapshot(t *testing.T, source Snapshot) Snapshot {
	t.Helper()
	encoded, err := json.Marshal(source.Data)
	require.NoError(t, err)
	var data map[string]any
	require.NoError(t, json.Unmarshal(encoded, &data))
	return Snapshot{SessionID: source.SessionID, Data: data}
}
