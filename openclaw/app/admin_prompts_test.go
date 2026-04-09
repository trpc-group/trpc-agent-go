//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

func TestAdminPromptProviderStatus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	instructionDir := filepath.Join(root, "instruction")
	systemFile := filepath.Join(root, "system.md")
	require.NoError(t, os.MkdirAll(instructionDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(instructionDir, "10_base.md"),
		[]byte("dir instruction\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		systemFile,
		[]byte("system from file\n"),
		0o600,
	))

	opts := runOptions{
		AgentInstruction:       "inline instruction",
		AgentInstructionDir:    instructionDir,
		AgentSystemPromptFiles: systemFile,
	}
	prompts, err := resolveAgentPromptsForDir(opts, root)
	require.NoError(t, err)

	provider := &adminPromptProvider{
		cwd:  root,
		opts: opts,
		controller: newRuntimePromptController(
			llmagent.New(
				"test",
				llmagent.WithInstruction(prompts.Instruction),
				llmagent.WithGlobalInstruction(prompts.SystemPrompt),
			),
			prompts.Instruction,
			prompts.SystemPrompt,
		),
	}

	status, err := provider.PromptsStatus()
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.Len(t, status.Bundles, 2)

	require.Equal(
		t,
		"inline instruction\n\ndir instruction",
		status.Bundles[0].ConfiguredValue,
	)
	require.Len(t, status.Bundles[0].Files, 1)
	require.True(t, status.Bundles[0].CreateEnabled)

	require.Equal(
		t,
		"system from file",
		status.Bundles[1].ConfiguredValue,
	)
	require.Len(t, status.Bundles[1].Files, 1)
	require.False(t, status.Bundles[1].CreateEnabled)
}

func TestAdminPromptProviderSavePromptFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	instructionPath := filepath.Join(root, "instruction.md")
	require.NoError(t, os.WriteFile(
		instructionPath,
		[]byte("before\n"),
		0o600,
	))

	opts := runOptions{
		AgentInstructionFiles: instructionPath,
	}
	prompts, err := resolveAgentPromptsForDir(opts, root)
	require.NoError(t, err)

	provider := &adminPromptProvider{
		cwd:  root,
		opts: opts,
		controller: newRuntimePromptController(
			llmagent.New(
				"test",
				llmagent.WithInstruction(prompts.Instruction),
				llmagent.WithGlobalInstruction(prompts.SystemPrompt),
			),
			prompts.Instruction,
			prompts.SystemPrompt,
		),
	}

	require.NoError(t, provider.SavePromptFile(
		adminPromptInstructionBundle,
		instructionPath,
		"after",
	))

	data, err := os.ReadFile(instructionPath)
	require.NoError(t, err)
	require.Equal(t, "after\n", string(data))
	require.Equal(
		t,
		"after",
		provider.controller.Snapshot().Instruction,
	)
}

func TestAdminPromptProviderCreateAndDeletePromptFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	instructionDir := filepath.Join(root, "instruction")
	require.NoError(t, os.MkdirAll(instructionDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(instructionDir, "10_base.md"),
		[]byte("base\n"),
		0o600,
	))

	opts := runOptions{AgentInstructionDir: instructionDir}
	prompts, err := resolveAgentPromptsForDir(opts, root)
	require.NoError(t, err)

	provider := &adminPromptProvider{
		cwd:  root,
		opts: opts,
		controller: newRuntimePromptController(
			llmagent.New(
				"test",
				llmagent.WithInstruction(prompts.Instruction),
				llmagent.WithGlobalInstruction(prompts.SystemPrompt),
			),
			prompts.Instruction,
			prompts.SystemPrompt,
		),
	}

	require.NoError(t, provider.CreatePromptFile(
		adminPromptInstructionBundle,
		"20_extra",
		"extra",
	))
	require.Contains(
		t,
		provider.controller.Snapshot().Instruction,
		"extra",
	)

	extraPath := filepath.Join(instructionDir, "20_extra.md")
	require.NoError(t, provider.DeletePromptFile(
		adminPromptInstructionBundle,
		extraPath,
	))
	require.NoFileExists(t, extraPath)
	require.NotContains(
		t,
		provider.controller.Snapshot().Instruction,
		"extra",
	)
}

func TestAdminPromptProviderSavePromptRuntime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	instructionPath := filepath.Join(root, "instruction.md")
	require.NoError(t, os.WriteFile(
		instructionPath,
		[]byte("from file\n"),
		0o600,
	))

	opts := runOptions{AgentInstructionFiles: instructionPath}
	prompts, err := resolveAgentPromptsForDir(opts, root)
	require.NoError(t, err)

	provider := &adminPromptProvider{
		cwd:  root,
		opts: opts,
		controller: newRuntimePromptController(
			llmagent.New(
				"test",
				llmagent.WithInstruction(prompts.Instruction),
				llmagent.WithGlobalInstruction(prompts.SystemPrompt),
			),
			prompts.Instruction,
			prompts.SystemPrompt,
		),
	}

	require.NoError(t, provider.SavePromptRuntime(
		adminPromptInstructionBundle,
		"runtime override",
	))
	require.Equal(
		t,
		"runtime override",
		provider.controller.Snapshot().Instruction,
	)

	require.NoError(t, provider.SavePromptRuntime(
		adminPromptInstructionBundle,
		"",
	))
	require.Equal(
		t,
		"from file",
		provider.controller.Snapshot().Instruction,
	)
}
