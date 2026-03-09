//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWorkspaceFileTools_WriteReadReplaceList(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	run := NewRunTool(repo, localexec.New())
	writeTool := NewWorkspaceWriteFileTool(run)
	readTool := NewWorkspaceReadFileTool(run)
	replaceTool := NewWorkspaceReplaceContentTool(run)
	listTool := NewWorkspaceListDirTool(run)

	ctx := context.Background()
	writeArgs := workspaceWriteFileInput{
		Skill:     testSkillName,
		Path:      "out/note.txt",
		Content:   "hello",
		Overwrite: true,
	}
	wres := mustCallTool[workspaceWriteFileOutput](
		t, ctx, writeTool, writeArgs,
	)
	require.True(t, wres.Changed)
	require.Equal(t, 5, wres.BytesWritten)

	readArgs := workspaceReadFileInput{
		Skill: testSkillName,
		Path:  "out/note.txt",
	}
	rres := mustCallTool[workspaceReadFileOutput](
		t, ctx, readTool, readArgs,
	)
	require.Equal(t, "hello", rres.Content)
	require.Equal(t, 1, rres.StartLine)
	require.Equal(t, 1, rres.EndLine)

	repArgs := workspaceReplaceContentInput{
		Skill:      testSkillName,
		Path:       "out/note.txt",
		OldString:  "hello",
		NewString:  "world",
	}
	repRes := mustCallTool[workspaceReplaceContentOutput](
		t, ctx, replaceTool, repArgs,
	)
	require.True(t, repRes.Changed)
	require.Equal(t, 1, repRes.Replacements)

	rres2 := mustCallTool[workspaceReadFileOutput](
		t, ctx, readTool, readArgs,
	)
	require.Equal(t, "world", rres2.Content)

	listArgs := workspaceListDirInput{
		Skill: testSkillName,
		Path:  "out",
	}
	lres := mustCallTool[workspaceListDirOutput](
		t, ctx, listTool, listArgs,
	)
	require.Contains(t, lres.Files, "out/note.txt")
}

func TestWorkspaceFileTools_RejectInvalidPath(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	run := NewRunTool(repo, localexec.New())
	writeTool := NewWorkspaceWriteFileTool(run)

	ctx := context.Background()
	args := workspaceWriteFileInput{
		Skill:     testSkillName,
		Path:      "../bad.txt",
		Content:   "x",
		Overwrite: true,
	}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = writeTool.Call(ctx, b)
	require.Error(t, err)
	require.Contains(t, err.Error(), "path")
}

func mustCallTool[T any](
	t *testing.T,
	ctx context.Context,
	tl tool.CallableTool,
	args any,
) T {
	t.Helper()
	b, err := json.Marshal(args)
	require.NoError(t, err)
	res, err := tl.Call(ctx, b)
	require.NoError(t, err)
	enc, err := json.Marshal(res)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(enc, &out))
	return out
}
