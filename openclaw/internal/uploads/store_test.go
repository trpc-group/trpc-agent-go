//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package uploads

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreSave(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	saved, err := store.Save(context.Background(), Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:abc",
	}, "../李光耀回忆录.pdf", []byte("pdf-bytes"))
	require.NoError(t, err)

	require.NotEmpty(t, saved.Path)
	require.Equal(t, HostRef(saved.Path), saved.HostRef)

	data, err := os.ReadFile(saved.Path)
	require.NoError(t, err)
	require.Equal(t, []byte("pdf-bytes"), data)

	require.Contains(t, saved.Name, "李光耀回忆录.pdf")
	require.NotContains(t, saved.Name, "..")
}

func TestStoreSave_Deduplicates(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	scope := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	first, err := store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("same"),
	)
	require.NoError(t, err)

	second, err := store.Save(
		context.Background(),
		scope,
		"report.pdf",
		[]byte("same"),
	)
	require.NoError(t, err)

	require.Equal(t, first.Path, second.Path)
}

func TestStoreDeleteUser(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root)
	require.NoError(t, err)

	saved, err := store.Save(context.Background(), Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}, "report.pdf", []byte("x"))
	require.NoError(t, err)

	require.NoError(t, store.DeleteUser(
		context.Background(),
		"telegram",
		"u1",
	))

	_, err = os.Stat(filepath.Dir(saved.Path))
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestPathFromHostRef(t *testing.T) {
	t.Parallel()

	absPath := filepath.Join(t.TempDir(), "report.pdf")

	path, ok := PathFromHostRef(HostRef(absPath))
	require.True(t, ok)
	require.Equal(t, absPath, path)

	path, ok = PathFromHostRef(absPath)
	require.True(t, ok)
	require.Equal(t, absPath, path)

	_, ok = PathFromHostRef("file-123")
	require.False(t, ok)
}
