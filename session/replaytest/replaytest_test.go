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

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
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
	return fmt.Sprintf("replay summary after %d events", sess.GetEventCount()), nil
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

	got, err := report.JSON()
	require.NoError(t, err)
	want := readReportFixture(t)
	require.JSONEq(t, string(want), string(got))
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
}

func TestRunValidatesBackends(t *testing.T) {
	_, err := Run(context.Background(), []Backend{{Name: "only"}}, StandardCases())
	require.EqualError(t, err, "replay requires at least two backends")
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
