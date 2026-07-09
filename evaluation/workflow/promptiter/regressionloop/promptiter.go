//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// CandidatesFromPromptIterResult adapts existing PromptIter engine rounds into regression loop candidates.
func CandidatesFromPromptIterResult(result *promptiterengine.RunResult, promptSurfaceID string) []Candidate {
	if result == nil {
		return nil
	}
	candidates := make([]Candidate, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		candidate := Candidate{Round: round.Round}
		if round.OutputProfile != nil {
			candidate.Prompt = promptTextForSurface(round.OutputProfile.Overrides, promptSurfaceID)
		}
		if round.Acceptance != nil {
			candidate.Reason = round.Acceptance.Reason
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func promptTextForSurface(overrides []promptiter.SurfaceOverride, surfaceID string) string {
	for _, override := range overrides {
		if override.SurfaceID != surfaceID || override.Value.Text == nil {
			continue
		}
		return *override.Value.Text
	}
	return ""
}
