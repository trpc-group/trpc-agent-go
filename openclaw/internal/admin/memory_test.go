//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
)

type memoryStoreStub struct {
	root      string
	readCount *atomic.Int32
}

func (s memoryStoreStub) Root() string {
	return s.root
}

func (s memoryStoreStub) ReadFile(string, int) (string, error) {
	if s.readCount != nil {
		s.readCount.Add(1)
	}
	return "", nil
}

func TestMemoryStatusWithFiles_ReportsStoreErrors(t *testing.T) {
	t.Parallel()

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   memoryStoreStub{root: " "},
	})

	status := svc.memoryStatusSummary()
	require.True(t, status.Enabled)
	require.True(t, status.FileEnabled)
	require.Equal(t, "file", status.Backend)
	require.Contains(t, status.Error, "memory file root is not configured")
	require.Empty(t, status.Files)
}

func TestMemoryStatusWithFiles_NilServiceAndNoStore(t *testing.T) {
	t.Parallel()

	var svc *Service
	require.Equal(t, memoryStatus{}, svc.memoryStatus())

	svc = New(Config{MemoryBackend: "file"})
	status := svc.memoryStatus()
	require.True(t, status.Enabled)
	require.Equal(t, "file", status.Backend)
	require.False(t, status.FileEnabled)
	require.Empty(t, status.Files)
}

func TestMemoryStatusSummary_DoesNotReadMemoryFilePreview(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	app := base64.RawURLEncoding.EncodeToString([]byte("openclaw"))
	user := base64.RawURLEncoding.EncodeToString([]byte("alice"))
	dir := filepath.Join(root, app, user)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, memoryFileName),
			[]byte("# Memory\n\n- Alice prefers concise updates.\n"),
			0o600,
		),
	)

	var reads atomic.Int32
	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   memoryStoreStub{root: root, readCount: &reads},
	})

	status := svc.memoryStatusSummary()
	require.True(t, status.FileEnabled)
	require.Equal(t, 1, status.FileCount)
	require.Empty(t, status.Files)
	require.Zero(t, reads.Load())

	full := svc.memoryStatus()
	require.Len(t, full.Files, 1)
	require.EqualValues(t, 1, reads.Load())
}

func TestMemoryStatusSummary_TypedNilStoreStaysUnconfigured(t *testing.T) {
	t.Parallel()

	var typedNil *memoryfile.Store
	var store MemoryFileStore = typedNil

	svc := New(Config{
		MemoryBackend: "file",
		MemoryFiles:   store,
	})

	status := svc.memoryStatusSummary()
	require.True(t, status.Enabled)
	require.False(t, status.FileEnabled)
	require.Empty(t, status.Root)
	require.Empty(t, status.Error)
}

func TestMemoryFileViews_NilStoreReturnsEmpty(t *testing.T) {
	t.Parallel()

	views, err := memoryFileViews(nil, true)
	require.NoError(t, err)
	require.Empty(t, views)
}

func TestMemoryFileViews_CoversFilesystemVariants(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	writeMemoryFile := func(appPart string, userPart string, body string, mod time.Time) {
		t.Helper()
		dir := filepath.Join(root, appPart, userPart)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		path := filepath.Join(dir, memoryFileName)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		require.NoError(t, os.Chtimes(path, mod, mod))
	}

	appOpenclaw := base64.RawURLEncoding.EncodeToString([]byte("openclaw"))
	appDemo := base64.RawURLEncoding.EncodeToString([]byte("demo"))
	userAlice := base64.RawURLEncoding.EncodeToString([]byte("alice"))
	userBlank := base64.RawURLEncoding.EncodeToString([]byte("   "))
	userBob := base64.RawURLEncoding.EncodeToString([]byte("bob"))

	now := time.Now().UTC()
	writeMemoryFile(
		appOpenclaw,
		userAlice,
		"# Memory\n\n- Alice prefers concise updates.\n",
		now.Add(2*time.Minute),
	)
	writeMemoryFile(
		appOpenclaw,
		"%%%",
		"# Memory\n\n- Invalid base64 path part.\n",
		now.Add(time.Minute),
	)
	writeMemoryFile(
		appDemo,
		userBlank,
		"# Memory\n\n- Blank user id decodes back to encoded path.\n",
		now,
	)

	// Root-level file should be ignored.
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, "README.md"), []byte("ignore"), 0o600),
	)
	// User-level file should be ignored because it is not a directory.
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, appOpenclaw, "notes.txt"), []byte("ignore"), 0o600),
	)
	// MEMORY.md directory should be ignored.
	require.NoError(
		t,
		os.MkdirAll(filepath.Join(root, appOpenclaw, userBob, memoryFileName), 0o755),
	)

	views, err := memoryFileViews(store, true)
	require.NoError(t, err)
	require.Len(t, views, 3)

	require.Equal(t, "alice", views[0].UserID)
	require.Equal(t, "openclaw", views[0].AppName)
	require.Contains(t, views[0].Preview, "Alice prefers concise updates.")

	require.Equal(t, "%%%", views[1].UserID)
	require.Equal(t, "openclaw", views[1].AppName)

	require.Equal(t, userBlank, views[2].UserID)
	require.Equal(t, "demo", views[2].AppName)
	require.Contains(t, views[2].OpenURL, routeMemoryFile+"?path=")
}

