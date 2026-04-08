//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryfile

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultRootUsesMemoryDir(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	root, err := DefaultRoot(stateDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(stateDir, rootDirName), root)
}

func TestDefaultRoot_EmptyStateDirReturnsError(t *testing.T) {
	t.Parallel()

	_, err := DefaultRoot(" ")
	require.Error(t, err)
}

func TestNewStore_EmptyRootReturnsError(t *testing.T) {
	t.Parallel()

	_, err := NewStore(" ")
	require.Error(t, err)
}

func TestStoreRoot_ReturnsConfiguredRootAndHandlesNilStore(t *testing.T) {
	t.Parallel()

	var nilStore *Store
	require.Empty(t, nilStore.Root())

	root := filepath.Join(t.TempDir(), "memory")
	store, err := NewStore(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(root), store.Root())
}

func TestStoreEnsureMemoryCreatesTemplate(t *testing.T) {
	t.Parallel()

	const appName = "demo-app"

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.EnsureMemory(
		context.Background(),
		appName,
		"u1",
	)
	require.NoError(t, err)
	require.FileExists(t, path)

	text, err := store.ReadFile(path, 0)
	require.NoError(t, err)
	require.Contains(t, text, "# Memory")
	require.Contains(t, text, "user-owned file")
	require.Contains(t, text, "remember this")
	require.Contains(t, text, "## Preferences")
	require.Equal(
		t,
		filepath.Join(
			root,
			base64.RawURLEncoding.EncodeToString([]byte(appName)),
			base64.RawURLEncoding.EncodeToString([]byte("u1")),
			memoryFileName,
		),
		path,
	)
}

func TestStoreReadFileHonorsLimit(t *testing.T) {
	t.Parallel()

	const appName = "demo-app"

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath(appName, "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(
		t,
		os.WriteFile(path, []byte("0123456789"), filePerm),
	)

	text, err := store.ReadFile(path, 4)
	require.NoError(t, err)
	require.Equal(t, "0123", text)
}

func TestStoreReadFile_NilStoreReturnsError(t *testing.T) {
	t.Parallel()

	var store *Store
	_, err := store.ReadFile("/tmp/memory.md", 0)
	require.Error(t, err)
}

func TestStoreReadFile_EmptyPathReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.ReadFile(" ", 0)
	require.Error(t, err)
}

func TestStoreReadFile_RejectsPathOutsideRoot(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	outsidePath := filepath.Join(t.TempDir(), memoryFileName)
	require.NoError(
		t,
		os.WriteFile(outsidePath, []byte("outside"), filePerm),
	)

	_, err = store.ReadFile(outsidePath, 0)
	require.Error(t, err)
	require.EqualError(t, err, "memoryfile: path outside store root")
}

func TestStoreReadFile_MissingFileReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("demo-app", "u1")
	require.NoError(t, err)

	_, err = store.ReadFile(path, 0)
	require.Error(t, err)
}

func TestStoreDeleteUser(t *testing.T) {
	t.Parallel()

	const appName = "demo-app"

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.EnsureMemory(
		context.Background(),
		appName,
		"u1",
	)
	require.NoError(t, err)

	require.NoError(
		t,
		store.DeleteUser(context.Background(), appName, "u1"),
	)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStoreDeleteUser_NilStoreIsNoop(t *testing.T) {
	t.Parallel()

	var store *Store
	require.NoError(
		t,
		store.DeleteUser(context.Background(), "demo-app", "u1"),
	)
}

func TestStoreDeleteUser_CanceledContextReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = store.DeleteUser(ctx, "demo-app", "u1")
	require.ErrorIs(t, err, context.Canceled)
}

func TestStoreDeleteUser_EmptyScopeReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	err = store.DeleteUser(context.Background(), " ", "u1")
	require.Error(t, err)
}

func TestStoreRemoveScopedDir_EmptyDirIsNoop(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	require.NoError(t, store.removeScopedDir(context.Background(), " "))
}

func TestStoreRemoveScopedDir_CanceledContextReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = store.removeScopedDir(ctx, filepath.Join(root, "demo-app", "u1"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestMemoryPathUsesLosslessScopeEncoding(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)

	store, err := NewStore(root)
	require.NoError(t, err)

	pathOne, err := store.MemoryPath("Demo", "User_A")
	require.NoError(t, err)
	pathTwo, err := store.MemoryPath("demo", "user-a")
	require.NoError(t, err)
	require.NotEqual(t, pathOne, pathTwo)

	require.Contains(
		t,
		pathOne,
		base64.RawURLEncoding.EncodeToString([]byte("Demo")),
	)
	require.Contains(
		t,
		pathOne,
		base64.RawURLEncoding.EncodeToString([]byte("User_A")),
	)
}

func TestMemoryDir_EmptyScopeReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.MemoryDir(" ", "u1")
	require.Error(t, err)

	_, err = store.MemoryDir("demo-app", " ")
	require.Error(t, err)
}

func TestMemoryDir_NilStoreReturnsError(t *testing.T) {
	t.Parallel()

	var store *Store
	_, err := store.MemoryDir("demo-app", "u1")
	require.Error(t, err)
}

