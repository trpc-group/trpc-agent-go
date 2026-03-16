//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactmem "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/session"
	skillsrepo "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const workspaceTestSkill = "echoer"

func createWorkspaceToolRepo(t *testing.T) skillsrepo.Repository {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, workspaceTestSkill)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillBody := "---\nname: echoer\n" +
		"description: simple echo skill\n---\n\nbody\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte(skillBody),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "README.md"),
		[]byte("hello docs\n"),
		0o644,
	))
	repo, err := skillsrepo.NewFSRepository(root)
	require.NoError(t, err)
	return repo
}

func decodeToolOutput[T any](t *testing.T, v any) T {
	t.Helper()
	var out T
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func TestWorkspaceReadFileTool_ReadsStagedSkillFile(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	tl := NewWorkspaceReadFileTool(runTool)

	args, err := json.Marshal(map[string]any{
		"path": "skills/echoer/SKILL.md",
	})
	require.NoError(t, err)

	res, err := tl.(tool.CallableTool).Call(context.Background(), args)
	require.NoError(t, err)
	out := decodeToolOutput[workspaceReadFileOutput](t, res)
	require.Equal(t, "skills/echoer/SKILL.md", out.Path)
	require.Contains(t, out.Content, "simple echo skill")
	require.Contains(t, out.Message, "Successfully read")
}

func TestWorkspaceListDirTool_ListsAvailableSkills(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	tl := NewWorkspaceListDirTool(runTool)

	args, err := json.Marshal(map[string]any{
		"path": "skills",
	})
	require.NoError(t, err)

	res, err := tl.(tool.CallableTool).Call(context.Background(), args)
	require.NoError(t, err)
	out := decodeToolOutput[workspaceListDirOutput](t, res)
	require.Equal(t, "skills", out.Path)
	require.Contains(t, out.Entries, workspaceDirEntry{
		Name: "echoer",
		Path: "skills/echoer",
		Kind: "directory",
	})
}

func TestWorkspaceListDirTool_ListsWorkspaceRoot(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	tl := NewWorkspaceListDirTool(runTool)

	res, err := tl.(tool.CallableTool).Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	out := decodeToolOutput[workspaceListDirOutput](t, res)
	require.Equal(t, ".", out.Path)

	names := make(map[string]bool)
	for _, entry := range out.Entries {
		names[entry.Name] = true
	}
	require.True(t, names[codeexecutor.DirSkills])
	require.True(t, names[codeexecutor.DirWork])
	require.True(t, names[codeexecutor.DirOut])
	require.True(t, names[codeexecutor.DirRuns])
}

func TestWorkspaceReadFileTool_RejectsBinaryFiles(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	eng := runTool.ensureEngine()
	ws, err := runTool.createWorkspace(
		context.Background(),
		eng,
		workspaceToolsWorkspaceID,
	)
	require.NoError(t, err)
	require.NoError(t, eng.FS().PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "work/blob.bin",
		Content: []byte{0x00, 0x01, 0x02},
		Mode:    0o644,
	}}))

	tl := NewWorkspaceReadFileTool(runTool)
	args, err := json.Marshal(map[string]any{
		"path": "work/blob.bin",
	})
	require.NoError(t, err)

	_, err = tl.(tool.CallableTool).Call(context.Background(), args)
	require.Error(t, err)
	require.Contains(t, err.Error(), "UTF-8 text files")
}

func TestWorkspaceListDirTool_RejectsSymlinkEscape(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	eng := runTool.ensureEngine()
	ws, err := runTool.createWorkspace(
		context.Background(),
		eng,
		workspaceToolsWorkspaceID,
	)
	require.NoError(t, err)

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(outside, "secret.txt"),
		[]byte("secret\n"),
		0o644,
	))
	require.NoError(t, os.Symlink(
		outside,
		filepath.Join(ws.Path, codeexecutor.DirWork, "escape"),
	))

	tl := NewWorkspaceListDirTool(runTool)
	args, err := json.Marshal(map[string]any{
		"path": "work/escape",
	})
	require.NoError(t, err)

	_, err = tl.(tool.CallableTool).Call(context.Background(), args)
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes workspace root")
}

