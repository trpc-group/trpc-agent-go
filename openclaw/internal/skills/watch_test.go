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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const watchTestSkill = `---
name: demo
description: test
---

hello
`

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
		return hasSkillSummary(repo.Summaries(), "demo")
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
