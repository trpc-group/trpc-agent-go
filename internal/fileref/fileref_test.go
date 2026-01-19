package fileref_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
)

func TestParse_NoScheme(t *testing.T) {
	ref, err := fileref.Parse("out/a.txt")
	require.NoError(t, err)
	require.Empty(t, ref.Scheme)
	require.Equal(t, "out/a.txt", ref.Path)
}

func TestParse_WorkspaceScheme(t *testing.T) {
	ref, err := fileref.Parse("workspace://out/a.txt")
	require.NoError(t, err)
	require.Equal(t, fileref.SchemeWorkspace, ref.Scheme)
	require.Equal(t, "out/a.txt", ref.Path)
}

func TestParse_ArtifactScheme(t *testing.T) {
	ref, err := fileref.Parse("artifact://x.txt@12")
	require.NoError(t, err)
	require.Equal(t, fileref.SchemeArtifact, ref.Scheme)
	require.Equal(t, "x.txt", ref.ArtifactName)
	require.NotNil(t, ref.ArtifactVersion)
	require.Equal(t, 12, *ref.ArtifactVersion)
}

func TestParse_UnsupportedScheme(t *testing.T) {
	_, err := fileref.Parse("unknown://x")
	require.Error(t, err)
}

func TestTryRead_Workspace_FromCache(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	toolcache.StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{{
			Name:     "out/a.txt",
			Content:  "hi",
			MIMEType: "text/plain",
		}},
	)

	content, mime, handled, err := fileref.TryRead(
		ctx,
		"workspace://out/a.txt",
	)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "hi", content)
	require.Equal(t, "text/plain", mime)
}

func TestTryRead_Artifact_NoService(t *testing.T) {
	content, mime, handled, err := fileref.TryRead(
		context.Background(),
		"artifact://x.txt@1",
	)
	require.Error(t, err)
	require.True(t, handled)
	require.Empty(t, content)
	require.Empty(t, mime)
}
