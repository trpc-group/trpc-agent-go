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

func TestIsAllowedWorkspaceRoot(t *testing.T) {
	// Looser than IsAllowedPublishArtifactPath — also accepts skills/.
	require.True(t, IsAllowedWorkspaceRoot("skills"))
	require.True(t, IsAllowedWorkspaceRoot("skills/echoer"))
	require.True(t, IsAllowedWorkspaceRoot("work"))
	require.True(t, IsAllowedWorkspaceRoot("work/x.txt"))
	require.True(t, IsAllowedWorkspaceRoot("out/done.bin"))
	require.True(t, IsAllowedWorkspaceRoot("runs/foo/bar"))
	require.False(t, IsAllowedWorkspaceRoot(""))
	require.False(t, IsAllowedWorkspaceRoot("etc/passwd"))
	require.False(t, IsAllowedWorkspaceRoot("workspaces"))
}

func TestNormalizeWorkspaceCWD(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "empty becomes root", in: "", want: "."},
		{name: "whitespace becomes root", in: "   ", want: "."},
		{name: "dot stays root", in: ".", want: "."},
		{name: "work root", in: "work", want: "work"},
		{name: "nested out path", in: "out/sub/file", want: "out/sub/file"},
		{name: "windows separators", in: "work\\sub", want: "work/sub"},
		{name: "absolute to work", in: "/work/x", want: "work/x"},
		{name: "absolute root collapses", in: "/", want: "."},
		{name: "env-prefixed path expands", in: "${WORK_DIR}/x", want: "work/x"},

		{name: "rejects glob", in: "work/*.txt", wantErr: "glob"},
		{name: "rejects parent escape", in: "..", wantErr: "stay within"},
		{name: "rejects parent prefix escape", in: "../etc", wantErr: "stay within"},
		{name: "rejects disallowed root", in: "etc/passwd", wantErr: "workspace roots"},
		{name: "rejects absolute disallowed root", in: "/etc/passwd", wantErr: "workspace roots"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeWorkspaceCWD(tc.in)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

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