func TestWorkspaceWriteFileTool_WritesTextFile(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	tl := NewWorkspaceWriteFileTool(runTool)

	args, err := json.Marshal(map[string]any{
		"path":      "work/note.txt",
		"content":   "hello workspace\n",
		"overwrite": true,
	})
	require.NoError(t, err)

	res, err := tl.(tool.CallableTool).Call(context.Background(), args)
	require.NoError(t, err)
	out := decodeToolOutput[workspaceWriteFileOutput](t, res)
	require.Equal(t, "work/note.txt", out.Path)

	readTool := NewWorkspaceReadFileTool(runTool)
	readArgs, err := json.Marshal(map[string]any{"path": "work/note.txt"})
	require.NoError(t, err)
	readRes, err := readTool.(tool.CallableTool).Call(context.Background(), readArgs)
	require.NoError(t, err)
	readOut := decodeToolOutput[workspaceReadFileOutput](t, readRes)
	require.Equal(t, "hello workspace\n", readOut.Content)
}

func TestWorkspaceWriteFileTool_OverwriteBehavior(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	tl := NewWorkspaceWriteFileTool(runTool)

	firstArgs, err := json.Marshal(map[string]any{
		"path":      "work/note.txt",
		"content":   "first\n",
		"overwrite": true,
	})
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), firstArgs)
	require.NoError(t, err)

	noOverwriteArgs, err := json.Marshal(map[string]any{
		"path":      "work/note.txt",
		"content":   "second\n",
		"overwrite": false,
	})
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), noOverwriteArgs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "overwrite=false")

	overwriteArgs, err := json.Marshal(map[string]any{
		"path":      "work/note.txt",
		"content":   "second\n",
		"overwrite": true,
	})
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), overwriteArgs)
	require.NoError(t, err)

	readTool := NewWorkspaceReadFileTool(runTool)
	readArgs, err := json.Marshal(map[string]any{"path": "work/note.txt"})
	require.NoError(t, err)
	readRes, err := readTool.(tool.CallableTool).Call(context.Background(), readArgs)
	require.NoError(t, err)
	readOut := decodeToolOutput[workspaceReadFileOutput](t, readRes)
	require.Equal(t, "second\n", readOut.Content)
}

func TestWorkspaceReplaceContentTool_ReplacesText(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	eng := runTool.ensureEngine()
	ws, err := runTool.createWorkspace(context.Background(), eng, workspaceToolsWorkspaceID)
	require.NoError(t, err)
	require.NoError(t, eng.FS().PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "out/report.txt",
		Content: []byte("alpha beta alpha\n"),
		Mode:    0o644,
	}}))

	tl := NewWorkspaceReplaceContentTool(runTool)
	args, err := json.Marshal(map[string]any{
		"path":             "out/report.txt",
		"old_string":       "alpha",
		"new_string":       "omega",
		"num_replacements": 1,
	})
	require.NoError(t, err)

	res, err := tl.(tool.CallableTool).Call(context.Background(), args)
	require.NoError(t, err)
	out := decodeToolOutput[workspaceReplaceContentOutput](t, res)
	require.Equal(t, 1, out.ReplacedCount)
	require.Equal(t, 2, out.TotalMatches)

	readTool := NewWorkspaceReadFileTool(runTool)
	readArgs, err := json.Marshal(map[string]any{"path": "out/report.txt"})
	require.NoError(t, err)
	readRes, err := readTool.(tool.CallableTool).Call(context.Background(), readArgs)
	require.NoError(t, err)
	readOut := decodeToolOutput[workspaceReadFileOutput](t, readRes)
	require.Equal(t, "omega beta alpha\n", readOut.Content)
}

func TestWorkspaceReplaceContentTool_NotFound(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	eng := runTool.ensureEngine()
	ws, err := runTool.createWorkspace(context.Background(), eng, workspaceToolsWorkspaceID)
	require.NoError(t, err)
	require.NoError(t, eng.FS().PutFiles(context.Background(), ws, []codeexecutor.PutFile{{
		Path:    "out/report.txt",
		Content: []byte("alpha beta alpha\n"),
		Mode:    0o644,
	}}))

	tl := NewWorkspaceReplaceContentTool(runTool)
	args, err := json.Marshal(map[string]any{
		"path":       "out/report.txt",
		"old_string": "missing",
		"new_string": "omega",
	})
	require.NoError(t, err)

	res, err := tl.(tool.CallableTool).Call(context.Background(), args)
	require.NoError(t, err)
	out := decodeToolOutput[workspaceReplaceContentOutput](t, res)
	require.Equal(t, 0, out.ReplacedCount)
	require.Equal(t, 0, out.TotalMatches)
	require.Contains(t, out.Message, "not found")

	readTool := NewWorkspaceReadFileTool(runTool)
	readArgs, err := json.Marshal(map[string]any{"path": "out/report.txt"})
	require.NoError(t, err)
	readRes, err := readTool.(tool.CallableTool).Call(context.Background(), readArgs)
	require.NoError(t, err)
	readOut := decodeToolOutput[workspaceReadFileOutput](t, readRes)
	require.Equal(t, "alpha beta alpha\n", readOut.Content)
}

