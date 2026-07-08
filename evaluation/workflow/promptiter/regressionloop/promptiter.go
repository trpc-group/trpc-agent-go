//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"

// CandidatesFromPromptIterResult adapts existing PromptIter engine rounds into regression loop candidates.
func CandidatesFromPromptIterResult(result *promptiterengine.RunResult) []Candidate {
	if result == nil {
		return nil
	}
	candidates := make([]Candidate, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		candidate := Candidate{Round: round.Round}
		if round.OutputProfile != nil && len(round.OutputProfile.Overrides) > 0 {
			if round.OutputProfile.Overrides[0].Value.Text != nil {
				candidate.Prompt = *round.OutputProfile.Overrides[0].Value.Text
			}
		}
		if round.Acceptance != nil {
			candidate.Reason = round.Acceptance.Reason
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}
