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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/buildinfo"
)

func TestCurrentVersionUsesReleaseValue(t *testing.T) {
	old := buildinfo.ReleaseVersion
	buildinfo.ReleaseVersion = " v0.0.3 "
	t.Cleanup(func() {
		buildinfo.ReleaseVersion = old
	})

	require.Equal(t, "v0.0.3", currentVersion())
}

func TestCurrentVersionFallsBackToDefault(t *testing.T) {
	old := buildinfo.ReleaseVersion
	buildinfo.ReleaseVersion = "   "
	t.Cleanup(func() {
		buildinfo.ReleaseVersion = old
	})

	require.Equal(t, buildinfo.DefaultReleaseVersion, currentVersion())
}

func TestNormalizeReleaseVersion(t *testing.T) {
	t.Parallel()

	require.Equal(t, "v1.2.3", normalizeReleaseVersion(" v1.2.3 "))
	require.Equal(t, "v1.2.3", normalizeReleaseVersion("1.2.3"))
	require.Equal(
		t,
		"v1.2.3",
		normalizeReleaseVersion("openclaw-v1.2.3"),
	)
	require.Empty(t, normalizeReleaseVersion(" "))
}

func TestReleaseTagForVersion(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"openclaw-v1.2.3",
		releaseTagForVersion("1.2.3"),
	)
	require.Empty(t, releaseTagForVersion(" "))
}
