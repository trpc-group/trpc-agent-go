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

func TestSessionSQLite_UpdateAppAndUserState_UpdateExisting(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	appName := "app"
	userKey := session.UserKey{AppName: appName, UserID: "u1"}

	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v1"),
	}))
	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v2"),
	}))
	appState, err := svc.ListAppStates(ctx, appName)
	require.NoError(t, err)
	require.Equal(t, []byte("v2"), appState["k"])

	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("u1"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("u2"),
	}))
	userState, err := svc.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	require.Equal(t, []byte("u2"), userState["k"])
}

func TestSessionSQLite_DeleteAppAndUserState_Hard(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db, WithSoftDelete(false))
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()
	appName := "app"
	userKey := session.UserKey{AppName: appName, UserID: "u1"}

	require.NoError(t, svc.UpdateAppState(ctx, appName, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	}))

	require.NoError(t, svc.DeleteAppState(ctx, appName, "k"))
	require.NoError(t, svc.DeleteUserState(ctx, userKey, "k"))

	var count int
	err = svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableAppStates,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = svc.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+svc.tableUserStates,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestSessionSQLite_DeleteState_ValidationErrors(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	ctx := context.Background()

	err = svc.DeleteAppState(ctx, "", "k")
	require.ErrorIs(t, err, session.ErrAppNameRequired)

	err = svc.DeleteAppState(ctx, "app", "")
	require.Error(t, err)

	err = svc.DeleteUserState(ctx, session.UserKey{}, "k")
	require.Error(t, err)

	err = svc.DeleteUserState(
		ctx,
		session.UserKey{AppName: "app", UserID: "u1"},
		"",
	)
	require.Error(t, err)
}
