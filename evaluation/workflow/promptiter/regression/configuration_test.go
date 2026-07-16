//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestPromptIterConfigurationCopiesEffectiveSettings(t *testing.T) {
	target := .8
	source := validConfiguration()
	source.StopPolicy.TargetScore = &target
	source.TargetSurfaceIDs = []string{"target", "secondary"}
	actual := promptIterConfiguration(source)
	require.NotNil(t, actual.TargetScore)
	assert.Equal(t, target, *actual.TargetScore)
	assert.Equal(t, source.TargetSurfaceIDs, actual.TargetSurfaceIDs)
	target = .9
	source.TargetSurfaceIDs[0] = "changed"
	assert.Equal(t, .8, *actual.TargetScore)
	assert.Equal(t, "target", actual.TargetSurfaceIDs[0])
}

func TestValidatePromptIterConfigurationRejectsIncompatibleSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*engine.RunConfiguration, *RunSpec)
	}{
		{name: "nil spec", mutate: func(_ *engine.RunConfiguration, spec *RunSpec) { *spec = RunSpec{} }},
		{name: "no effective runs", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.EvaluationOptions.NumRuns = 0 }},
		{name: "run count mismatch", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.EvaluationOptions.NumRuns = 2 }},
		{name: "no max rounds", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.MaxRounds = 0 }},
		{name: "non finite gain", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.AcceptancePolicy.MinScoreGain = math.NaN() }},
		{name: "negative stop rounds", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.StopPolicy.MaxRoundsWithoutAcceptance = -1 }},
		{name: "non finite target", mutate: func(source *engine.RunConfiguration, _ *RunSpec) {
			value := math.Inf(1)
			source.StopPolicy.TargetScore = &value
		}},
		{name: "negative parallelism", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.EvaluationOptions.EvalCaseParallelism = -1 }},
		{name: "no target ids", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.TargetSurfaceIDs = nil }},
		{name: "empty target id", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.TargetSurfaceIDs = []string{""} }},
		{name: "multiple target ids", mutate: func(source *engine.RunConfiguration, _ *RunSpec) {
			source.TargetSurfaceIDs = []string{"target", "secondary"}
		}},
		{name: "duplicate target id", mutate: func(source *engine.RunConfiguration, _ *RunSpec) {
			source.TargetSurfaceIDs = []string{"target", "target"}
		}},
		{name: "audit target omitted", mutate: func(source *engine.RunConfiguration, _ *RunSpec) { source.TargetSurfaceIDs = []string{"other"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := validConfiguration()
			spec := validRunSpec()
			test.mutate(&source, spec)
			if test.name == "nil spec" {
				require.Error(t, validatePromptIterConfiguration(source, nil))
				return
			}
			require.Error(t, validatePromptIterConfiguration(source, spec))
		})
	}
	require.NoError(t, validatePromptIterConfiguration(validConfiguration(), validRunSpec()))
}

func validConfiguration() engine.RunConfiguration {
	return engine.RunConfiguration{
		EvaluationOptions: engine.EvaluationOptions{NumRuns: 1},
		MaxRounds:         1,
		TargetSurfaceIDs:  []string{"target"},
	}
}

func validRunSpec() *RunSpec {
	return &RunSpec{Runtime: RuntimePolicy{NumRuns: 1}, TargetSurfaceID: "target"}
}
