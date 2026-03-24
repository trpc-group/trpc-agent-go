//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveAgentPrompts_DefaultInstruction(t *testing.T) {
	t.Parallel()

	prompts, err := resolveAgentPromptsForDir(runOptions{}, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, defaultAgentInstruction, prompts.Instruction)
	require.Empty(t, prompts.SystemPrompt)
}

func TestResolveAgentPrompts_UsesCurrentWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))
	writeTempPromptFile(t, root, projectDocFileName, "root doc")

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})

	prompts, err := resolveAgentPrompts(runOptions{
		AgentInstruction: "inline",
	})
	require.NoError(t, err)
	require.Equal(t, "root doc\n\ninline", prompts.Instruction)
}

func TestResolveAgentPrompts_GetwdError(t *testing.T) {
	root := t.TempDir()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	require.NoError(t, os.RemoveAll(root))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})

	_, err = resolveAgentPrompts(runOptions{})
	require.Error(t, err)
}

func TestResolveAgentPromptsForDir_ProjectDocsError(t *testing.T) {
	t.Parallel()

	_, err := resolveAgentPromptsForDir(runOptions{
		AgentInstruction: "inline",
	}, " ")
	require.Error(t, err)
}

func TestResolveAgentPrompts_MergesInlineFilesAndDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	file1 := writeTempPromptFile(t, dir, "p1.md", "file 1")
	file2 := writeTempPromptFile(t, dir, "p2.md", "file 2")

	promptDir := filepath.Join(dir, "promptdir")
	require.NoError(t, os.MkdirAll(promptDir, 0o700))

	_ = writeTempPromptFile(t, promptDir, "02_b.md", "dir b")
	_ = writeTempPromptFile(t, promptDir, "01_a.md", "dir a")
	_ = writeTempPromptFile(t, promptDir, "ignore.txt", "ignored")

	opts := runOptions{
		AgentInstruction:      "inline",
		AgentInstructionFiles: file1 + "," + file2,
		AgentInstructionDir:   promptDir,
	}

	prompts, err := resolveAgentPromptsForDir(opts, dir)
	require.NoError(t, err)
	require.Equal(
		t,
		"inline\n\nfile 1\n\nfile 2\n\ndir a\n\ndir b",
		prompts.Instruction,
	)
}

func TestResolveAgentPrompts_InstructionReadErrorReturnsError(t *testing.T) {
	t.Parallel()

	_, err := resolveAgentPromptsForDir(runOptions{
		AgentInstructionFiles: "/no/such/file.md",
	}, t.TempDir())
	require.Error(t, err)
}

func TestResolveAgentPrompts_DirWithoutMDReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = writeTempPromptFile(t, dir, "a.txt", "ignored")

	_, err := resolveAgentPromptsForDir(runOptions{
		AgentSystemPromptDir: dir,
	}, t.TempDir())
	require.Error(t, err)
}

func TestResolveAgentPrompts_MissingFileReturnsError(t *testing.T) {
	t.Parallel()

	_, err := resolveAgentPromptsForDir(runOptions{
		AgentSystemPromptFiles: "/no/such/file.md",
	}, t.TempDir())
	require.Error(t, err)
}

func TestReadAgentPromptFile_EmptyPathReturnsError(t *testing.T) {
	t.Parallel()

	_, err := readAgentPromptFile(" ")
	require.Error(t, err)
}

func TestReadAgentPromptDir_EmptyPathReturnsError(t *testing.T) {
	t.Parallel()

	_, err := readAgentPromptDir(" ")
	require.Error(t, err)
}

func TestReadAgentPromptDir_MissingDirReturnsError(t *testing.T) {
	t.Parallel()

	_, err := readAgentPromptDir("/no/such/dir")
	require.Error(t, err)
}

func TestBuildAgentPrompt_SkipsEmptyPathsAndEmptyContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	emptyFile := writeTempPromptFile(t, dir, "empty.md", " \n")
	nonEmptyFile := writeTempPromptFile(t, dir, "ok.md", "ok")

	p, err := buildAgentPrompt("", []string{
		"",
		" ",
		emptyFile,
		nonEmptyFile,
	}, "")
	require.NoError(t, err)
	require.Equal(t, "ok", p)
}

func TestReadAgentPromptDir_SkipsSubdirsAndEmptyFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))

	_ = writeTempPromptFile(t, dir, "01_empty.md", " \n")
	_ = writeTempPromptFile(t, dir, "02_ok.md", "ok")

	parts, err := readAgentPromptDir(dir)
	require.NoError(t, err)
	require.Equal(t, []string{"ok"}, parts)
}

func writeTempPromptFile(
	t *testing.T,
	dir string,
	name string,
	content string,
) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
