//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

func TestLoadRunsRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), subagentRunsFileName)
	require.NoError(
		t,
		os.WriteFile(path, []byte(`{"version":999}`), 0o600),
	)

	_, err := loadRuns(path)
	require.ErrorContains(t, err, "unsupported store version")
}

func TestSaveRunsAndLoadRunsRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), subagentRunsFileName)
	now := time.Now()
	runs := map[string]*runRecord{
		"run-1": {
			Run: publicsubagent.Run{
				ID:        "run-1",
				Status:    publicsubagent.StatusCompleted,
				CreatedAt: now,
				UpdatedAt: now,
			},
			OwnerUserID: "user-a",
		},
	}
	require.NoError(t, saveRuns(path, runs))

	loaded, err := loadRuns(path)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, "user-a", loaded["run-1"].OwnerUserID)
}

func TestStoreAndTypeHelpers(t *testing.T) {
	t.Parallel()

	loaded, err := loadRuns(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err)
	require.Empty(t, loaded)

	var nilRecord *runRecord
	require.Equal(t, publicsubagent.Run{}, nilRecord.publicView())

	startedAt := time.Now()
	record := &runRecord{
		Run: publicsubagent.Run{
			ID:        "run-2",
			CreatedAt: startedAt,
			UpdatedAt: startedAt,
			StartedAt: cloneTime(startedAt),
		},
	}
	cloned := record.clone()
	require.Equal(t, record.ID, cloned.ID)
	require.NotSame(t, record.StartedAt, cloned.StartedAt)

	require.Equal(t, "trimmed", truncateRunes(" trimmed ", 0))
	require.Equal(t, "ab", truncateRunes("abcd", 2))
	require.Equal(t, "abc", truncateRunes("abc", 8))
}

func TestStoreErrorBranches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := loadRuns(dir)
	require.Error(t, err)

	badJSONPath := filepath.Join(t.TempDir(), subagentRunsFileName)
	require.NoError(t, os.WriteFile(badJSONPath, []byte("{"), 0o600))
	_, err = loadRuns(badJSONPath)
	require.Error(t, err)

	skippedPath := filepath.Join(t.TempDir(), subagentRunsFileName)
	require.NoError(t, os.WriteFile(
		skippedPath,
		[]byte(`{"runs":[{"id":""},{"id":"run-3","status":"queued"}]}`),
		0o600,
	))
	loaded, err := loadRuns(skippedPath)
	require.NoError(t, err)
	require.Len(t, loaded, 1)

	blockedParent := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(blockedParent, []byte("x"), 0o600))
	err = saveRuns(filepath.Join(blockedParent, subagentRunsFileName), nil)
	require.Error(t, err)

	err = saveRuns(filepath.Join(t.TempDir(), subagentRunsFileName), map[string]*runRecord{
		"nil": nil,
		"empty": {
			Run: publicsubagent.Run{},
		},
	})
	require.NoError(t, err)

	now := time.Now()
	changed := normalizeLoadedRuns(map[string]*runRecord{
		"nil": nil,
		"done": {
			Run: publicsubagent.Run{
				ID:     "done",
				Status: publicsubagent.StatusCompleted,
			},
		},
	}, now)
	require.False(t, changed)
}
