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
	require.Len(t, status.Sections, 1)
	require.Equal(t, "Core Prompt", status.Sections[0].Title)
	require.Len(t, status.Previews, 1)
	require.Equal(t, "Agent Prompt", status.Previews[0].Title)
	require.Contains(t, status.Previews[0].Content, "Instruction")

	require.Equal(
		t,
		"inline instruction\n\ndir instruction",
		status.Bundles[0].ConfiguredValue,
	)
	require.Equal(t, "Instruction", status.Bundles[0].Title)
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

func TestAdminPromptProviderErrorsAndHelpers(t *testing.T) {
	t.Parallel()

	var nilProvider *adminPromptProvider
	_, err := nilProvider.PromptsStatus()
	require.NoError(t, err)
	require.Error(t, nilProvider.SavePromptInline(
		adminPromptInstructionBundle,
		"inline",
	))
	require.Error(t, nilProvider.SavePromptRuntime(
		adminPromptInstructionBundle,
		"runtime",
	))
	require.Error(t, nilProvider.SavePromptFile(
		adminPromptInstructionBundle,
		"/tmp/prompt.md",
		"body",
	))
	require.Error(t, nilProvider.CreatePromptFile(
		adminPromptInstructionBundle,
		"20_extra.md",
		"body",
	))
	require.Error(t, nilProvider.DeletePromptFile(
		adminPromptInstructionBundle,
		"/tmp/prompt.md",
	))

	root := t.TempDir()
	systemPath := filepath.Join(root, "system.md")
	require.NoError(t, os.WriteFile(
		systemPath,
		[]byte("system\n"),
		0o600,
	))
	dirPath := filepath.Join(root, "instruction")
	require.NoError(t, os.MkdirAll(dirPath, 0o700))
	existingPath := filepath.Join(dirPath, "10_base.md")
	require.NoError(t, os.WriteFile(
		existingPath,
		[]byte("base\n"),
		0o600,
	))

	provider := &adminPromptProvider{
		cwd: root,
		opts: runOptions{
			AgentInstructionDir:    dirPath,
			AgentSystemPromptFiles: systemPath,
			AgentSystemPromptDir:   "",
			AgentInstructionFiles:  "",
			AgentInstruction:       "",
			AgentSystemPrompt:      "",
		},
	}

	require.Error(t, provider.SavePromptInline(
		adminPromptInstructionBundle,
		"inline",
	))
	require.Error(t, provider.SavePromptRuntime("unknown", "runtime"))
	require.Error(t, provider.SavePromptFile(
		adminPromptInstructionBundle,
		filepath.Join(root, "missing.md"),
		"body",
	))
	require.Error(t, provider.CreatePromptFile(
		adminPromptInstructionBundle,
		"10_base.md",
		"body",
	))
	require.Error(t, provider.DeletePromptFile(
		adminPromptSystemBundle,
		systemPath,
	))

	_, err = provider.bundleConfiguredPromptLocked("unknown")
	require.Error(t, err)
	_, err = provider.bundleCreateDirLocked(adminPromptSystemBundle)
	require.Error(t, err)
	_, _, err = provider.bundleResolvedPathsLocked("unknown")
	require.Error(t, err)

	found, ok, err := provider.lookupPromptFileLocked(
		adminPromptInstructionBundle,
		existingPath,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, existingPath, found.Path)

	_, ok, err = provider.lookupPromptFileLocked(
		adminPromptInstructionBundle,
		filepath.Join(root, "other.md"),
	)
	require.NoError(t, err)
	require.False(t, ok)

	absPath, err := provider.resolvePromptPath(systemPath)
	require.NoError(t, err)
	require.Equal(t, systemPath, absPath)

	relPath, err := provider.resolvePromptPath("instruction/10_base.md")
	require.NoError(t, err)
	require.Equal(t, existingPath, relPath)

	paths, dir, err := provider.resolvePromptPaths(
		[]string{"instruction/10_base.md", ""},
		"instruction",
	)
	require.NoError(t, err)
	require.Equal(t, []string{existingPath}, paths)
	require.Equal(t, dirPath, dir)

	noCtrl := &adminPromptProvider{
		cwd:  root,
		opts: provider.opts,
	}
	require.NoError(t, noCtrl.applyLocked())

	require.Nil(t, promptOverridePtr(""))
	ptr := promptOverridePtr("  kept  ")
	require.NotNil(t, ptr)
	require.Equal(t, "kept", *ptr)

	require.Error(t, writeAdminPromptFile("", "body"))

	newPath := filepath.Join(root, "nested", "30_extra.md")
	require.NoError(t, writeAdminPromptFile(newPath, "body"))
	data, err := os.ReadFile(newPath)
	require.NoError(t, err)
	require.Equal(t, "body\n", string(data))

	name, err := normalizeAdminPromptFileName("30_extra")
	require.NoError(t, err)
	require.Equal(t, "30_extra.md", name)

	_, err = normalizeAdminPromptFileName("")
	require.Error(t, err)
	_, err = normalizeAdminPromptFileName("../bad")
	require.Error(t, err)
	_, err = normalizeAdminPromptFileName(".")
	require.Error(t, err)
}

