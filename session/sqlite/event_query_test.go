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
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSQLite_GetEventsList_EmptyInput(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	out, err := svc.getEventsList(
		context.Background(),
		nil,
		nil,
		0,
		time.Time{},
	)
	require.NoError(t, err)
	require.Nil(t, out)
}

func TestSessionSQLite_GetTrackEvents_EmptyInput(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	out, err := svc.getTrackEvents(
		context.Background(),
		nil,
		nil,
		0,
		time.Time{},
	)
	require.NoError(t, err)
	require.Nil(t, out)
}

func TestSessionSQLite_GetTrackEvents_Mismatch_Error(t *testing.T) {
	db, _, cleanup := openTempSQLiteDB(t)
	defer cleanup()

	svc, err := NewService(db)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	_, err = svc.getTrackEvents(
		context.Background(),
		[]session.Key{{AppName: "app", UserID: "u1", SessionID: "s1"}},
		nil,
		0,
		time.Time{},
	)
	require.Error(t, err)
}
