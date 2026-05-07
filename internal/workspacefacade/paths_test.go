//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspacefacade

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasGlobMeta(t *testing.T) {
	require.False(t, HasGlobMeta(""))
	require.False(t, HasGlobMeta("work/demo.txt"))
	require.True(t, HasGlobMeta("work/*.txt"))
	require.True(t, HasGlobMeta("work/?.txt"))
	require.True(t, HasGlobMeta("work/[abc].txt"))
}

func TestIsWorkspaceEnvPath(t *testing.T) {
	require.True(t, IsWorkspaceEnvPath("$WORK_DIR/demo"))
	require.True(t, IsWorkspaceEnvPath("${OUTPUT_DIR}/demo"))
	require.True(t, IsWorkspaceEnvPath("${SKILLS_DIR}/demo"))
	require.False(t, IsWorkspaceEnvPath("$OUTPUT_DIR_demo"))
	require.False(t, IsWorkspaceEnvPath("/tmp/demo"))
	require.False(t, IsWorkspaceEnvPath(""))
}

func TestNormalizeArtifactPath(t *testing.T) {
	rel, err := NormalizeArtifactPath("/out/site.zip")
	require.NoError(t, err)
	require.Equal(t, "out/site.zip", rel)

	rel, err = NormalizeArtifactPath("${OUTPUT_DIR}/site.zip")
	require.NoError(t, err)
	require.Equal(t, "out/site.zip", rel)

	_, err = NormalizeArtifactPath("")
	require.Error(t, err)

	_, err = NormalizeArtifactPath("/")
	require.Error(t, err)

	_, err = NormalizeArtifactPath("../site.zip")
	require.Error(t, err)

	_, err = NormalizeArtifactPath("tmp/site.zip")
	require.Error(t, err)

	_, err = NormalizeArtifactPath("work/*.txt")
	require.Error(t, err)
}

func TestIsAllowedPublishArtifactPath(t *testing.T) {
	require.True(t, IsAllowedPublishArtifactPath("work/demo"))
	require.True(t, IsAllowedPublishArtifactPath("out/demo"))
	require.True(t, IsAllowedPublishArtifactPath("runs/demo"))
	require.False(t, IsAllowedPublishArtifactPath("skills/demo"))
	require.False(t, IsAllowedPublishArtifactPath("tmp/demo"))
}
