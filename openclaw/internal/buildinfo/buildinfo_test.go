//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package buildinfo

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCurrentVersionUsesReleaseVersion(t *testing.T) {
	old := ReleaseVersion
	ReleaseVersion = " v1.2.3 "
	t.Cleanup(func() {
		ReleaseVersion = old
	})

	require.Equal(t, "v1.2.3", CurrentVersion())
}

func TestCurrentVersionFallsBackToDefault(t *testing.T) {
	old := ReleaseVersion
	ReleaseVersion = " "
	t.Cleanup(func() {
		ReleaseVersion = old
	})

	require.Equal(t, DefaultReleaseVersion, CurrentVersion())
}

func TestSnapshotIncludesReleaseMetadata(t *testing.T) {
	oldVersion := ReleaseVersion
	oldCommit := SourceCommit
	ReleaseVersion = "v1.2.3"
	SourceCommit = "abc123"
	t.Cleanup(func() {
		ReleaseVersion = oldVersion
		SourceCommit = oldCommit
	})

	info := Snapshot()
	require.Equal(t, "v1.2.3", info.Version)
	require.Equal(t, "abc123", info.SourceCommit)
	require.Equal(t, runtime.Version(), info.GoVersion)
}