func TestMemoryFileViews_SortsByModifiedAtAppAndUser(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	writeMemoryFile := func(app string, user string) {
		t.Helper()
		dir := filepath.Join(root, app, user)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		path := filepath.Join(dir, memoryFileName)
		require.NoError(t, os.WriteFile(path, []byte("# Memory\n\n- note\n"), 0o600))
		mod := time.Unix(1_700_000_000, 0)
		require.NoError(t, os.Chtimes(path, mod, mod))
	}

	appA := base64.RawURLEncoding.EncodeToString([]byte("aaa"))
	appB := base64.RawURLEncoding.EncodeToString([]byte("bbb"))
	userA := base64.RawURLEncoding.EncodeToString([]byte("alice"))
	userB := base64.RawURLEncoding.EncodeToString([]byte("bob"))

	writeMemoryFile(appB, userB)
	writeMemoryFile(appA, userB)
	writeMemoryFile(appB, userA)

	views, err := memoryFileViews(store, false)
	require.NoError(t, err)
	require.Len(t, views, 3)
	require.Empty(t, views[0].Preview)
	require.Empty(t, views[1].Preview)
	require.Empty(t, views[2].Preview)
	require.Equal(t, "aaa", views[0].AppName)
	require.Equal(t, "bob", views[0].UserID)
	require.Equal(t, "bbb", views[1].AppName)
	require.Equal(t, "alice", views[1].UserID)
	require.Equal(t, "bbb", views[2].AppName)
	require.Equal(t, "bob", views[2].UserID)
}

func TestMemoryFileViews_NonexistentAndInvalidRoots(t *testing.T) {
	t.Parallel()

	store, err := memoryfile.NewStore(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)

	views, err := memoryFileViews(store, true)
	require.NoError(t, err)
	require.Empty(t, views)

	rootFile := filepath.Join(t.TempDir(), "root-file")
	require.NoError(t, os.WriteFile(rootFile, []byte("not-a-dir"), 0o600))
	store, err = memoryfile.NewStore(rootFile)
	require.NoError(t, err)

	_, err = memoryFileViews(store, true)
	require.ErrorContains(t, err, "read memory root")
}

func TestMemoryFileViews_SkipsUnreadableAppDir(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("permission errors are not reliable on windows")
	}

	root := t.TempDir()
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	appDir := filepath.Join(
		root,
		base64.RawURLEncoding.EncodeToString([]byte("openclaw")),
	)
	require.NoError(t, os.MkdirAll(appDir, 0o700))
	require.NoError(t, os.Chmod(appDir, 0))
	defer os.Chmod(appDir, 0o700)

	views, err := memoryFileViews(store, true)
	require.NoError(t, err)
	require.Empty(t, views)
}

func TestDecodeMemoryPathPartVariants(t *testing.T) {
	t.Parallel()

	require.Empty(t, decodeMemoryPathPart(" "))
	require.Equal(t, "%%%", decodeMemoryPathPart("%%%"))

	blank := base64.RawURLEncoding.EncodeToString([]byte("   "))
	require.Equal(t, blank, decodeMemoryPathPart(blank))

	trimmed := base64.RawURLEncoding.EncodeToString([]byte(" alice "))
	require.Equal(t, "alice", decodeMemoryPathPart(trimmed))
}

func TestResolveMemoryFile_RejectsAbsolutePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := resolveMemoryFile(root, filepath.Join(root, "app", "user", memoryFileName))
	require.ErrorContains(t, err, "invalid memory file path")
}

func TestResolveMemoryFile_RejectsDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "app", "user", memoryFileName)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	_, err := resolveMemoryFile(root, "app/user/MEMORY.md")
	require.ErrorContains(t, err, "memory path is a directory")
}

func TestResolveMemoryFile_RejectsSymlinkEscapes(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are not reliable on windows")
	}

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("# Memory\n"), 0o600))

	dir := filepath.Join(root, "app", "user")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(
		t,
		os.Symlink(outside, filepath.Join(dir, memoryFileName)),
	)

	_, err := resolveMemoryFile(root, "app/user/MEMORY.md")
	require.ErrorContains(t, err, "memory file escapes memory root")
}
