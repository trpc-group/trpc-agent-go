//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const mainHelperEnv = "CODE_REVIEW_AGENT_TEST_MAIN_HELPER"

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv(mainHelperEnv) != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	os.Args = append([]string{"code-review-agent"}, os.Args[separator+1:]...)
	main()
}

func runMainSubprocess(t *testing.T, args ...string) (string, error) {
	t.Helper()
	commandArgs := append([]string{"-test.run=^TestMainHelperProcess$", "--"}, args...)
	cmd := exec.Command(os.Args[0], commandArgs...)
	cmd.Env = append(os.Environ(), mainHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestMainHelpExitsSuccessfully(t *testing.T) {
	output, err := runMainSubprocess(t, "--help")
	require.NoError(t, err)
	require.Contains(t, output, "Usage of code-review-agent:")
	require.NotContains(t, output, "Error: flag: help requested")
}

func TestMainInvalidInvocationStillFails(t *testing.T) {
	output, err := runMainSubprocess(t)
	require.Error(t, err)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.NotZero(t, exitErr.ExitCode())
	require.Contains(t, output, "either --diff-file or --repo-path is required")
}

func TestRunClosesStorageWhenReportWriteFails(t *testing.T) {
	temp := t.TempDir()
	output := filepath.Join(temp, "out")
	require.NoError(t, os.MkdirAll(filepath.Join(output, "review_report.json"), 0o700))
	diff, err := filepath.Abs(filepath.Join("fixtures", "01_clean.diff"))
	require.NoError(t, err)
	database := filepath.Join(temp, "review.db")

	err = run([]string{
		"--diff-file", diff,
		"--db-path", database,
		"--output-dir", output,
		"--dry-run=true",
	})
	require.ErrorContains(t, err, "write json report")

	// On Windows this rename fails while SQLite still has the database open,
	// so it also verifies that the error return ran the deferred close.
	require.NoError(t, os.Rename(database, database+".closed"))
}