func TestAdminPromptProviderAdditionalCoverage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	systemDir := filepath.Join(root, "system_dir")
	require.NoError(t, os.MkdirAll(systemDir, 0o700))
	systemFile := filepath.Join(systemDir, "10_system.md")
	require.NoError(t, os.WriteFile(
		systemFile,
		[]byte("system\n"),
		0o600,
	))

	instructionDir := filepath.Join(root, "instruction")
	require.NoError(t, os.MkdirAll(instructionDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(instructionDir, "10_base.md"),
		[]byte("base\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(instructionDir, "notes.txt"),
		[]byte("skip\n"),
		0o600,
	))
	require.NoError(t, os.MkdirAll(
		filepath.Join(instructionDir, "subdir"),
		0o700,
	))

	opts := runOptions{
		AgentInstructionDir:   instructionDir,
		AgentSystemPromptDir:  systemDir,
		AgentSystemPrompt:     "inline system",
		AgentInstruction:      "",
		AgentInstructionFiles: "",
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

	require.NoError(t, provider.SavePromptRuntime(
		adminPromptSystemBundle,
		"system override",
	))
	require.Equal(
		t,
		"system override",
		provider.controller.Snapshot().SystemPrompt,
	)

	state := provider.bundleStateLocked(
		adminPromptSystemBundle,
		"Agent System Prompt",
		"live system",
		provider.systemOverride,
	)
	require.Equal(t, "system override", state.RuntimeValue)

	brokenPath := filepath.Join(instructionDir, "40_broken.md")
	require.NoError(t, os.Symlink(
		filepath.Join(root, "missing.md"),
		brokenPath,
	))

	fileProvider := &adminPromptProvider{
		cwd:  root,
		opts: opts,
	}
	files, err := fileProvider.bundleFilesLocked(
		adminPromptInstructionBundle,
	)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "10 Base", files[0].Label)
	require.Equal(t, "40 Broken", files[1].Label)
	require.NotEmpty(t, files[1].Error)

	missingDirProvider := &adminPromptProvider{
		cwd: root,
		opts: runOptions{
			AgentInstructionDir: filepath.Join(root, "does-not-exist"),
		},
	}
	files, err = missingDirProvider.bundleFilesLocked(
		adminPromptInstructionBundle,
	)
	require.NoError(t, err)
	require.Empty(t, files)

	fileAsDir := filepath.Join(root, "not-a-dir")
	require.NoError(t, os.WriteFile(
		fileAsDir,
		[]byte("body\n"),
		0o600,
	))
	invalidDirProvider := &adminPromptProvider{
		cwd: root,
		opts: runOptions{
			AgentInstructionDir: fileAsDir,
		},
	}
	_, err = invalidDirProvider.bundleFilesLocked(
		adminPromptInstructionBundle,
	)
	require.Error(t, err)

	emptyProvider := &adminPromptProvider{
		cwd:  root,
		opts: runOptions{},
	}
	instruction, err := emptyProvider.bundleConfiguredPromptLocked(
		adminPromptInstructionBundle,
	)
	require.NoError(t, err)
	require.Equal(t, defaultAgentInstruction, instruction)

	state = emptyProvider.bundleStateLocked(
		"unknown",
		"Unknown",
		"",
		nil,
	)
	require.NotEmpty(t, state.LoadError)

	cwdProvider := &adminPromptProvider{}
	path, err := cwdProvider.resolvePromptPath("relative.md")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(path))
}
