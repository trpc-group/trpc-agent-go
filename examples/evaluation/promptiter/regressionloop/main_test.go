//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	reportBytes, err := os.ReadFile(filepath.Join(output, "golden", reportJSONName))
	require.NoError(t, err)
	var report regressionReport
	require.NoError(t, json.Unmarshal(reportBytes, &report))
	assert.NotContains(t, report.Roles, "judge")
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
	firstMarkdown, err := os.ReadFile(filepath.Join(first, "stable", reportMarkdownName))
	require.NoError(t, err)
	secondMarkdown, err := os.ReadFile(filepath.Join(second, "stable", reportMarkdownName))
	require.NoError(t, err)
	assert.Equal(t, firstMarkdown, secondMarkdown)
}

func TestDeterministicSampleMatchesGenerator(t *testing.T) {
	output := t.TempDir()
	_, err := runCLI(context.Background(), cliOptions{
		ConfigPath: "data/promptiter.json", DataDir: "data", OutputDir: output, RunID: "sample",
	})
	require.NoError(t, err)

	for _, name := range []string{
		reportJSONName,
		reportMarkdownName,
		candidateProfileName,
	} {
		generated, readErr := os.ReadFile(filepath.Join(output, "sample", name))
		require.NoError(t, readErr)
		committed, readErr := os.ReadFile(filepath.Join("output", "sample", name))
		require.NoError(t, readErr)
		assert.Equal(t, committed, generated, name)
	}
}

func TestExecuteMainDoesNotPrintArtifactWhenPublishFails(t *testing.T) {
	output := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(output, "existing"), 0o700))

	stdout, err := captureStdout(t, func() error {
		return executeMain(context.Background(), []string{
			"-config", "data/promptiter.json",
			"-data-dir", "data",
			"-output-dir", output,
			"-run-id", "existing",
		})
	})

	require.ErrorContains(t, err, "already exists")
	assert.NotContains(t, stdout, "artifact=")
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = original })

	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&output, reader)
		done <- copyErr
	}()
	runErr := run()
	require.NoError(t, writer.Close())
	os.Stdout = original
	require.NoError(t, <-done)
	require.NoError(t, reader.Close())
	return output.String(), runErr
}
