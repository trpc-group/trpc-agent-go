//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// Promotion installs the artifacts of an accepted run as the next run's
// baseline: the accepted instruction text into the configured prompt source,
// and the accepted effective profile into baseline_profile.json next to it.
//
// It exists so promotion never has to be spelled as two shell copies. Copying
// the prompt first and the profile second is not failure-atomic: when the
// second copy fails, baseline_prompt.txt already carries the new instruction
// while baseline_profile.json still holds the old (or no) overrides, so the
// next run starts from a combination that never passed the gate. Taking the
// paths from the configuration instead of from generated shell text also keeps
// whitespace and shell metacharacters in those paths from splitting the
// promotion or being interpreted.

// PromotionResult reports what a promotion published.
type PromotionResult struct {
	// PromptSourcePath is the baseline prompt file the promotion targets.
	PromptSourcePath string
	// BaselineProfilePath is the baseline profile file the promotion writes.
	BaselineProfilePath string
	// PromptPromoted is false for a tool-only acceptance, where the accepted
	// profile carries no instruction change and the baseline prompt file stays
	// untouched.
	PromptPromoted bool
}

// promoteCandidate validates the accepted candidate artifacts under outputDir
// and publishes them as the new baseline in one rollback unit. Both artifacts
// must agree on the instruction text: the pipeline writes them together, so a
// disagreement means one of them was edited or belongs to another run, and
// promoting them would deploy a combination no gate ever saw.
func promoteCandidate(config *Config, outputDir string) (*PromotionResult, error) {
	if config == nil {
		return nil, errors.New("promotion config is nil")
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, errors.New("output dir is empty")
	}
	promptSourcePath := config.PromptSourcePath()
	baselineProfilePath := filepath.Join(filepath.Dir(promptSourcePath), baselineProfileFileName)
	profilePath := filepath.Join(outputDir, candidateProfileFileName)
	profileContent, profileInstruction, err := loadCandidateProfile(profilePath, config)
	if err != nil {
		return nil, err
	}
	staged, promptPromoted, err := stagePromotion(
		outputDir, promptSourcePath, profilePath, profileInstruction,
	)
	if err != nil {
		return nil, err
	}
	// The profile lands last so a failure on it rolls the prompt back too.
	staged = append(staged, stagedFile{path: baselineProfilePath, content: profileContent})
	if err := publishFiles(staged); err != nil {
		return nil, err
	}
	return &PromotionResult{
		PromptSourcePath:    promptSourcePath,
		BaselineProfilePath: baselineProfilePath,
		PromptPromoted:      promptPromoted,
	}, nil
}

// loadCandidateProfile reads and validates the accepted effective profile,
// returning its raw bytes (published verbatim) and its instruction text.
func loadCandidateProfile(profilePath string, config *Config) ([]byte, string, error) {
	content, err := os.ReadFile(profilePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf(
			"nothing to promote: %q does not exist; only an accepted run publishes it", profilePath)
	}
	if err != nil {
		return nil, "", fmt.Errorf("read candidate profile %q: %w", profilePath, err)
	}
	profile := &promptiter.Profile{}
	if err := json.Unmarshal(content, profile); err != nil {
		return nil, "", fmt.Errorf("decode candidate profile %q: %w", profilePath, err)
	}
	instructionSurfaceID, err := instructionTargetSurfaceID(config)
	if err != nil {
		return nil, "", err
	}
	instruction := ""
	for _, override := range profile.Overrides {
		if override.SurfaceID == instructionSurfaceID && override.Value.Text != nil {
			instruction = strings.TrimSpace(*override.Value.Text)
			break
		}
	}
	// The pipeline always refreshes the instruction override from the effective
	// instruction text, so a profile without one is not a publishable baseline.
	if instruction == "" {
		return nil, "", fmt.Errorf(
			"candidate profile %q carries no instruction text for surface %q",
			profilePath, instructionSurfaceID)
	}
	return content, instruction, nil
}

// stagePromotion stages the prompt side of the promotion. When the accepted
// run published a prompt artifact, its text must match the profile's
// instruction override and replaces the prompt source. Without one (a tool-only
// acceptance) the prompt source must already carry the profile's instruction:
// it stays in force untouched, and a mismatch means the prompt source drifted
// since the run, so the profile would reinstate an instruction the current
// baseline no longer uses.
func stagePromotion(
	outputDir string,
	promptSourcePath string,
	profilePath string,
	profileInstruction string,
) ([]stagedFile, bool, error) {
	promptPath := filepath.Join(outputDir, candidatePromptFileName)
	promptContent, err := os.ReadFile(promptPath)
	if errors.Is(err, os.ErrNotExist) {
		baseline, err := os.ReadFile(promptSourcePath)
		if err != nil {
			return nil, false, fmt.Errorf("read baseline prompt %q: %w", promptSourcePath, err)
		}
		if strings.TrimSpace(string(baseline)) != profileInstruction {
			return nil, false, fmt.Errorf(
				"candidate profile %q carries an instruction that differs from baseline prompt %q "+
					"while no candidate prompt was published; rerun the pipeline instead of promoting "+
					"a combination that never passed the gate",
				profilePath, promptSourcePath)
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read candidate prompt %q: %w", promptPath, err)
	}
	promptText := strings.TrimSpace(string(promptContent))
	if promptText == "" {
		return nil, false, fmt.Errorf("candidate prompt %q is empty", promptPath)
	}
	if promptText != profileInstruction {
		return nil, false, fmt.Errorf(
			"candidate prompt %q and the instruction override in %q disagree; "+
				"promoting them would deploy a combination that never passed the gate",
			promptPath, profilePath)
	}
	return []stagedFile{{path: promptSourcePath, content: []byte(promptText + "\n")}}, true, nil
}