func TestMemoryPath_NilStoreReturnsError(t *testing.T) {
	t.Parallel()

	var store *Store
	_, err := store.MemoryPath("demo-app", "u1")
	require.Error(t, err)
}

func TestEnsureMemory_CanceledContextReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = store.EnsureMemory(ctx, "demo-app", "u1")
	require.ErrorIs(t, err, context.Canceled)
}

func TestEnsureMemory_EmptyScopeReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.EnsureMemory(context.Background(), " ", "u1")
	require.Error(t, err)
}

func TestEnsureMemory_ExistingFileReturnsPath(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("demo-app", "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte("## Preferences\n\n- Keep existing memory."),
			filePerm,
		),
	)

	got, err := store.EnsureMemory(context.Background(), "demo-app", "u1")
	require.NoError(t, err)
	require.Equal(t, path, got)

	text, err := store.ReadFile(path, 0)
	require.NoError(t, err)
	require.Contains(t, text, "Keep existing memory")
}

func TestEnsureMemory_WriteFileErrorReturnsError(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "memory-root")
	require.NoError(t, os.WriteFile(root, []byte("x"), filePerm))
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.EnsureMemory(context.Background(), "demo-app", "u1")
	require.Error(t, err)
}

func TestStoreUpdateMemoryCreatesAndWrites(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.UpdateMemory(
		context.Background(),
		"demo-app",
		"u1",
		func(current string) (string, error) {
			require.Contains(t, current, "# Memory")
			return current + "\n- Added fact\n", nil
		},
	)
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "- Added fact")
}

func TestStoreUpdateMemoryUpdatesExistingFile(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("demo-app", "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(t, os.WriteFile(path, []byte("original"), filePerm))

	got, err := store.UpdateMemory(
		context.Background(),
		"demo-app",
		"u1",
		func(current string) (string, error) {
			require.Equal(t, "original", current)
			return "updated", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, path, got)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "updated", string(raw))
}

func TestStoreUpdateMemory_NilStoreReturnsError(t *testing.T) {
	t.Parallel()

	var store *Store
	_, err := store.UpdateMemory(
		context.Background(),
		"demo-app",
		"u1",
		func(current string) (string, error) {
			return current, nil
		},
	)
	require.Error(t, err)
}

func TestStoreUpdateMemory_NilUpdateReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.UpdateMemory(context.Background(), "demo-app", "u1", nil)
	require.Error(t, err)
}

func TestStoreUpdateMemory_CanceledContextReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = store.UpdateMemory(
		ctx,
		"demo-app",
		"u1",
		func(current string) (string, error) {
			return current, nil
		},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestStoreUpdateMemory_EmptyScopeReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	_, err = store.UpdateMemory(
		context.Background(),
		" ",
		"u1",
		func(current string) (string, error) {
			return current, nil
		},
	)
	require.Error(t, err)
}

func TestStoreUpdateMemory_UpdateFuncErrorKeepsOriginalFile(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("demo-app", "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(t, os.WriteFile(path, []byte("original"), filePerm))

	updateErr := errors.New("boom")
	_, err = store.UpdateMemory(
		context.Background(),
		"demo-app",
		"u1",
		func(current string) (string, error) {
			require.Equal(t, "original", current)
			return "", updateErr
		},
	)
	require.ErrorIs(t, err, updateErr)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(raw))
}

func TestStoreUpdateMemory_CanceledAfterUpdateKeepsOriginalFile(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("demo-app", "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(t, os.WriteFile(path, []byte("original"), filePerm))

	ctx, cancel := context.WithCancel(context.Background())
	_, err = store.UpdateMemory(
		ctx,
		"demo-app",
		"u1",
		func(current string) (string, error) {
			cancel()
			return "updated", nil
		},
	)
	require.ErrorIs(t, err, context.Canceled)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(raw))
}

func TestStoreUpdateMemory_WriteErrorReturnsError(t *testing.T) {
	t.Parallel()

	root, err := DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := NewStore(root)
	require.NoError(t, err)

	path, err := store.MemoryPath("demo-app", "u1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	require.NoError(t, os.WriteFile(path, []byte("original"), filePerm))

	_, err = store.UpdateMemory(
		context.Background(),
		"demo-app",
		"u1",
		func(current string) (string, error) {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, dirPerm))
			return "updated", nil
		},
	)
	require.Error(t, err)
}

func TestWriteFileAtomic_EmptyPathReturnsError(t *testing.T) {
	t.Parallel()

	err := writeFileAtomic(" ", []byte("demo"))
	require.Error(t, err)
}

func TestFileExists_EmptyPathIsFalse(t *testing.T) {
	t.Parallel()

	require.False(t, fileExists(" "))
}

func TestSanitizePathPart_WhitespaceOnlyIsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, sanitizePathPart(" \t\n "))
}

func TestBuildContextText(t *testing.T) {
	t.Parallel()

	text := BuildContextText("- prefers concise replies")
	require.Contains(t, text, "user-owned file MEMORY.md")
	require.Contains(t, text, "not hidden internal state")
	require.Contains(t, text, "prefers concise replies")
}

func TestBuildContextText_EmptyReturnsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, BuildContextText(" \n "))
}

func TestContextErr_NilContextReturnsNil(t *testing.T) {
	t.Parallel()

	require.NoError(t, contextErr(nil))
}
