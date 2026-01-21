//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolcache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestSkillRunOutputFilesFromContext_Sorted(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{
			{Name: "b.txt", Content: "b", MIMEType: "text/plain"},
			{Name: "a.txt", Content: "a", MIMEType: "text/plain"},
		},
	)

	files := SkillRunOutputFilesFromContext(ctx)
	require.Len(t, files, 2)
	require.Equal(t, "a.txt", files[0].Name)
	require.Equal(t, "a", files[0].Content)
	require.Equal(t, "b.txt", files[1].Name)
	require.Equal(t, "b", files[1].Content)
}

func TestStoreSkillRunOutputFilesFromContext_NoInvocation(t *testing.T) {
	StoreSkillRunOutputFilesFromContext(
		context.Background(),
		[]codeexecutor.File{{Name: "a.txt", Content: "a"}},
	)
}

func TestStoreSkillRunOutputFiles_Merges(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{{Name: "a.txt", Content: "a"}},
	)
	StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{
			{Name: "b.txt", Content: "b"},
			{Name: "  ", Content: "ignored"},
		},
	)

	content, _, ok := LookupSkillRunOutputFileFromContext(ctx, "a.txt")
	require.True(t, ok)
	require.Equal(t, "a", content)

	content, _, ok = LookupSkillRunOutputFileFromContext(ctx, "b.txt")
	require.True(t, ok)
	require.Equal(t, "b", content)
}

func TestLookupSkillRunOutputFileFromContext_Miss(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	content, mime, ok := LookupSkillRunOutputFileFromContext(ctx, "a.txt")
	require.False(t, ok)
	require.Empty(t, content)
	require.Empty(t, mime)

	content, mime, ok = LookupSkillRunOutputFileFromContext(
		context.Background(),
		"a.txt",
	)
	require.False(t, ok)
	require.Empty(t, content)
	require.Empty(t, mime)
}

func TestLookupSkillRunOutputFile_InvalidArgs(t *testing.T) {
	content, mime, ok := LookupSkillRunOutputFile(nil, "a.txt")
	require.False(t, ok)
	require.Empty(t, content)
	require.Empty(t, mime)

	inv := agent.NewInvocation()
	content, mime, ok = LookupSkillRunOutputFile(inv, "  ")
	require.False(t, ok)
	require.Empty(t, content)
	require.Empty(t, mime)
}

func TestStoreSkillRunOutputFiles_EarlyReturns(t *testing.T) {
	StoreSkillRunOutputFiles(
		nil,
		[]codeexecutor.File{{Name: "a.txt", Content: "a"}},
	)

	inv := agent.NewInvocation()
	StoreSkillRunOutputFiles(inv, nil)
	StoreSkillRunOutputFiles(inv, []codeexecutor.File{})

	StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: "  ", Content: "ignored"},
	})
}

func TestLookupSkillRunOutputFile_WrongStateType(t *testing.T) {
	inv := agent.NewInvocation()
	inv.SetState(stateKeySkillRunOutputFiles, 1)
	content, mime, ok := LookupSkillRunOutputFile(inv, "a.txt")
	require.False(t, ok)
	require.Empty(t, content)
	require.Empty(t, mime)
}

func TestLookupSkillRunOutputFile_MissingKey(t *testing.T) {
	inv := agent.NewInvocation()
	inv.SetState(stateKeySkillRunOutputFiles, map[string]cachedSkillRunFile{
		"x": {Content: "hi", MIMEType: "text/plain"},
	})
	content, mime, ok := LookupSkillRunOutputFile(inv, "y")
	require.False(t, ok)
	require.Empty(t, content)
	require.Empty(t, mime)
}

func TestSkillRunOutputFiles_WrongOrEmptyState(t *testing.T) {
	require.Nil(t, SkillRunOutputFiles(nil))

	inv := agent.NewInvocation()
	require.Nil(t, SkillRunOutputFiles(inv))

	inv.SetState(stateKeySkillRunOutputFiles, map[string]cachedSkillRunFile{})
	require.Nil(t, SkillRunOutputFiles(inv))

	inv.SetState(stateKeySkillRunOutputFiles, "bad")
	require.Nil(t, SkillRunOutputFiles(inv))
}
