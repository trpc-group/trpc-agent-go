//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package persona

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookup(t *testing.T) {
	t.Parallel()

	preset, ok := Lookup(" gf ")
	require.True(t, ok)
	require.Equal(t, PresetGirlfriend, preset.ID)

	preset, ok = Lookup("")
	require.True(t, ok)
	require.Equal(t, PresetDefault, preset.ID)

	_, ok = Lookup("missing")
	require.False(t, ok)
}

func TestScopeKeyFromSession(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		DMScopeKey("telegram", "u1"),
		ScopeKeyFromSession(
			"telegram",
			"u1",
			"telegram:dm:u1:rotated",
		),
	)
	require.Equal(
		t,
		ThreadScopeKey("telegram", "100:topic:7"),
		ScopeKeyFromSession(
			"telegram",
			"u1",
			"telegram:thread:100:topic:7",
		),
	)
	require.Equal(
		t,
		DMScopeKey("telegram", "u2"),
		ScopeKeyFromSession("telegram", "u2", ""),
	)
}

func TestStoreSetGetAndPersist(t *testing.T) {
	t.Parallel()

	path, err := DefaultStorePath(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(path)
	require.NoError(t, err)

	scopeKey := DMScopeKey("telegram", "u1")
	preset, err := store.Set(
		context.Background(),
		scopeKey,
		PresetCoach,
	)
	require.NoError(t, err)
	require.Equal(t, PresetCoach, preset.ID)

	got, err := store.Get(scopeKey)
	require.NoError(t, err)
	require.Equal(t, PresetCoach, got.ID)

	store2, err := NewStore(path)
	require.NoError(t, err)

	got, err = store2.Get(scopeKey)
	require.NoError(t, err)
	require.Equal(t, PresetCoach, got.ID)
}

func TestStoreSetDefaultClearsScope(t *testing.T) {
	t.Parallel()

	path, err := DefaultStorePath(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(path)
	require.NoError(t, err)

	scopeKey := DMScopeKey("telegram", "u1")
	_, err = store.Set(context.Background(), scopeKey, PresetCreative)
	require.NoError(t, err)

	preset, err := store.Set(context.Background(), scopeKey, "reset")
	require.NoError(t, err)
	require.Equal(t, PresetDefault, preset.ID)

	got, err := store.Get(scopeKey)
	require.NoError(t, err)
	require.Equal(t, PresetDefault, got.ID)
}

func TestStoreForgetUser(t *testing.T) {
	t.Parallel()

	path, err := DefaultStorePath(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(path)
	require.NoError(t, err)

	_, err = store.Set(
		context.Background(),
		DMScopeKey("telegram", "u1"),
		PresetGirlfriend,
	)
	require.NoError(t, err)
	_, err = store.Set(
		context.Background(),
		ThreadScopeKey("telegram", "100"),
		PresetCoach,
	)
	require.NoError(t, err)

	require.NoError(
		t,
		store.ForgetUser(context.Background(), "telegram", "u1"),
	)

	got, err := store.Get(DMScopeKey("telegram", "u1"))
	require.NoError(t, err)
	require.Equal(t, PresetDefault, got.ID)

	got, err = store.Get(ThreadScopeKey("telegram", "100"))
	require.NoError(t, err)
	require.Equal(t, PresetCoach, got.ID)
}

func TestStoreSetUnknownPreset(t *testing.T) {
	t.Parallel()

	path, err := DefaultStorePath(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(path)
	require.NoError(t, err)

	_, err = store.Set(
		context.Background(),
		DMScopeKey("telegram", "u1"),
		"missing",
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnknownPreset))
}
