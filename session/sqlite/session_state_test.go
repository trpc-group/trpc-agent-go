//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSQLite_UpdateSessionState_ValidationErrors(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	})
	require.Error(t, err)

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	})
	require.Error(t, err)
}

func TestSessionSQLite_UpdateSessionState_SessionNotFound(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}

	err = svc.UpdateSessionState(ctx, key, session.StateMap{
		"k": []byte("v"),
	})
	require.Error(t, err)
}

func TestSessionSQLite_UpdateSessionState_PreservesTracksAfterAppend(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}))
	require.NoError(t, svc.UpdateSessionState(ctx, key, session.StateMap{
		"marker": []byte("1"),
	}))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, []byte("1"), got.State["marker"])

	tracks, err := session.TracksFromState(got.State)
	require.NoError(t, err)
	require.Contains(t, tracks, session.Track("alpha"))
}
