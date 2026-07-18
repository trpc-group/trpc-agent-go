//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"errors"
	"fmt"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// GeneratePromptIter runs one existing PromptIter engine round for one prompt surface.
func GeneratePromptIter(
	ctx context.Context,
	engine promptiterengine.Engine,
	base promptiterengine.RunRequest,
	targetSurfaceID string,
	request CandidateRequest,
) (string, error) {
	if engine == nil {
		return "", errors.New("PromptIter engine is nil")
	}
	if strings.TrimSpace(targetSurfaceID) == "" || strings.TrimSpace(request.Prompt) == "" {
		return "", errors.New("PromptIter target surface and input prompt are required")
	}
	if len(base.Train) != 1 || len(base.Validation) != 1 {
		return "", errors.New("PromptIter regression requires one train and one validation eval set")
	}
	structure, err := engine.Describe(ctx)
	if err != nil {
		return "", fmt.Errorf("describe PromptIter structure: %w", err)
	}
	if structure == nil || structure.StructureID == "" || !containsSurface(structure.Surfaces, targetSurfaceID) {
		return "", fmt.Errorf("PromptIter structure does not contain target surface %q", targetSurfaceID)
	}
	runRequest := base
	runRequest.Train = cloneEvalSetInputs(base.Train)
	runRequest.Validation = cloneEvalSetInputs(base.Validation)
	runRequest.MaxRounds = 1
	runRequest.TargetSurfaceIDs = []string{targetSurfaceID}
	text := request.Prompt
	runRequest.InitialProfile = &promptiter.Profile{
		StructureID: structure.StructureID,
		Overrides:   []promptiter.SurfaceOverride{{SurfaceID: targetSurfaceID, Value: astructure.SurfaceValue{Text: &text}}},
	}
	for _, hint := range request.Hints {
		if hint.CaseID == "" || hint.MetricName == "" || strings.TrimSpace(hint.Reason) == "" {
			return "", errors.New("PromptIter failure hint is incomplete")
		}
		runRequest.Train[0].LossHints = append(runRequest.Train[0].LossHints, promptiterengine.LossHint{
			EvalCaseID: hint.CaseID, MetricName: hint.MetricName, Reason: hint.Reason,
		})
	}
	run, err := engine.Run(ctx, &runRequest)
	if err != nil {
		return "", fmt.Errorf("run PromptIter: %w", err)
	}
	if run == nil || len(run.Rounds) != 1 || run.Rounds[0].OutputProfile == nil {
		return "", errors.New("PromptIter returned no candidate profile")
	}
	return promptFromProfile(run.Rounds[0].OutputProfile, targetSurfaceID)
}

func promptFromProfile(profile *promptiter.Profile, targetSurfaceID string) (string, error) {
	var result *string
	for _, override := range profile.Overrides {
		if override.SurfaceID != targetSurfaceID {
			continue
		}
		if result != nil {
			return "", fmt.Errorf("PromptIter returned duplicate target surface %q", targetSurfaceID)
		}
		result = override.Value.Text
	}
	if result == nil || strings.TrimSpace(*result) == "" {
		return "", fmt.Errorf("PromptIter returned no text for target surface %q", targetSurfaceID)
	}
	return *result, nil
}

func cloneEvalSetInputs(inputs []promptiterengine.EvalSetInput) []promptiterengine.EvalSetInput {
	result := append([]promptiterengine.EvalSetInput(nil), inputs...)
	for index := range result {
		result[index].EvalCaseIDs = append([]string(nil), result[index].EvalCaseIDs...)
		result[index].LossHints = append([]promptiterengine.LossHint(nil), result[index].LossHints...)
	}
	return result
}

func containsSurface(surfaces []astructure.Surface, id string) bool {
	for _, surface := range surfaces {
		if surface.SurfaceID == id {
			return true
		}
	}
	return false
}
