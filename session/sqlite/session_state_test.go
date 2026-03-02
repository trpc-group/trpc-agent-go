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
	"testing"

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
