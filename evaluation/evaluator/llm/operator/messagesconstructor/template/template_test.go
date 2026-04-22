//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package template

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/templateresolver"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesRendersBoundVariables(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "What is the capital of France?"},
		FinalResponse: &model.Message{Content: "Paris"},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "Paris"},
	}

	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		buildTemplateEvalMetric(
			"Question: {{question}}\nAnswer: {{answer}}\nReference: {{reference}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "question",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldUserContent,
				},
			},
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "answer",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "reference",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeExpected,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
		),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "What is the capital of France?")
	assert.Contains(t, messages[0].Content, "Answer: Paris")
	assert.Contains(t, messages[0].Content, "Reference: Paris")
}

func TestConstructMessagesRejectsDuplicateBindings(t *testing.T) {
	constructor := New()

	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "reference"}}},
		buildTemplateEvalMetric(
			"Answer: {{answer}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "answer",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "answer",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeExpected,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
		),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `templateVariable "answer" is duplicated`)
}

func TestConstructMessagesRequiresExpectedFinalResponse(t *testing.T) {
	constructor := New()

	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "candidate"}}},
		[]*evalset.Invocation{{}},
		buildTemplateEvalMetric(
			"Reference: {{reference}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "reference",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeExpected,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
		),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected finalResponse is empty")
}

func TestConstructMessagesRejectsUnknownPlaceholder(t *testing.T) {
	constructor := New()

	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "candidate"}}},
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "reference"}}},
		buildTemplateEvalMetric(
			"Answer: {{answer}}\nMissing: {{missing}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "answer",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldFinalResponse,
				},
			},
		),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render template prompt")
	assert.Contains(t, err.Error(), "missing")
}

func TestConstructMessagesRejectsUnsupportedSource(t *testing.T) {
	constructor := New()

	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{{UserContent: &model.Message{Content: "question"}}},
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "reference"}}},
		buildTemplateEvalMetric(
			"Question: {{question}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "question",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeExpected,
					Field: criterionllm.TemplateVariableFieldUserContent,
				},
			},
		),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported source expected.userContent")
}

func TestConstructMessagesRejectsInvalidTemplateOptions(t *testing.T) {
	constructor := New()
	_, err := constructor.ConstructMessages(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing llm judge criterion")
	_, err = constructor.ConstructMessages(context.Background(), nil, nil, &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template is nil")
	_, err = constructor.ConstructMessages(context.Background(), nil, nil, buildTemplateEvalMetric("", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template prompt is empty")
	metricWithEmptyScorer := buildTemplateEvalMetric("Answer: {{answer}}",
		&criterionllm.TemplateVariableBinding{
			TemplateVariable: "answer",
			Source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldFinalResponse,
			},
		},
	)
	metricWithEmptyScorer.Criterion.LLMJudge.Template.ResponseScorerName = ""
	_, err = constructor.ConstructMessages(context.Background(), nil, nil, metricWithEmptyScorer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template responseScorerName is empty")
}

func TestResolveTemplateValuesRejectsInvalidBindings(t *testing.T) {
	values, err := resolveTemplateValues(nil, nil, []*criterionllm.TemplateVariableBinding{nil})
	require.Error(t, err)
	assert.Nil(t, values)
	assert.Contains(t, err.Error(), "template binding is nil")
	values, err = resolveTemplateValues(nil, nil, []*criterionllm.TemplateVariableBinding{{
		Source: &criterionllm.TemplateVariableSource{
			Scope: criterionllm.TemplateVariableScopeActual,
			Field: criterionllm.TemplateVariableFieldFinalResponse,
		},
	}})
	require.Error(t, err)
	assert.Nil(t, values)
	assert.Contains(t, err.Error(), "templateVariable is empty")
}

func TestResolveBindingValueRejectsNilAndUnsupportedSource(t *testing.T) {
	value, err := resolveBindingValue(nil, nil, nil)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "source is nil")
	value, err = resolveBindingValue(nil, nil, &criterionllm.TemplateVariableSource{
		Scope: "metric",
		Field: criterionllm.TemplateVariableFieldFinalResponse,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "unsupported source metric.finalResponse")
}

func TestResolveActualValueRejectsInvalidActualInput(t *testing.T) {
	value, err := resolveActualValue(nil, criterionllm.TemplateVariableFieldFinalResponse)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "actuals is empty")
	value, err = resolveActualValue([]*evalset.Invocation{nil}, criterionllm.TemplateVariableFieldFinalResponse)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "actual invocation is nil")
	value, err = resolveActualValue([]*evalset.Invocation{{}}, criterionllm.TemplateVariableField("rubrics"))
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "unsupported source actual.rubrics")
}

func TestResolveExpectedValueRejectsInvalidExpectedInput(t *testing.T) {
	value, err := resolveExpectedValue(nil, criterionllm.TemplateVariableFieldFinalResponse)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "expecteds is empty")
	value, err = resolveExpectedValue([]*evalset.Invocation{nil}, criterionllm.TemplateVariableFieldFinalResponse)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "expected invocation is nil")
	value, err = resolveExpectedValue([]*evalset.Invocation{{FinalResponse: &model.Message{Content: "ok"}}},
		criterionllm.TemplateVariableFieldUserContent)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "unsupported source expected.userContent")
}

func buildTemplateEvalMetric(promptText string,
	bindings ...*criterionllm.TemplateVariableBinding) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             promptText,
					ResponseScorerName: templateresolver.ResponseScorerSingleScoreName,
					VariableBindings:   bindings,
				},
			},
		},
	}
}
