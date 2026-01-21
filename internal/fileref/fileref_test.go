//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fileref_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestParse_NoScheme(t *testing.T) {
	ref, err := fileref.Parse("out/a.txt")
	require.NoError(t, err)
	require.Empty(t, ref.Scheme)
	require.Equal(t, "out/a.txt", ref.Path)
}

func TestParse_EmptyString(t *testing.T) {
	ref, err := fileref.Parse("   ")
	require.NoError(t, err)
	require.Empty(t, ref.Scheme)
	require.Empty(t, ref.Path)
}

func TestParse_WorkspaceScheme(t *testing.T) {
	ref, err := fileref.Parse("workspace://out/a.txt")
	require.NoError(t, err)
	require.Equal(t, fileref.SchemeWorkspace, ref.Scheme)
	require.Equal(t, "out/a.txt", ref.Path)
}

func TestParse_Workspace_EmptyOrDot(t *testing.T) {
	ref, err := fileref.Parse("workspace://.")
	require.NoError(t, err)
	require.Equal(t, fileref.SchemeWorkspace, ref.Scheme)
	require.Empty(t, ref.Path)
}

func TestParse_Workspace_CleansToDot(t *testing.T) {
	ref, err := fileref.Parse("workspace://a/..")
	require.NoError(t, err)
	require.Equal(t, fileref.SchemeWorkspace, ref.Scheme)
	require.Empty(t, ref.Path)
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

func TestParse_Artifact_InvalidRef(t *testing.T) {
	_, err := fileref.Parse("artifact://x@bad")
	require.Error(t, err)
}

func TestParse_Artifact_EmptyNameAfterParse(t *testing.T) {
	_, err := fileref.Parse("artifact://@1")
	require.Error(t, err)
}

func TestParse_Artifact_EmptyName(t *testing.T) {
	_, err := fileref.Parse("artifact://")
	require.Error(t, err)
}

func TestParse_Workspace_InvalidPath(t *testing.T) {
	_, err := fileref.Parse("workspace://../x")
	require.Error(t, err)

	_, err = fileref.Parse("workspace:///abs")
	require.Error(t, err)
}

func TestWorkspaceRef(t *testing.T) {
	require.Equal(t, "workspace://out/a.txt", fileref.WorkspaceRef("out/a.txt"))
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

func TestTryRead_Workspace_NotExported(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	content, mime, handled, err := fileref.TryRead(
		ctx,
		"workspace://out/a.txt",
	)
	require.Error(t, err)
	require.True(t, handled)
	require.Empty(t, content)
	require.Empty(t, mime)
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

func TestTryRead_Artifact_WithService(t *testing.T) {
	svc := artifactinmemory.NewService()
	sess := session.NewSession("app", "user", "sess")

	inv := agent.NewInvocation()
	inv.Session = sess
	inv.ArtifactService = svc
	ctx := agent.NewInvocationContext(context.Background(), inv)

	info := artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	ctxIO := codeexecutor.WithArtifactService(ctx, svc)
	ctxIO = codeexecutor.WithArtifactSession(ctxIO, info)
	_, err := codeexecutor.SaveArtifactHelper(
		ctxIO,
		"x.txt",
		[]byte("hi"),
		"text/plain",
	)
	require.NoError(t, err)

	content, mime, handled, err := fileref.TryRead(ctx, "artifact://x.txt")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "hi", content)
	require.Equal(t, "text/plain", mime)
}

func TestTryRead_NoScheme_NotHandled(t *testing.T) {
	content, mime, handled, err := fileref.TryRead(
		context.Background(),
		"out/a.txt",
	)
	require.NoError(t, err)
	require.False(t, handled)
	require.Empty(t, content)
	require.Empty(t, mime)
}

func TestTryRead_ParseErrorHandled(t *testing.T) {
	content, mime, handled, err := fileref.TryRead(
		context.Background(),
		"unknown://x",
	)
	require.Error(t, err)
	require.True(t, handled)
	require.Empty(t, content)
	require.Empty(t, mime)
}

func TestTryRead_Artifact_WithServiceInContext(t *testing.T) {
	svc := artifactinmemory.NewService()

	ctx := context.Background()
	ctx = codeexecutor.WithArtifactService(ctx, svc)
	ctx = codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{})

	_, err := codeexecutor.SaveArtifactHelper(
		ctx,
		"x.txt",
		[]byte("hi"),
		"text/plain",
	)
	require.NoError(t, err)

	content, mime, handled, err := fileref.TryRead(ctx, "artifact://x.txt")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "hi", content)
	require.Equal(t, "text/plain", mime)
}

func TestWorkspaceFiles(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	toolcache.StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{{Name: "out/a.txt", Content: "hi"}},
	)
	files := fileref.WorkspaceFiles(ctx)
	require.Len(t, files, 1)
	require.Equal(t, "out/a.txt", files[0].Name)
	require.Equal(t, "hi", files[0].Content)
}
