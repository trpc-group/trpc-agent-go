//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func promptIterConfiguration(source engine.RunConfiguration) *PromptIterConfiguration {
	result := &PromptIterConfiguration{
		NumRuns:                              source.EvaluationOptions.NumRuns,
		TraceUsageCoversAllCalls:             source.EvaluationOptions.TraceUsageCoversAllCalls,
		EvalCaseParallelism:                  source.EvaluationOptions.EvalCaseParallelism,
		EvalCaseParallelInferenceEnabled:     source.EvaluationOptions.EvalCaseParallelInferenceEnabled,
		EvalCaseParallelEvaluationEnabled:    source.EvaluationOptions.EvalCaseParallelEvaluationEnabled,
		BackwardCaseParallelismEnabled:       source.BackwardOptions.CaseParallelismEnabled,
		BackwardCaseParallelism:              source.BackwardOptions.CaseParallelism,
		AggregationSurfaceParallelismEnabled: source.AggregationOptions.SurfaceParallelismEnabled,
		AggregationSurfaceParallelism:        source.AggregationOptions.SurfaceParallelism,
		OptimizerSurfaceParallelismEnabled:   source.OptimizerOptions.SurfaceParallelismEnabled,
		OptimizerSurfaceParallelism:          source.OptimizerOptions.SurfaceParallelism,
		MinScoreGain:                         source.AcceptancePolicy.MinScoreGain,
		MaxRounds:                            source.MaxRounds,
		MaxRoundsWithoutAcceptance:           source.StopPolicy.MaxRoundsWithoutAcceptance,
		TargetSurfaceIDs:                     append([]string(nil), source.TargetSurfaceIDs...),
	}
	if source.StopPolicy.TargetScore != nil {
		targetScore := *source.StopPolicy.TargetScore
		result.TargetScore = &targetScore
	}
	return result
}

func validatePromptIterConfiguration(
	source engine.RunConfiguration,
	spec *RunSpec,
) error {
	if spec == nil {
		return errors.New("run spec is nil")
	}
	if source.EvaluationOptions.NumRuns <= 0 {
		return errors.New("effective evaluation num runs must be greater than zero")
	}
	if source.EvaluationOptions.NumRuns != spec.Runtime.NumRuns {
		return fmt.Errorf(
			"effective evaluation num runs %d do not match audit num runs %d",
			source.EvaluationOptions.NumRuns,
			spec.Runtime.NumRuns,
		)
	}
	if source.MaxRounds <= 0 {
		return errors.New("effective max rounds must be greater than zero")
	}
	if !finite(source.AcceptancePolicy.MinScoreGain) {
		return errors.New("effective acceptance minimum score gain must be finite")
	}
	if source.StopPolicy.MaxRoundsWithoutAcceptance < 0 {
		return errors.New("effective max rounds without acceptance must be non-negative")
	}
	if source.StopPolicy.TargetScore != nil && !finite(*source.StopPolicy.TargetScore) {
		return errors.New("effective stop target score must be finite")
	}
	parallelism := []int{
		source.EvaluationOptions.EvalCaseParallelism,
		source.BackwardOptions.CaseParallelism,
		source.AggregationOptions.SurfaceParallelism,
		source.OptimizerOptions.SurfaceParallelism,
	}
	for _, value := range parallelism {
		if value < 0 {
			return errors.New("effective parallelism values must be non-negative")
		}
	}
	if len(source.TargetSurfaceIDs) != 1 {
		return errors.New("regression audit requires exactly one effective target surface id")
	}
	surfaceID := strings.TrimSpace(source.TargetSurfaceIDs[0])
	if surfaceID == "" {
		return errors.New("effective target surface id is empty")
	}
	if surfaceID != spec.TargetSurfaceID {
		return fmt.Errorf(
			"effective target surface %q does not match audit target surface %q",
			surfaceID,
			spec.TargetSurfaceID,
		)
	}
	return nil
}
