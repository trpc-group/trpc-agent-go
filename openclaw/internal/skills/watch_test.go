//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const watchTestSkill = `---
name: demo
description: test
---

hello
`

const (
	watchErrClosed = "file already closed"
	watchErrRemove = "can't remove non-existent watch"
)

func TestWatchService_RefreshesWhenSkillAdded(t *testing.T) {
	root := t.TempDir()
	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	watch := NewWatchService(repo, []string{root}, WatchConfig{
		Enabled:  true,
		Debounce: 20 * time.Millisecond,
	})
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	writeSkill(t, root, "demo", watchTestSkill)

	require.Eventually(t, func() bool {
		if !hasSkillSummary(repo.Summaries(), "demo") {
			return false
		}
		status := watch.Status()
		return status != nil &&
			status.LastRefreshReason ==
				watchRefreshReasonWatch &&
			status.LastRefreshAt != nil &&
			status.Generation >= 1
	}, time.Second, 10*time.Millisecond)

	status := watch.Status()
	require.NotNil(t, status)
	require.Equal(t, watchRefreshReasonWatch, status.LastRefreshReason)
	require.NotNil(t, status.LastRefreshAt)
	require.GreaterOrEqual(t, status.Generation, int64(1))
}

func TestWatchService_RefreshesWhenRootCreatedLater(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")
	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	watch := NewWatchService(repo, []string{root}, WatchConfig{
		Enabled:  true,
		Debounce: 20 * time.Millisecond,
	})
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	require.NoError(t, os.MkdirAll(root, 0o755))
	writeSkill(t, root, "demo", watchTestSkill)

	require.Eventually(t, func() bool {
		return hasSkillSummary(repo.Summaries(), "demo")
	}, time.Second, 10*time.Millisecond)
}

func TestWatchService_RefreshesWhenNestedFileChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := writeSkill(t, root, "demo", watchTestSkill)
	guideDir := filepath.Join(skillDir, "docs")
	guidePath := filepath.Join(guideDir, "guide.md")
	require.NoError(t, os.MkdirAll(guideDir, 0o755))
	require.NoError(t, os.WriteFile(guidePath, []byte("v1"), 0o644))

	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	watch := NewWatchService(repo, []string{root}, WatchConfig{
		Enabled:  true,
		Debounce: 20 * time.Millisecond,
	})
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	require.NoError(t, os.WriteFile(guidePath, []byte("v2"), 0o644))

	require.Eventually(t, func() bool {
		status := watch.Status()
		return status != nil &&
			status.Generation >= 1 &&
			status.LastChangedPath == guidePath
	}, time.Second, 10*time.Millisecond)
}

func TestWatchService_DisabledDoesNotRefresh(t *testing.T) {
	root := t.TempDir()
	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	watch := NewWatchService(repo, []string{root}, WatchConfig{
		Enabled: false,
	})
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	writeSkill(t, root, "demo", watchTestSkill)
	time.Sleep(120 * time.Millisecond)

	require.False(t, hasSkillSummary(repo.Summaries(), "demo"))
	status := watch.Status()
	require.NotNil(t, status)
	require.False(t, status.Enabled)
}

func TestWatchService_NilReceiverHelpers(t *testing.T) {
	t.Parallel()

	var watch *WatchService
	require.NoError(t, watch.Close())
	require.NoError(t, watch.Refresh())
	require.Nil(t, watch.Status())

	watch.recordError(errors.New("boom"))
}

func TestWatchService_ManualRefreshUpdatesStatus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	watch := NewWatchService(repo, []string{root}, WatchConfig{
		Enabled: false,
	})
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	writeSkill(t, root, "demo", watchTestSkill)

	require.NoError(t, watch.Refresh())
	require.True(t, hasSkillSummary(repo.Summaries(), "demo"))

	status := watch.Status()
	require.NotNil(t, status)
	require.Equal(t, watchRefreshReasonManual, status.LastRefreshReason)
	require.NotNil(t, status.LastRefreshAt)
	require.Equal(t, filepath.Clean(root), status.Roots[0])
	require.GreaterOrEqual(t, status.Generation, int64(1))
}

func TestWatchService_HelperFunctions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	localRoot := filepath.Join(root, "local")
	bundledRoot := filepath.Join(root, "bundled")
	linkRoot := filepath.Join(root, "linked")
	require.NoError(t, os.MkdirAll(localRoot, 0o755))
	require.NoError(t, os.MkdirAll(bundledRoot, 0o755))
	require.NoError(t, os.Symlink(localRoot, linkRoot))

	fileURL := fmt.Sprintf("file://localhost%s", filepath.ToSlash(localRoot))
	remoteURL := fmt.Sprintf("file://remote%s", filepath.ToSlash(localRoot))

	require.Equal(t, "", normalizeWatchPath(""))
	require.Equal(t, "", normalizeWatchPath("http://[::1"))
	require.Equal(
		t,
		"",
		normalizeWatchPath("https://example.com/skills"),
	)
	require.Equal(t, "", normalizeWatchPath(remoteURL))
	require.Equal(t, localRoot, normalizeWatchPath(fileURL))
	require.Equal(t, localRoot, normalizeWatchPath(linkRoot))

	require.Equal(
		t,
		[]string{localRoot},
		normalizeWatchRoots(
			[]string{
				" ",
				bundledRoot,
				fileURL,
				localRoot,
				"https://example.com/skills",
			},
			WatchConfig{
				BundledRoot:  bundledRoot,
				WatchBundled: false,
			},
		),
	)
	require.Equal(
		t,
		[]string{bundledRoot},
		normalizeWatchRoots(
			[]string{bundledRoot},
			WatchConfig{
				BundledRoot:  bundledRoot,
				WatchBundled: true,
			},
		),
	)

	missingRoot := filepath.Join(root, "missing", "skills")
	require.Equal(t, root, nearestExistingWatchParent(missingRoot))
	require.Equal(t, "", nearestExistingWatchParent(""))
	require.Equal(
		t,
		string(os.PathSeparator),
		nearestExistingWatchParent(string(os.PathSeparator)),
	)

	require.True(
		t,
		isIgnoredWatchPath(filepath.Join(root, "node_modules", "pkg")),
	)
	require.False(t, isIgnoredWatchPath(filepath.Join(root, "demo")))
	require.True(t, isIgnoredWatchName("node_modules"))
	require.False(t, isIgnoredWatchName("demo"))

	require.False(t, isIgnorableWatchError(nil))
	require.True(t, isIgnorableWatchError(errors.New(watchErrRemove)))
	require.True(t, isIgnorableWatchError(errors.New(watchErrClosed)))
	require.False(t, isIgnorableWatchError(errors.New("boom")))

	require.True(t, watchDirExists(root))
	require.False(t, watchDirExists(filepath.Join(root, "missing")))
}

func TestWatchService_RelevantEventAndDesiredWatchDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	skillDir := filepath.Join(root, "demo")
	nestedDir := filepath.Join(skillDir, "docs")
	nestedFile := filepath.Join(nestedDir, "guide.md")
	ignoredDir := filepath.Join(root, "node_modules")
	outsideFile := filepath.Join(t.TempDir(), "note.txt")
	watchedDir := filepath.Join(root, "watched")
	missingRoot := filepath.Join(t.TempDir(), "missing", "skills")

	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))
	require.NoError(t, os.MkdirAll(ignoredDir, 0o755))
	require.NoError(t, os.MkdirAll(watchedDir, 0o755))
	require.NoError(t, os.WriteFile(nestedFile, []byte("guide"), 0o644))
	require.NoError(t, os.WriteFile(outsideFile, []byte("note"), 0o644))

	watch := &WatchService{
		repo: repo,
		roots: []string{
			root,
			missingRoot,
			"",
		},
		watched: map[string]struct{}{
			root:       {},
			nestedDir:  {},
			watchedDir: {},
		},
	}

	dirs := watch.desiredWatchDirs()
	require.Contains(t, dirs, root)
	require.Contains(t, dirs, skillDir)
	require.Contains(t, dirs, nestedDir)
	require.Contains(
		t,
		dirs,
		nearestExistingWatchParent(missingRoot),
	)
	require.NotContains(t, dirs, ignoredDir)
	require.NotContains(t, dirs, outsideFile)

	path, ok := watch.relevantEvent(fsnotify.Event{Name: " "})
	require.False(t, ok)
	require.Empty(t, path)

	path, ok = watch.relevantEvent(fsnotify.Event{
		Name: filepath.Join(ignoredDir, skillFileName),
	})
	require.False(t, ok)
	require.Empty(t, path)

	skillPath := filepath.Join(skillDir, skillFileName)
	path, ok = watch.relevantEvent(fsnotify.Event{Name: skillPath})
	require.True(t, ok)
	require.Equal(t, skillPath, path)

	path, ok = watch.relevantEvent(fsnotify.Event{Name: root})
	require.True(t, ok)
	require.Equal(t, root, path)

	newDir := filepath.Join(root, "new-skill")
	require.NoError(t, os.MkdirAll(newDir, 0o755))
	path, ok = watch.relevantEvent(fsnotify.Event{
		Name: newDir,
		Op:   fsnotify.Create,
	})
	require.True(t, ok)
	require.Equal(t, newDir, path)

	path, ok = watch.relevantEvent(fsnotify.Event{Name: watchedDir})
	require.True(t, ok)
	require.Equal(t, watchedDir, path)

	path, ok = watch.relevantEvent(fsnotify.Event{Name: nestedFile})
	require.True(t, ok)
	require.Equal(t, nestedFile, path)

	path, ok = watch.relevantEvent(fsnotify.Event{Name: outsideFile})
	require.False(t, ok)
	require.Empty(t, path)
}

func TestWatchService_RecordErrorAndSyncWithoutWatcher(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo, err := NewRepository([]string{root})
	require.NoError(t, err)

	watch := NewWatchService(repo, []string{root}, WatchConfig{
		Enabled: false,
	})
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	require.NoError(t, watch.syncWatches())

	watch.recordError(nil)
	watch.recordError(errors.New("boom"))

	status := watch.Status()
	require.NotNil(t, status)
	require.Equal(t, "boom", status.LastError)
}

func hasSkillSummary(
	summaries []skill.Summary,
	name string,
) bool {
	for _, summary := range summaries {
		if summary.Name == name {
			return true
		}
	}
	return false
}
