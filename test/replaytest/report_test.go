//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// WriteDiffReport
// ---------------------------------------------------------------------------

func TestWriteDiffReport_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	diffs := []DiffEntry{
		{
			Case:      "test_case",
			SessionID: "s1",
			BackendA:  "in_memory",
			BackendB:  "sqlite",
			Section:   "events",
			Path:      "$.events[0].content",
			Left:      "a",
			Right:     "b",
		},
	}
	err := WriteDiffReport(path, diffs)
	require.NoError(t, err)

	// Read back and verify.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var decoded []DiffEntry
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded, 1)
	assert.Equal(t, "test_case", decoded[0].Case)
	assert.Equal(t, "$.events[0].content", decoded[0].Path)
}

func TestWriteDiffReport_EmptyDiffs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	err := WriteDiffReport(path, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "[]\n", string(data))
}

func TestWriteDiffReport_EmptyPath_UsesDefault(t *testing.T) {
	// Temporarily override the env var to write to a temp location.
	tmpPath := filepath.Join(t.TempDir(), "custom_report.json")
	t.Setenv(EnvReportPath, tmpPath)

	err := WriteDiffReport("", []DiffEntry{})
	require.NoError(t, err)

	_, err = os.Stat(tmpPath)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// DiffReportPath
// ---------------------------------------------------------------------------

func TestDiffReportPath_Default(t *testing.T) {
	// Clear env override.
	t.Setenv(EnvReportPath, "")
	assert.Equal(t, defaultReportName, DiffReportPath())
}

func TestDiffReportPath_EnvVar(t *testing.T) {
	custom := "/tmp/my_custom_diff.json"
	t.Setenv(EnvReportPath, custom)
	assert.Equal(t, custom, DiffReportPath())
}

// ---------------------------------------------------------------------------
// HasUnexpectedDiffs
// ---------------------------------------------------------------------------

func TestHasUnexpectedDiffs_True(t *testing.T) {
	diffs := []DiffEntry{
		{Allowed: true, Reason: "expected"},
		{Allowed: false},
	}
	assert.True(t, HasUnexpectedDiffs(diffs))
}

func TestHasUnexpectedDiffs_False(t *testing.T) {
	diffs := []DiffEntry{
		{Allowed: true, Reason: "a"},
		{Allowed: true, Reason: "b"},
	}
	assert.False(t, HasUnexpectedDiffs(diffs))
}

func TestHasUnexpectedDiffs_Empty(t *testing.T) {
	assert.False(t, HasUnexpectedDiffs(nil))
	assert.False(t, HasUnexpectedDiffs([]DiffEntry{}))
}
