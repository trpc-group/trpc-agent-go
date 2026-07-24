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
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

func TestReplayConsistencySQLiteBackend(t *testing.T) {
	ctx := context.Background()
	report, err := replaytest.Run(ctx, replaytest.PublicCases(), []replaytest.Backend{
		replaytest.NewInMemoryBackend(),
		newSQLiteReplayBackend(t),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if replaytest.HasBlockingDiff(report) {
		data, _ := replaytest.MarshalReport(report)
		t.Fatalf("sqlite replay consistency diff:\n%s", data)
	}
}

func newSQLiteReplayBackend(t *testing.T) replaytest.Backend {
	t.Helper()
	return replaytest.NewServiceBackend(
		"session/sqlite+memory/sqlite",
		func(ctx context.Context, c replaytest.ReplayCase) (*replaytest.ServiceBundle, error) {
			sessionDB, err := openReplaySQLiteDB(filepath.Join(t.TempDir(), c.Name+"-session.db"))
			if err != nil {
				return nil, err
			}
			memoryDB, err := openReplaySQLiteDB(filepath.Join(t.TempDir(), c.Name+"-memory.db"))
			if err != nil {
				_ = sessionDB.Close()
				return nil, err
			}
			sessionSvc, err := sesssqlite.NewService(
				sessionDB,
				sesssqlite.WithSummarizer(replaytest.NewDeterministicSummarizer()),
				sesssqlite.WithEnableAsyncPersist(false),
			)
			if err != nil {
				_ = sessionDB.Close()
				_ = memoryDB.Close()
				return nil, err
			}
			memorySvc, err := memsqlite.NewService(
				memoryDB,
				memsqlite.WithMaxResults(100),
				memsqlite.WithMinSearchScore(0),
			)
			if err != nil {
				_ = sessionSvc.Close()
				_ = memoryDB.Close()
				return nil, err
			}
			return &replaytest.ServiceBundle{
				SessionService: sessionSvc,
				MemoryService:  memorySvc,
				TrackService:   sessionSvc,
				DeleteSessionState: func(ctx context.Context, key session.Key, stateKey string) error {
					return deleteSQLiteReplaySessionState(ctx, sessionDB, key, stateKey)
				},
				ClearSessionState: func(ctx context.Context, key session.Key) error {
					return clearSQLiteReplaySessionState(ctx, sessionDB, key)
				},
				TTLProbe: func(ctx context.Context) error {
					ttlDB, err := openReplaySQLiteDB(filepath.Join(t.TempDir(), c.Name+"-ttl.db"))
					if err != nil {
						return err
					}
					ttlSvc, err := sesssqlite.NewService(
						ttlDB,
						sesssqlite.WithSessionTTL(100*time.Millisecond),
						sesssqlite.WithEnableAsyncPersist(false),
					)
					if err != nil {
						_ = ttlDB.Close()
						return err
					}
					defer ttlSvc.Close()
					key := c.Key
					key.SessionID += "-ttl-probe"
					return replaytest.ProbeSessionTTLExpiration(ctx, ttlSvc, key, 220*time.Millisecond)
				},
				Close: func() error {
					sessErr := sessionSvc.Close()
					memErr := memorySvc.Close()
					if sessErr != nil {
						return sessErr
					}
					return memErr
				},
			}, nil
		},
		replaytest.WithSupportedCapabilities(
			replaytest.CapabilityMemorySearch,
			replaytest.CapabilityTTL,
			replaytest.CapabilityTrack,
			replaytest.CapabilityStateDelete,
			replaytest.CapabilityStateClear,
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityEventPage,
			"session/sqlite GetSession returns ErrEventPageUnsupported for strict event pages",
		),
	)
}

func TestReplayConsistencySQLiteStateDeleteClearSupported(t *testing.T) {
	cases := replaytest.PublicCases()
	var stateCase replaytest.ReplayCase
	for _, c := range cases {
		if c.Name == "11_state_delete_clear_semantics" {
			stateCase = c
			break
		}
	}
	if stateCase.Name == "" {
		t.Fatalf("state delete/clear replay case not found")
	}
	snapshot, err := newSQLiteReplayBackend(t).Apply(context.Background(), stateCase)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	for _, feature := range snapshot.Unsupported {
		if feature.Capability == replaytest.CapabilityStateDelete ||
			feature.Capability == replaytest.CapabilityStateClear {
			t.Fatalf("sqlite should support state delete/clear replay: %+v", snapshot.Unsupported)
		}
	}
	got := snapshot.State["locale"]
	if got.Kind != "value" || got.Value != `"en-US"` || len(snapshot.State) != 1 {
		t.Fatalf("sqlite final state = %+v, want only locale=en-US", snapshot.State)
	}
}

func openReplaySQLiteDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

type sqliteReplaySessionState struct {
	ID        string           `json:"id"`
	State     session.StateMap `json:"state"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

func deleteSQLiteReplaySessionState(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	stateKey string,
) error {
	return updateSQLiteReplaySessionState(ctx, db, key, func(state session.StateMap) {
		delete(state, stateKey)
	})
}

func clearSQLiteReplaySessionState(ctx context.Context, db *sql.DB, key session.Key) error {
	return updateSQLiteReplaySessionState(ctx, db, key, func(state session.StateMap) {
		for k := range state {
			delete(state, k)
		}
	})
}

func updateSQLiteReplaySessionState(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	mutate func(session.StateMap),
) error {
	var raw []byte
	err := db.QueryRowContext(
		ctx,
		`SELECT state FROM session_states
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		key.AppName,
		key.UserID,
		key.SessionID,
	).Scan(&raw)
	if err != nil {
		return err
	}
	var state sqliteReplaySessionState
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &state); err != nil {
			return err
		}
	}
	if state.State == nil {
		state.State = make(session.StateMap)
	}
	mutate(state.State)
	state.UpdatedAt = time.Now().UTC()
	updated, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`UPDATE session_states SET state = ?, updated_at = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		updated,
		state.UpdatedAt.UnixNano(),
		key.AppName,
		key.UserID,
		key.SessionID,
	)
	return err
}
