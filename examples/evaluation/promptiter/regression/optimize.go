//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

type promptCandidate struct {
	ID      string
	Reason  string
	Patch   promptiter.SurfacePatch
	Profile promptiter.Profile
}

func buildPromptCandidate(
	basePrompt string,
	config candidateConfig,
	targetSurfaceID string,
) (promptCandidate, error) {
	basePrompt = strings.TrimSpace(basePrompt)
	appendText := strings.TrimSpace(config.Append)
	if basePrompt == "" {
		return promptCandidate{}, fmt.Errorf("base prompt is empty")
	}
	if appendText == "" {
		return promptCandidate{}, fmt.Errorf("candidate %q append text is empty", config.ID)
	}
	candidateText := basePrompt + "\n\n" + appendText
	patch := promptiter.SurfacePatch{
		SurfaceID: targetSurfaceID,
		Value:     astructure.SurfaceValue{Text: &candidateText},
		Reason:    config.Reason,
	}
	return promptCandidate{
		ID:     config.ID,
		Reason: config.Reason,
		Patch:  patch,
		Profile: promptiter.Profile{
			StructureID: "deterministic-regression-loop",
			Overrides: []promptiter.SurfaceOverride{{
				SurfaceID: targetSurfaceID,
				Value:     patch.Value,
			}},
		},
	}, nil
}

func candidateTargetsCurrentFailures(
	config candidateConfig,
	currentTrain evaluationSummary,
) bool {
	if len(config.TargetFailures) == 0 {
		return true
	}
	targets := make(map[failureCategory]struct{}, len(config.TargetFailures))
	for _, category := range config.TargetFailures {
		targets[category] = struct{}{}
	}
	for _, evalCase := range currentTrain.Cases {
		for _, attribution := range evalCase.FailureAttributions {
			if _, ok := targets[attribution.Category]; ok {
				return true
			}
		}
	}
	return false
}