func TestWorkspaceWriteFileTool_RejectsInputsDir(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	tl := NewWorkspaceWriteFileTool(runTool)

	args, err := json.Marshal(map[string]any{
		"path":    "work/inputs/data.txt",
		"content": "x",
	})
	require.NoError(t, err)

	_, err = tl.(tool.CallableTool).Call(context.Background(), args)
	require.Error(t, err)
	require.Contains(t, err.Error(), "writable workspace roots")
}

func TestWorkspaceListDirTool_RejectsMissingAndFilePaths(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())

	listTool := NewWorkspaceListDirTool(runTool)
	missingArgs, err := json.Marshal(map[string]any{"path": "out/missing"})
	require.NoError(t, err)
	_, err = listTool.(tool.CallableTool).Call(context.Background(), missingArgs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "workspace path not found")

	readWriteTool := NewWorkspaceWriteFileTool(runTool)
	writeArgs, err := json.Marshal(map[string]any{
		"path":      "out/report.txt",
		"content":   "hello\n",
		"overwrite": true,
	})
	require.NoError(t, err)
	_, err = readWriteTool.(tool.CallableTool).Call(context.Background(), writeArgs)
	require.NoError(t, err)

	fileArgs, err := json.Marshal(map[string]any{"path": "out/report.txt"})
	require.NoError(t, err)
	_, err = listTool.(tool.CallableTool).Call(context.Background(), fileArgs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestArtifactPublishTool_PublishesWorkspaceFile(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	svc := artifactmem.NewService()
	sess := session.NewSession("app", "user", "sess")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	eng := runTool.ensureEngine()
	ws, err := runTool.createWorkspace(ctx, eng, workspaceToolsWorkspaceID)
	require.NoError(t, err)
	require.NoError(t, eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "out/report.txt",
		Content: []byte("artifact payload\n"),
		Mode:    0o644,
	}}))

	tl := NewArtifactPublishTool(runTool)
	args, err := json.Marshal(map[string]any{
		"paths":           []string{"out/report.txt"},
		"artifact_prefix": "published/",
	})
	require.NoError(t, err)

	res, err := tl.(tool.CallableTool).Call(ctx, args)
	require.NoError(t, err)
	out := decodeToolOutput[artifactPublishOutput](t, res)
	require.Len(t, out.Published, 1)
	require.Equal(t, "published/out/report.txt", out.Published[0].ArtifactName)
	require.Equal(t, "artifact://published/out/report.txt@0", out.Published[0].Ref)

	art, err := svc.LoadArtifact(ctx, artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}, "published/out/report.txt", nil)
	require.NoError(t, err)
	require.NotNil(t, art)
	require.Equal(t, []byte("artifact payload\n"), art.Data)
}

func TestArtifactPublishTool_RejectsGlobAndLargeFiles(t *testing.T) {
	repo := createWorkspaceToolRepo(t)
	runTool := NewRunTool(repo, localexec.New())
	svc := artifactmem.NewService()
	sess := session.NewSession("app", "user", "sess")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := NewArtifactPublishTool(runTool)

	globArgs, err := json.Marshal(map[string]any{
		"paths": []string{"out/*.txt"},
	})
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(ctx, globArgs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "explicit file paths")

	eng := runTool.ensureEngine()
	ws, err := runTool.createWorkspace(ctx, eng, workspaceToolsWorkspaceID)
	require.NoError(t, err)
	large := make([]byte, 4*1024*1024+1)
	for i := range large {
		large[i] = 'a'
	}
	require.NoError(t, eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "out/large.txt",
		Content: large,
		Mode:    0o644,
	}}))

	largeArgs, err := json.Marshal(map[string]any{
		"paths": []string{"out/large.txt"},
	})
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(ctx, largeArgs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "up to 4 MiB")
}
