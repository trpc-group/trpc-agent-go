//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// relaxGate is the accept-path preset used by the promotion tests.
func relaxGate(config *Config) {
	config.Gate.ProtectedCases = nil
	config.Gate.MaxRegressedCases = 1
	config.Gate.MaxNewHardFails = 1
}

// acceptedRun executes one accepting fake-mode run without write-back, so the
// promotion has real candidate artifacts to install.
func acceptedRun(t *testing.T, dataDir, outputDir string) (*Config, *resolvedInputs, *Result) {
	t.Helper()
	config, inputs := loadInputsAt(t, dataDir)
	relaxGate(config)
	result := runExamplePipeline(t, config, inputs, dataDir, outputDir, false)
	require.Equal(t, StatusAccepted, result.Status)
	return config, inputs, result
}

// TestPromoteInstallsBothArtifacts locks the promotion contract: one command
// installs the accepted prompt and the accepted effective profile, and the
// next run starts from exactly the combination that passed the gate.
func TestPromoteInstallsBothArtifacts(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	config, inputs, result := acceptedRun(t, dataDir, outputDir)
	require.NotEmpty(t, result.CandidatePromptPath)

	promotion, err := promoteCandidate(config, outputDir)
	require.NoError(t, err)
	assert.True(t, promotion.PromptPromoted)
	assert.Equal(t, inputs.promptSourcePath, promotion.PromptSourcePath)
	assert.Equal(t, inputs.baselineProfilePath, promotion.BaselineProfilePath)

	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, result.CandidatePrompt+"\n", string(promptAfter))
	candidateProfile, err := os.ReadFile(result.CandidateProfilePath)
	require.NoError(t, err)
	promotedProfile, err := os.ReadFile(inputs.baselineProfilePath)
	require.NoError(t, err)
	assert.Equal(t, string(candidateProfile), string(promotedProfile))

	// The promoted baseline is what the next run resolves: instruction from the
	// prompt source, non-instruction overrides from the profile.
	_, reloaded := loadInputsAt(t, dataDir)
	assert.Contains(t, reloaded.baselinePrompt, OptimizedMarker)
	assert.Equal(t, ImprovedToolDescriptions[ToolQueryOrder], reloaded.baselineToolDescriptions[ToolQueryOrder])
}

// TestPromoteFailureLeavesBaselineUntouched locks failure atomicity: when the
// profile cannot be written, the prompt source must not be left updated — that
// combination (new prompt, old profile) never passed the gate.
func TestPromoteFailureLeavesBaselineUntouched(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	config, inputs, _ := acceptedRun(t, dataDir, outputDir)
	originalPrompt, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)

	// A directory squatting on the baseline profile path fails the second (and
	// last) staged write, after the prompt source was already replaced.
	require.NoError(t, os.MkdirAll(inputs.baselineProfilePath, 0o755))
	_, err = promoteCandidate(config, outputDir)
	require.Error(t, err)

	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, string(originalPrompt), string(promptAfter),
		"a failed promotion must roll the prompt source back")
}

// TestPromoteHandlesPathsWithWhitespaceAndMetacharacters: the promotion takes
// its paths from the configuration, so a data dir whose name contains spaces
// and shell metacharacters promotes exactly like any other.
func TestPromoteHandlesPathsWithWhitespaceAndMetacharacters(t *testing.T) {
	dataDir := copyTestDataInto(t, filepath.Join(t.TempDir(), "my data $(id); rm -rf ~"))
	outputDir := filepath.Join(t.TempDir(), "out dir & more")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	config, inputs, result := acceptedRun(t, dataDir, outputDir)

	promotion, err := promoteCandidate(config, outputDir)
	require.NoError(t, err)
	assert.True(t, promotion.PromptPromoted)
	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, result.CandidatePrompt+"\n", string(promptAfter))
	assert.FileExists(t, inputs.baselineProfilePath)
}

// TestPromoteToolOnlyKeepsBaselinePrompt: with no candidate prompt published
// (the accepted candidate touched no instruction surface), only the profile is
// promoted and the baseline prompt file stays byte-identical.
func TestPromoteToolOnlyKeepsBaselinePrompt(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	config, inputs, _ := acceptedRun(t, dataDir, outputDir)
	// Drop the prompt artifact to emulate a tool-only acceptance whose profile
	// carries the unchanged baseline instruction.
	require.NoError(t, os.Remove(filepath.Join(outputDir, candidatePromptFileName)))
	writeCandidateProfileInstruction(t, outputDir, config, inputs.baselinePrompt)
	originalPrompt, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)

	promotion, err := promoteCandidate(config, outputDir)
	require.NoError(t, err)
	assert.False(t, promotion.PromptPromoted)
	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, string(originalPrompt), string(promptAfter))
	assert.FileExists(t, inputs.baselineProfilePath)
}

// TestPromoteRejectsInconsistentArtifacts: the prompt and the profile are
// published together, so a disagreement means one of them was edited or came
// from another run; promoting it would deploy an unevaluated combination.
func TestPromoteRejectsInconsistentArtifacts(t *testing.T) {
	dataDir := copyTestData(t)
	outputDir := t.TempDir()
	config, inputs, _ := acceptedRun(t, dataDir, outputDir)
	originalPrompt, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(outputDir, candidatePromptFileName), []byte("手工改过的指令\n"), 0o644))

	_, err = promoteCandidate(config, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disagree")
	promptAfter, err := os.ReadFile(inputs.promptSourcePath)
	require.NoError(t, err)
	assert.Equal(t, string(originalPrompt), string(promptAfter))
	assert.NoFileExists(t, inputs.baselineProfilePath)

	// The tool-only shape fails closed the same way: without a prompt artifact
	// the profile's instruction must match the prompt source still in force.
	require.NoError(t, os.Remove(filepath.Join(outputDir, candidatePromptFileName)))
	_, err = promoteCandidate(config, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "differs from baseline prompt")
	assert.NoFileExists(t, inputs.baselineProfilePath)
}

// TestPromoteWithoutAcceptedCandidate: a rejected (or never executed) run
// leaves no candidate profile, and promotion says so instead of writing.
func TestPromoteWithoutAcceptedCandidate(t *testing.T) {
	dataDir := copyTestData(t)
	config, inputs := loadInputsAt(t, dataDir)
	_, err := promoteCandidate(config, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to promote")
	assert.NoFileExists(t, inputs.baselineProfilePath)
}

// writeCandidateProfileInstruction rewrites the published candidate profile so
// its instruction override carries the given text.
func writeCandidateProfileInstruction(t *testing.T, outputDir string, config *Config, instruction string) {
	t.Helper()
	surfaceID, err := instructionTargetSurfaceID(config)
	require.NoError(t, err)
	profilePath := filepath.Join(outputDir, candidateProfileFileName)
	content, err := os.ReadFile(profilePath)
	require.NoError(t, err)
	profile := &promptiter.Profile{}
	require.NoError(t, json.Unmarshal(content, profile))
	profile.Overrides = upsertOverride(profile.Overrides, promptiter.SurfaceOverride{
		SurfaceID: surfaceID,
		Value:     astructureTextValue(instruction),
	})
	updated, err := json.MarshalIndent(profile, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(profilePath, append(updated, '\n'), 0o644))
}
