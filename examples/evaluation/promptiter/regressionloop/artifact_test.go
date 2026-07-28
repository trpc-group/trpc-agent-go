//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestPublishBundleWritesAcceptedProfileWithPrivateModes(t *testing.T) {
	root := t.TempDir()
	report := minimalReport()
	report.Accepted = true
	profile := &promptiter.Profile{StructureID: "structure-1"}

	require.NoError(t, publishBundle(root, report, profile))
	dir := filepath.Join(root, report.RunID)
	for _, name := range []string{reportJSONName, reportMarkdownName, candidateProfileName} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
	}
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm())
}

func TestPublishBundlePreservesExistingRootMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "operator-owned")
	require.NoError(t, os.Mkdir(root, 0o750))
	require.NoError(t, publishBundle(root, minimalReport(), nil))

	info, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o750), info.Mode().Perm())
}

func TestPublishBundleRejectsCredentialBearingProfile(t *testing.T) {
	root := t.TempDir()
	report := minimalReport()
	report.Accepted = true
	profile := &promptiter.Profile{
		StructureID: "structure-1",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#model",
			Value: structure.SurfaceValue{Model: &structure.ModelRef{
				Name: "model", APIKey: "secret",
			}},
		}},
	}

	require.ErrorContains(t, publishBundle(root, report, profile), "credential")
	_, err := os.Stat(filepath.Join(root, report.RunID))
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestPublishBundleDoesNotLeaveCandidateOnFailure(t *testing.T) {
	root := t.TempDir()
	report := minimalReport()
	report.Accepted = true

	err := publishBundle(root, report, nil)
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(root, report.RunID, candidateProfileName))
	assert.ErrorIs(t, statErr, fs.ErrNotExist)
}

func TestPublishBundleRejectsUnsafeRunIDAndPreservesExistingBundle(t *testing.T) {
	root := t.TempDir()
	report := minimalReport()
	report.RunID = "../escape"
	require.Error(t, publishBundle(root, report, nil))

	report.RunID = "run-1"
	existing := filepath.Join(root, report.RunID)
	require.NoError(t, os.Mkdir(existing, 0o700))
	marker := filepath.Join(existing, "marker")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
	require.Error(t, publishBundle(root, report, nil))
	contents, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(contents))
}
