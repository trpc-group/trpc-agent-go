//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package registry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer/singlescore"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRegistryResolveReturnsBuiltins(t *testing.T) {
	registry := New()
	operators, err := registry.Resolve(buildTemplateMetric(ResponseScorerSingleScoreName, "", ""))
	require.NoError(t, err)
	assert.NotNil(t, operators.ResponseScorer)
	assert.NotNil(t, operators.SamplesAggregator)
	assert.NotNil(t, operators.InvocationsAggregator)
	operators, err = registry.Resolve(buildTemplateMetric(ResponseScorerBooleanName, "", ""))
	require.NoError(t, err)
	require.NotNil(t, operators.StructuredOutputProvider)
	output, err := operators.StructuredOutputProvider.StructuredOutput(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Equal(t, "boolean_result", output.JSONSchema.Name)
}

func TestRegistryResolveUsesCustomResponseScorer(t *testing.T) {
	registry := New()
	err := registry.RegisterResponseScorer("custom", singlescore.New())
	require.NoError(t, err)
	operators, err := registry.Resolve(buildTemplateMetric("custom", "", ""))
	require.NoError(t, err)
	assert.NotNil(t, operators.ResponseScorer)
}

func TestRegistryStructuredOutputUsesExplicitName(t *testing.T) {
	registry := New()
	err := registry.RegisterResponseScorer("custom_score", singlescore.New())
	require.NoError(t, err)
	err = registry.RegisterStructuredOutput("custom_schema", testStructuredOutputProvider{
		output: &model.StructuredOutput{
			Type:       model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{Name: "custom_schema_result"},
		},
	})
	require.NoError(t, err)
	evalMetric := buildTemplateMetric("custom_score", "", "")
	evalMetric.Criterion.LLMJudge.Template.StructuredOutputName = "custom_schema"
	operators, err := registry.Resolve(evalMetric)
	require.NoError(t, err)
	require.NotNil(t, operators.StructuredOutputProvider)
	output, err := operators.StructuredOutputProvider.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "custom_schema_result", output.JSONSchema.Name)
}

func TestRegistryStructuredOutputAllowsCustomScorerWithoutDefaultProvider(t *testing.T) {
	registry := New()
	err := registry.RegisterResponseScorer("custom_score", singlescore.New())
	require.NoError(t, err)
	operators, err := registry.Resolve(buildTemplateMetric("custom_score", "", ""))
	require.NoError(t, err)
	assert.Nil(t, operators.StructuredOutputProvider)
}

func TestRegistryResolveRejectsInvalidTemplateOptions(t *testing.T) {
	registry := New()
	_, err := registry.Resolve(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing llm judge criterion")
	_, err = registry.Resolve(&metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template is nil")
	_, err = registry.Resolve(buildTemplateMetric("", "", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template responseScorerName is empty")
}

func TestRegistryResolveRejectsUnknownOperators(t *testing.T) {
	registry := New()
	_, err := registry.Resolve(buildTemplateMetric("missing", "", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported response scorer "missing"`)
	_, err = registry.Resolve(buildTemplateMetric(ResponseScorerSingleScoreName, "missing", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported samples aggregator "missing"`)
	_, err = registry.Resolve(buildTemplateMetric(ResponseScorerSingleScoreName, "", "missing"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported invocations aggregator "missing"`)
}

func TestRegistryStructuredOutputRejectsExplicitMissingProvider(t *testing.T) {
	registry := New()
	evalMetric := buildTemplateMetric(ResponseScorerSingleScoreName, "", "")
	evalMetric.Criterion.LLMJudge.Template.StructuredOutputName = "missing"
	_, err := registry.Resolve(evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported structured output "missing"`)
}

func buildTemplateMetric(responseScorerName, sampleAggregatorName,
	invocationAggregatorName string) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					ResponseScorerName:       responseScorerName,
					SampleAggregatorName:     sampleAggregatorName,
					InvocationAggregatorName: invocationAggregatorName,
				},
			},
		},
	}
}

type testStructuredOutputProvider struct {
	output *model.StructuredOutput
}

func (p testStructuredOutputProvider) StructuredOutput(context.Context, []*evalset.Invocation, []*evalset.Invocation,
	*metric.EvalMetric) (*model.StructuredOutput, error) {
	return p.output, nil
}
