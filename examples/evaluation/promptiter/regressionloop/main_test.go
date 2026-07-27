//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeterministicEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output := t.TempDir()
	result, err := runCLI(ctx, cliOptions{
		ConfigPath: "data/promptiter.json", DataDir: "data", OutputDir: output, RunID: "golden",
	})
	require.NoError(t, err)
	require.Len(t, result.Rounds, 3)
	assert.True(t, result.Rounds[0].Gate.Accepted)
	assert.False(t, result.Rounds[1].Gate.Accepted)
	assert.False(t, result.Rounds[2].Gate.Accepted)
	assert.Contains(t, failedCheckIDs(result.Rounds[2].Gate), "critical_case")
	assert.FileExists(t, filepath.Join(output, "golden", candidateProfileName))
}

func TestDeterministicReportsAreByteStable(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	options := cliOptions{ConfigPath: "data/promptiter.json", DataDir: "data", RunID: "stable"}
	options.OutputDir = first
	_, err := runCLI(context.Background(), options)
	require.NoError(t, err)
	options.OutputDir = second
	_, err = runCLI(context.Background(), options)
	require.NoError(t, err)
	firstJSON, err := os.ReadFile(filepath.Join(first, "stable", reportJSONName))
	require.NoError(t, err)
	secondJSON, err := os.ReadFile(filepath.Join(second, "stable", reportJSONName))
	require.NoError(t, err)
	assert.Equal(t, firstJSON, secondJSON)
}
