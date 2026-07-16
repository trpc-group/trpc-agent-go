//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type trainEvidence struct {
	round    int
	snapshot *EvaluationSnapshot
}

func buildTrainIndex(
	ctx context.Context,
	source *engine.RunResult,
	critical map[string]struct{},
	policies map[string]MetricPolicy,
	expectedRuns int,
) (map[string][]trainEvidence, error) {
	result := make(map[string][]trainEvidence, len(source.Rounds))
	for _, round := range source.Rounds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hash, err := ProfileHash(round.InputProfile)
		if err != nil {
			return nil, fmt.Errorf("hash round %d input profile: %w", round.Round, err)
		}
		snapshot, err := adaptEvaluation(round.Train, round.InputProfile, critical)
		if err != nil {
			return nil, fmt.Errorf("adapt round %d train: %w", round.Round, err)
		}
		markConfiguredMetricCoverage(snapshot, policies)
		markExpectedRunCoverage(snapshot, expectedRuns)
		result[hash] = append(result[hash], trainEvidence{
			round: round.Round, snapshot: snapshot,
		})
	}
	return result, nil
}

func candidateTrain(
	values []trainEvidence,
	candidateRound int,
) *EvaluationSnapshot {
	var selected *trainEvidence
	for index := range values {
		current := &values[index]
		if current.round <= candidateRound {
			continue
		}
		if selected == nil || current.round < selected.round {
			selected = current
		}
	}
	if selected == nil {
		return nil
	}
	return selected.snapshot
}

func roundCandidateTrain(
	round engine.RoundResult,
	fallback []trainEvidence,
	critical map[string]struct{},
	policies map[string]MetricPolicy,
	expectedRuns int,
) (*EvaluationSnapshot, error) {
	if round.CandidateTrain == nil {
		return candidateTrain(fallback, round.Round), nil
	}
	snapshot, err := adaptEvaluation(
		round.CandidateTrain,
		round.OutputProfile,
		critical,
	)
	if err != nil {
		return nil, err
	}
	markConfiguredMetricCoverage(snapshot, policies)
	markExpectedRunCoverage(snapshot, expectedRuns)
	return snapshot, nil
}

func profileOnlyChangesTarget(
	baseline *promptiter.Profile,
	candidate *promptiter.Profile,
	targetSurfaceID string,
) (bool, string) {
	if baseline == nil || candidate == nil {
		return false, "baseline or candidate profile is nil"
	}
	if baseline.StructureID != candidate.StructureID {
		return false, "candidate structure id differs from baseline"
	}
	baselineOverrides, err := overrideJSON(baseline)
	if err != nil {
		return false, "baseline profile is invalid: " + err.Error()
	}
	candidateOverrides, err := overrideJSON(candidate)
	if err != nil {
		return false, "candidate profile is invalid: " + err.Error()
	}
	for surfaceID, baselineValue := range baselineOverrides {
		if surfaceID == targetSurfaceID {
			continue
		}
		candidateValue, exists := candidateOverrides[surfaceID]
		if !exists || candidateValue != baselineValue {
			return false, fmt.Sprintf("candidate modifies non-target surface %q", surfaceID)
		}
	}
	for surfaceID := range candidateOverrides {
		if surfaceID != targetSurfaceID {
			if _, exists := baselineOverrides[surfaceID]; !exists {
				return false, fmt.Sprintf("candidate adds non-target surface %q", surfaceID)
			}
		}
	}
	if _, exists := candidateOverrides[targetSurfaceID]; !exists {
		return false, fmt.Sprintf("candidate omits target surface %q", targetSurfaceID)
	}
	return true, "candidate changes only the configured target surface"
}

func overrideJSON(profile *promptiter.Profile) (map[string]string, error) {
	result := make(map[string]string, len(profile.Overrides))
	for _, override := range profile.Overrides {
		if override.SurfaceID == "" {
			return nil, fmt.Errorf("profile override surface id is empty")
		}
		if _, exists := result[override.SurfaceID]; exists {
			return nil, fmt.Errorf("duplicate profile override surface id %q", override.SurfaceID)
		}
		encoded, err := json.Marshal(override.Value)
		if err != nil {
			return nil, fmt.Errorf("encode profile override %q: %w", override.SurfaceID, err)
		}
		result[override.SurfaceID] = string(encoded)
	}
	return result, nil
}
