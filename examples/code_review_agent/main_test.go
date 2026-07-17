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
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunAllFixtures(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one.diff"), []byte(""), 0o644))
	out := t.TempDir()
	err := runAllFixtures(context.Background(), ReviewOptions{
		FixtureDir:     dir,
		OutDir:         out,
		Runtime:        "fake",
		DryRun:         true,
		SandboxTimeout: time.Second,
		SkillsRoot:     "skills",
	})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(out, "one", "review_report.json"))
}

func TestRunCLI(t *testing.T) {
	out := t.TempDir()
	err := runCLI([]string{
		"--fixture", "clean",
		"--fixture-dir", "testdata/fixtures",
		"--runtime", "fake",
		"--dry-run",
		"--out-dir", out,
		"--skills-root", "skills",
	})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(out, "review_report.json"))

	err = runCLI([]string{"--sandbox-timeout", "not-a-duration", "--runtime", "fake"})
	require.Error(t, err)
	err = runCLI([]string{"--unknown"})
	require.Error(t, err)
	err = runAllFixtures(context.Background(), ReviewOptions{FixtureDir: filepath.Join(t.TempDir(), "missing")})
	require.Error(t, err)
}
