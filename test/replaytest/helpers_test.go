//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest_test

import (
	"context"
	"database/sql"
	"fmt"
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
	replaytest "trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	replayApp  = "replaytest"
	replayUser = "user"

	EnvRedisURL      = replaytest.EnvRedisURL
	EnvPostgresDSN   = replaytest.EnvPostgresDSN
	EnvMySQLDSN      = replaytest.EnvMySQLDSN
	EnvClickHouseDSN = replaytest.EnvClickHouseDSN
)

type (
	Backend         = replaytest.Backend
	OptionalBackend = replaytest.OptionalBackend
)

var (
	LoadOptionalBackends = replaytest.LoadOptionalBackends
	Run                  = replaytest.Run
	StandardCases        = replaytest.StandardCases
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

type manualReplaySummarizer struct{ staticSummarizer }

func (manualReplaySummarizer) ShouldSummarize(*session.Session) bool { return false }

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

func cleanupReplayBackend(t *testing.T, backend Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, replayCase := range StandardCases() {
		if err := backend.Session.DeleteSession(ctx, session.Key{
			AppName: replayApp, UserID: replayUser, SessionID: replayCase.Name,
		}); err != nil {
			t.Errorf("delete %s replay session %q: %v", backend.Name, replayCase.Name, err)
		}
	}
	if err := backend.Memory.ClearMemories(ctx, memory.UserKey{AppName: replayApp, UserID: replayUser}); err != nil {
		t.Errorf("clear %s replay memories: %v", backend.Name, err)
	}
	if err := backend.Memory.Close(); err != nil {
		t.Errorf("close %s replay memory: %v", backend.Name, err)
	}
	if err := backend.Session.Close(); err != nil {
		t.Errorf("close %s replay session: %v", backend.Name, err)
	}
}

func cleanupReplaySession(t *testing.T, backend Backend, key session.Key) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := backend.Session.DeleteSession(ctx, key); err != nil {
		t.Errorf("delete %s replay session %q: %v", backend.Name, key.SessionID, err)
	}
}

func newReplayEvent(id string, role model.Role, content, filterKey string) *event.Event {
	response := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role:    role,
		Content: content,
	}}}}
	replayEvent := event.NewResponseEvent("replay-invocation", "replay-agent", response, event.WithBranch(filterKey))
	replayEvent.ID = id
	replayEvent.Timestamp = time.Now().UTC()
	replayEvent.FilterKey = filterKey
	return replayEvent
}
