//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inprocess

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileStoreRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "runs.json")
	store, err := NewFileStore(path)
	require.NoError(t, err)

	now := time.Now()
	runs := []Run{{
		ID:              "run-1",
		OwnerUserID:     "user-a",
		ParentSessionID: "parent-a",
		Status:          StatusCompleted,
		Metadata: map[string]string{
			"kind": "review",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}}
	require.NoError(t, store.Save(context.Background(), runs))

	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, "user-a", loaded[0].OwnerUserID)
	require.Equal(t, "review", loaded[0].Metadata["kind"])
	require.NotSame(t, &runs[0].Metadata, &loaded[0].Metadata)
}

func TestFileStoreErrors(t *testing.T) {
	t.Parallel()

	_, err := NewFileStore("")
	require.ErrorContains(t, err, "empty store path")

	path := filepath.Join(t.TempDir(), "runs.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":999}`), 0o600))
	store, err := NewFileStore(path)
	require.NoError(t, err)
	_, err = store.Load(context.Background())
	require.ErrorContains(t, err, "unsupported store version")

	dir := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(dir, []byte("x"), 0o600))
	store, err = NewFileStore(filepath.Join(dir, "runs.json"))
	require.NoError(t, err)
	require.Error(t, store.Save(context.Background(), nil))

	missing, err := NewFileStore(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err)
	loaded, err := missing.Load(context.Background())
	require.NoError(t, err)
	require.Nil(t, loaded)

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte(`{bad`), 0o600))
	store, err = NewFileStore(invalid)
	require.NoError(t, err)
	_, err = store.Load(context.Background())
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.Load(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, store.Save(ctx, nil), context.Canceled)
}

func TestMemoryStoreAndHelpers(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	now := time.Now()
	runs := []Run{{
		ID:        "run-1",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}}
	require.NoError(t, store.Save(context.Background(), runs))
	runs[0].Status = StatusFailed

	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, StatusQueued, loaded[0].Status)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, store.Save(ctx, nil), context.Canceled)
	_, err = store.Load(ctx)
	require.ErrorIs(t, err, context.Canceled)

	require.Equal(t, "trimmed", summarizeText(" trimmed ", 0))
	require.Equal(t, "ab", summarizeText("abcd", 2))
	require.Equal(t, "abc", summarizeText("abc", 8))

	var nilMemory *MemoryStore
	loaded, err = nilMemory.Load(context.Background())
	require.NoError(t, err)
	require.Nil(t, loaded)
	require.NoError(t, nilMemory.Save(context.Background(), nil))

	var nilFile *FileStore
	loaded, err = nilFile.Load(context.Background())
	require.NoError(t, err)
	require.Nil(t, loaded)
	require.NoError(t, nilFile.Save(context.Background(), nil))
}
