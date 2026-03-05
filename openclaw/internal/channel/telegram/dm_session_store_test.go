//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewDMSessionStore_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := newDMSessionStore("")
	require.Error(t, err)
}

func TestDMSessionStorePath_EmptyStateDir(t *testing.T) {
	t.Parallel()

	_, err := dmSessionStorePath("", BotInfo{Username: "bot"})
	require.Error(t, err)
}

func TestDMSessionStore_RotatePersists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)

	store, err := newDMSessionStore(path)
	require.NoError(t, err)

	legacy := buildLaneKey("2", "")
	sessionID, rotated, err := store.EnsureActiveSession(
		context.Background(),
		"2",
		legacy,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.Equal(t, legacy, sessionID)

	resetID, err := store.Rotate(context.Background(), "2", legacy)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(resetID, legacy+":"))

	store2, err := newDMSessionStore(path)
	require.NoError(t, err)

	got, rotated, err := store2.EnsureActiveSession(
		context.Background(),
		"2",
		legacy,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.Equal(t, resetID, got)

	forgot, err := store2.ForgetUser(context.Background(), "2")
	require.NoError(t, err)
	require.True(t, forgot)

	store3, err := newDMSessionStore(path)
	require.NoError(t, err)

	got, rotated, err = store3.EnsureActiveSession(
		context.Background(),
		"2",
		legacy,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.Equal(t, legacy, got)
}

func TestDMSessionStore_Load_RejectsVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"version":2}`), 0o600))

	_, err = newDMSessionStore(path)
	require.Error(t, err)
}

func TestDMSessionStore_AutoReset_IdleAndDaily(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)

	store, err := newDMSessionStore(path)
	require.NoError(t, err)

	base := time.Date(2026, 3, 4, 10, 0, 0, 0, time.Local)
	store.now = func() time.Time { return base }

	legacy := buildLaneKey("2", "")
	sid, rotated, err := store.EnsureActiveSession(
		context.Background(),
		"2",
		legacy,
		dmSessionResetPolicy{
			Idle:  1 * time.Second,
			Daily: true,
		},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.Equal(t, legacy, sid)

	store.now = func() time.Time { return base.Add(2 * time.Second) }
	sid, rotated, err = store.EnsureActiveSession(
		context.Background(),
		"2",
		legacy,
		dmSessionResetPolicy{Idle: 1 * time.Second},
	)
	require.NoError(t, err)
	require.True(t, rotated)
	require.True(t, strings.HasPrefix(sid, legacy+":"))

	nextDay := time.Date(2026, 3, 5, 10, 0, 0, 0, time.Local)
	store.now = func() time.Time { return nextDay }
	sid, rotated, err = store.EnsureActiveSession(
		context.Background(),
		"2",
		legacy,
		dmSessionResetPolicy{Daily: true},
	)
	require.NoError(t, err)
	require.True(t, rotated)
	require.True(t, strings.HasPrefix(sid, legacy+":"))
}

func TestDMSessionStore_EnsureActiveSession_ValidationErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)

	store, err := newDMSessionStore(path)
	require.NoError(t, err)

	_, _, err = store.EnsureActiveSession(
		context.Background(),
		"",
		"sid",
		dmSessionResetPolicy{},
	)
	require.Error(t, err)

	_, _, err = store.EnsureActiveSession(
		context.Background(),
		"u1",
		"",
		dmSessionResetPolicy{},
	)
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = store.EnsureActiveSession(
		ctx,
		"u1",
		"sid",
		dmSessionResetPolicy{},
	)
	require.Error(t, err)
}

func TestDMSessionStore_ForgetUser_EmptyAndMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := dmSessionStorePath(dir, BotInfo{Username: "bot"})
	require.NoError(t, err)

	store, err := newDMSessionStore(path)
	require.NoError(t, err)

	forgot, err := store.ForgetUser(context.Background(), "u1")
	require.NoError(t, err)
	require.False(t, forgot)

	legacy := buildLaneKey("u2", "")
	_, _, err = store.EnsureActiveSession(
		context.Background(),
		"u2",
		legacy,
		dmSessionResetPolicy{Daily: true},
	)
	require.NoError(t, err)

	forgot, err = store.ForgetUser(context.Background(), "missing")
	require.NoError(t, err)
	require.False(t, forgot)
}
