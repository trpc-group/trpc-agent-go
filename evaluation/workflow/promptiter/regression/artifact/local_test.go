//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

func TestNewStoreValidatesAndResolvesRoot(t *testing.T) {
	_, err := NewStore("  ")
	assert.EqualError(t, err, "artifact root is empty")

	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	_, err = NewStore(file)
	require.Error(t, err)

	root := filepath.Join(t.TempDir(), "reports")
	store, err := NewStore(root)
	require.NoError(t, err)
	absolute, err := filepath.Abs(root)
	require.NoError(t, err)
	assert.Equal(t, absolute, store.root)
}

func TestWriteReportsCreatesIdempotentCompleteBundle(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	result := artifactResult("run-1")

	first, err := WriteReports(context.Background(), store, result)
	require.NoError(t, err)
	require.Len(t, first, 2)
	second, err := WriteReports(context.Background(), store, result)
	require.NoError(t, err)
	assert.Equal(t, first, second)

	names := []string{first[0].Name, first[1].Name}
	assert.ElementsMatch(t, []string{
		"run-1/optimization_report.json",
		"run-1/optimization_report.md",
	}, names)
	for _, file := range first {
		assert.NotEmpty(t, file.SHA256)
		assert.Positive(t, file.Size)
		_, err := os.Stat(file.Path)
		require.NoError(t, err)
	}
	entries, err := os.ReadDir(filepath.Join(store.root, "run-1"))
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestWriteReportsConcurrentPublishersConverge(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	result := artifactResult("concurrent")

	const publishers = 8
	outputs := make(chan []File, publishers)
	errors := make(chan error, publishers)
	var wait sync.WaitGroup
	for index := 0; index < publishers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			files, err := WriteReports(context.Background(), store, result)
			outputs <- files
			errors <- err
		}()
	}
	wait.Wait()
	close(outputs)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var expected []File
	for files := range outputs {
		require.Len(t, files, 2)
		if expected == nil {
			expected = files
			continue
		}
		assert.Equal(t, expected, files)
	}
	entries, err := os.ReadDir(store.root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "concurrent", entries[0].Name())
}

func TestWriteReportsRejectsInvalidOrIncompleteInputs(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)

	_, err = WriteReports(context.Background(), nil, artifactResult("run"))
	require.Error(t, err)
	_, err = WriteReports(context.Background(), store, nil)
	require.Error(t, err)
	_, err = WriteReports(context.Background(), store, &regression.RunResult{})
	require.Error(t, err)
	_, err = WriteReports(context.Background(), store, &regression.RunResult{RunID: "run"})
	require.ErrorContains(t, err, "spec is nil")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = WriteReports(ctx, store, artifactResult("canceled"))
	assert.ErrorIs(t, err, context.Canceled)
	_, err = WriteReports(context.Background(), (*Store)(nil), artifactResult("run"))
	require.Error(t, err)
}

func TestWriteReportsRejectsUnsafeRunDirectories(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	for _, runID := range []string{".", "..", "nested/run", `nested\\run`, "CON", "run."} {
		t.Run(runID, func(t *testing.T) {
			_, err := WriteReports(context.Background(), store, artifactResult(runID))
			require.Error(t, err)
		})
	}

	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(store.root, "linked")))
	_, err = WriteReports(context.Background(), store, artifactResult("linked"))
	require.ErrorContains(t, err, "symbolic link")
}

func TestWriteReportsRejectsExistingDifferentBundle(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	result := artifactResult("conflict")
	_, err = WriteReports(context.Background(), store, result)
	require.NoError(t, err)

	changed := *result
	changed.Decision = regression.DecisionAccepted
	_, err = WriteReports(context.Background(), store, &changed)
	require.ErrorContains(t, err, "different content")

	blocked := artifactResult("blocked")
	require.NoError(t, os.WriteFile(filepath.Join(store.root, "blocked"), []byte("x"), 0o600))
	_, err = WriteReports(context.Background(), store, blocked)
	require.Error(t, err)
	entries, readErr := os.ReadDir(store.root)
	require.NoError(t, readErr)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".report-bundle-")
	}
}

func TestDigestFileRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, []byte("content"), 0o600))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(target, link))
	_, err := digestFile(link)
	require.ErrorContains(t, err, "symbolic link")
}

func TestWriteReportsPropagatesRenderErrorWithoutPublishing(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	result := artifactResult("render-error")
	result.Spec = nil

	_, err = WriteReports(context.Background(), store, result)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(store.root, result.RunID))
	assert.True(t, errors.Is(statErr, os.ErrNotExist))
}

func artifactResult(runID string) *regression.RunResult {
	return &regression.RunResult{
		SchemaVersion: regression.CurrentSchemaVersion,
		RunID:         runID,
		Status:        regression.RunStatusSucceeded,
		Decision:      regression.DecisionRejected,
		Spec: &regression.RunSpec{
			RunID:            runID,
			InputFingerprint: "fixture",
			MetricPolicies: map[string]regression.MetricPolicy{
				"quality": {Weight: 1},
			},
			Runtime: regression.RuntimePolicy{NumRuns: 1},
		},
	}
}
