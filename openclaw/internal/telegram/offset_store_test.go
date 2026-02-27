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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileOffsetStore_Read_Missing(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	offset, ok, err := store.Read(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 0, offset)
}

func TestFileOffsetStore_WriteThenRead(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	require.NoError(t, store.Write(context.Background(), 123))

	offset, ok, err := store.Read(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 123, offset)
}

func TestFileOffsetStore_Read_InvalidJSON(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	require.NoError(t, os.WriteFile(p, []byte("{"), 0o600))

	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	_, _, err = store.Read(context.Background())
	require.Error(t, err)
}

func TestFileOffsetStore_Read_UnexpectedVersion(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	require.NoError(
		t,
		os.WriteFile(
			p,
			[]byte("{\"version\":999,\"offset\":1}"),
			0o600,
		),
	)

	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	_, _, err = store.Read(context.Background())
	require.Error(t, err)
}

func TestFileOffsetStore_Write_NegativeOffset(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	require.Error(t, store.Write(context.Background(), -1))
}

func TestNewFileOffsetStore_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := NewFileOffsetStore("")
	require.Error(t, err)
}

func TestFileOffsetStore_Read_NegativeOffset(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	require.NoError(
		t,
		os.WriteFile(
			p,
			[]byte("{\"version\":1,\"offset\":-1}"),
			0o600,
		),
	)

	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	_, _, err = store.Read(context.Background())
	require.Error(t, err)
}

func TestFileOffsetStore_Read_FileReadError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "offset.json")
	require.NoError(t, os.Mkdir(p, 0o700))

	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	_, _, err = store.Read(context.Background())
	require.Error(t, err)
}

func TestFileOffsetStore_Read_ContextCanceled(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = store.Read(ctx)
	require.Error(t, err)
}

func TestFileOffsetStore_Write_ContextCanceled(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, store.Write(ctx, 1))
}

func TestFileOffsetStore_Write_DirIsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	parent := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o600))

	p := filepath.Join(parent, "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	require.Error(t, store.Write(context.Background(), 1))
}
