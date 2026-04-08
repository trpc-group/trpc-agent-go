package admin

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
)

type memoryStoreStub struct {
	root string
}

func (s memoryStoreStub) Root() string {
	return s.root
}

func (s memoryStoreStub) ReadFile(string, int) (string, error) {
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

func TestMemoryFileViews_NilStoreReturnsEmpty(t *testing.T) {
	t.Parallel()

	views, err := memoryFileViews(nil)
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

	views, err := memoryFileViews(store)
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

	views, err := memoryFileViews(store)
	require.NoError(t, err)
	require.Len(t, views, 3)
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

	views, err := memoryFileViews(store)
	require.NoError(t, err)
	require.Empty(t, views)

	rootFile := filepath.Join(t.TempDir(), "root-file")
	require.NoError(t, os.WriteFile(rootFile, []byte("not-a-dir"), 0o600))
	store, err = memoryfile.NewStore(rootFile)
	require.NoError(t, err)

	_, err = memoryFileViews(store)
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

	views, err := memoryFileViews(store)
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
