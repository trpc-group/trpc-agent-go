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
	"time"

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

func TestStoreListScopeAndListAll(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	scopeA := Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "s1",
	}
	scopeB := Scope{
		Channel:   "telegram",
		UserID:    "u2",
		SessionID: "s2",
	}

	first, err := store.Save(
		context.Background(),
		scopeA,
		"report.pdf",
		[]byte("a"),
	)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	second, err := store.Save(
		context.Background(),
		scopeB,
		"clip.mp4",
		[]byte("bb"),
	)
	require.NoError(t, err)

	scopeFiles, err := store.ListScope(scopeA, 10)
	require.NoError(t, err)
	require.Len(t, scopeFiles, 1)
	require.Equal(t, first.Name, scopeFiles[0].Name)
	require.Equal(t, first.Path, scopeFiles[0].Path)
	require.Equal(t, scopeA, scopeFiles[0].Scope)
	require.Contains(
		t,
		scopeFiles[0].RelativePath,
		"telegram/u1/s1/",
	)

	allFiles, err := store.ListAll(10)
	require.NoError(t, err)
	require.Len(t, allFiles, 2)
	require.Equal(t, second.Path, allFiles[0].Path)
	require.Equal(t, scopeB, allFiles[0].Scope)
	require.Equal(t, first.Path, allFiles[1].Path)
}
