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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestMaybeCreateEvolutionService_NilRepo(t *testing.T) {
	opts := runOptions{EvolutionEnabled: true, StateDir: t.TempDir()}
	svc := maybeCreateEvolutionService(opts, nil)
	assert.Nil(t, svc, "should return nil when repo is nil")
}

func TestMaybeCreateEvolutionService_NoStateDir(t *testing.T) {
	repo, err := skill.NewFSRepository(t.TempDir())
	require.NoError(t, err)

	opts := runOptions{EvolutionEnabled: true, StateDir: ""}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.Nil(t, svc, "should return nil when state dir is empty")
}

func TestMaybeCreateEvolutionService_Disabled(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	opts := runOptions{
		StateDir:  dir,
		ModelMode: modeOpenAI,
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.Nil(t, svc, "should require explicit evolution opt-in")
}

func TestMaybeCreateEvolutionService_MockMode(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	opts := runOptions{
		EvolutionEnabled: true,
		StateDir:         dir,
		ModelMode:        modeMock,
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.NotNil(t, svc, "should reuse the configured mock model")
	t.Cleanup(func() { _ = svc.Close() })
}

func TestMaybeCreateEvolutionService_Success(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	opts := runOptions{
		EvolutionEnabled: true,
		StateDir:         dir,
		ModelMode:        modeOpenAI,
		OpenAIModel:      "gpt-4o-mini",
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.NotNil(t, svc, "should create evolution service with valid config")
	t.Cleanup(func() { _ = svc.Close() })

	// Verify directories were created.
	_, err = os.Stat(filepath.Join(dir, defaultSkillsDir, "evolution"))
	assert.NoError(t, err, "skills/evolution dir should be created")
	_, err = os.Stat(filepath.Join(dir, "evolution", "revisions"))
	assert.NoError(t, err, "evolution/revisions dir should be created")
}

func TestMaybeCreateEvolutionService_DefaultModel(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	opts := runOptions{
		EvolutionEnabled: true,
		StateDir:         dir,
		ModelMode:        modeOpenAI,
		OpenAIModel:      defaultOpenAIModel,
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.NotNil(t, svc, "should create service with default model name")
	t.Cleanup(func() { _ = svc.Close() })
}

func TestMaybeCreateEvolutionService_WithBaseURL(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	opts := runOptions{
		EvolutionEnabled: true,
		StateDir:         dir,
		ModelMode:        modeOpenAI,
		OpenAIModel:      "gpt-4o-mini",
		OpenAIBaseURL:    "https://custom.api/v1",
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.NotNil(t, svc, "should create service with custom base URL")
	t.Cleanup(func() { _ = svc.Close() })
}

func TestMaybeCreateEvolutionService_InvalidModelConfig(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	opts := runOptions{
		EvolutionEnabled: true,
		StateDir:         dir,
		ModelMode:        modeOpenAI,
		OpenAIModel:      "gpt-4o-mini",
		OpenAIVariant:    "unsupported",
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.Nil(t, svc, "should fail when the shared model config is invalid")
}

func TestMaybeCreateEvolutionService_InvalidManagedDir(t *testing.T) {
	dir := t.TempDir()
	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	// Make skills path point to a file (cannot mkdir over a file).
	blockerPath := filepath.Join(dir, defaultSkillsDir)
	require.NoError(t, os.WriteFile(blockerPath, []byte("blocker"), 0o644))

	// Change permissions to read-only so MkdirAll fails.
	require.NoError(t, os.Chmod(blockerPath, 0o444))
	defer os.Chmod(blockerPath, 0o644)

	opts := runOptions{
		EvolutionEnabled: true,
		StateDir:         dir,
		ModelMode:        modeOpenAI,
		OpenAIModel:      "gpt-4o-mini",
	}
	svc := maybeCreateEvolutionService(opts, repo)
	assert.Nil(t, svc, "should return nil when skills dir cannot be created")
}
