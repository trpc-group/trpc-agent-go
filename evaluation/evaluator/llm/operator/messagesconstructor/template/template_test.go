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

	agenttrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"
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

func TestConstructMessagesRendersTraceStepOutputFromLastMatchingNode(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		ExecutionTrace: &agenttrace.Trace{
			Steps: []agenttrace.Step{
				{NodeID: "fetch_match", Output: &agenttrace.Snapshot{Text: "stale data"}},
				{NodeID: "other", Output: &agenttrace.Snapshot{Text: "ignore me"}},
				{NodeID: "fetch_match", Output: &agenttrace.Snapshot{Text: "fresh match data"}},
			},
		},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "reference"}}},
		buildTemplateEvalMetric(
			"Match data: {{match_data}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "match_data",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldTraceStepOutput,
					Selector: &criterionllm.TemplateVariableSelector{
						NodeID: "fetch_match",
					},
				},
			},
		),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "fresh match data")
	assert.NotContains(t, messages[0].Content, "stale data")
}

func TestConstructMessagesRendersTraceStepInput(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		ExecutionTrace: &agenttrace.Trace{
			Steps: []agenttrace.Step{
				{NodeID: "fetch_match", Input: &agenttrace.Snapshot{Text: "match query"}},
			},
		},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{{FinalResponse: &model.Message{Content: "reference"}}},
		buildTemplateEvalMetric(
			"Tool input: {{tool_input}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "tool_input",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldTraceStepInput,
					Selector: &criterionllm.TemplateVariableSelector{
						NodeID: "fetch_match",
					},
				},
			},
		),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "Tool input: match query")
}

func TestConstructMessagesRendersMetricRubrics(t *testing.T) {
	constructor := New()
	evalMetric := buildTemplateEvalMetric(
		"Rubrics: {{rubrics}}",
		&criterionllm.TemplateVariableBinding{
			TemplateVariable: "rubrics",
			Source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeMetric,
				Field: criterionllm.TemplateVariableFieldRubrics,
			},
		},
	)
	evalMetric.Criterion.LLMJudge.Rubrics = []*criterionllm.Rubric{
		{ID: "r1", Content: &criterionllm.RubricContent{Text: "Must be correct."}},
	}
	messages, err := constructor.ConstructMessages(context.Background(), nil, nil, evalMetric)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, `"id":"r1"`)
	assert.Contains(t, messages[0].Content, `"text":"Must be correct."`)
}

func TestConstructMessagesAppliesJSONPath(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		ExecutionTrace: &agenttrace.Trace{
			Steps: []agenttrace.Step{
				{NodeID: "fetch", Output: &agenttrace.Snapshot{Text: `{"payload":{"answer":"Paris","evidence":{"city":"Paris"}}}`}},
			},
		},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		nil,
		buildTemplateEvalMetric(
			"Answer: {{answer}}\nEvidence: {{evidence}}",
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "answer",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldTraceStepOutput,
					Selector: &criterionllm.TemplateVariableSelector{
						NodeID: "fetch",
					},
					Path: "$.payload.answer",
				},
			},
			&criterionllm.TemplateVariableBinding{
				TemplateVariable: "evidence",
				Source: &criterionllm.TemplateVariableSource{
					Scope: criterionllm.TemplateVariableScopeActual,
					Field: criterionllm.TemplateVariableFieldTraceStepOutput,
					Selector: &criterionllm.TemplateVariableSelector{
						NodeID: "fetch",
					},
					Path: "payload.evidence",
				},
			},
		),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "Answer: Paris")
	assert.Contains(t, messages[0].Content, `Evidence: {"city":"Paris"}`)
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

func TestStructuredOutputDefaultsToResponseScorerName(t *testing.T) {
	constructor, ok := New().(messagesconstructor.StructuredOutputMessagesConstructor)
	require.True(t, ok)
	output, err := constructor.StructuredOutput(context.Background(), nil, nil,
		buildTemplateEvalMetric("Answer: {{answer}}", nil))
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "single_score_result", output.JSONSchema.Name)
	evalMetric := buildTemplateEvalMetric("Answer: {{answer}}", nil)
	evalMetric.Criterion.LLMJudge.Template.ResponseScorerName = operatorregistry.ResponseScorerRubricScoresName
	output, err = constructor.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_scores_result", output.JSONSchema.Name)
	evalMetric.Criterion.LLMJudge.Template.ResponseScorerName = operatorregistry.ResponseScorerCategoricalName
	evalMetric.Criterion.LLMJudge.Template.ResponseScorerOptions = &criterionllm.ResponseScorerOptions{
		Categories: []*criterionllm.CategoryScore{
			{Label: "correct", Score: 1},
			{Label: "incorrect", Score: 0},
		},
	}
	output, err = constructor.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "categorical_result", output.JSONSchema.Name)
}

func TestStructuredOutputUsesStructuredOutputName(t *testing.T) {
	constructor := New().(messagesconstructor.StructuredOutputMessagesConstructor)
	evalMetric := buildTemplateEvalMetric("Answer: {{answer}}", nil)
	evalMetric.Criterion.LLMJudge.Template.ResponseScorerName = operatorregistry.ResponseScorerSingleScoreName
	evalMetric.Criterion.LLMJudge.Template.StructuredOutputName = operatorregistry.StructuredOutputRubricScoresName
	output, err := constructor.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.JSONSchema)
	assert.Equal(t, "rubric_scores_result", output.JSONSchema.Name)
}

func TestStructuredOutputRejectsInvalidTemplateOptions(t *testing.T) {
	constructor := New().(messagesconstructor.StructuredOutputMessagesConstructor)
	_, err := constructor.StructuredOutput(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing llm judge criterion")
	_, err = constructor.StructuredOutput(context.Background(), nil, nil, &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template is nil")
}

func TestStructuredOutputRejectsUnsupportedStructuredOutput(t *testing.T) {
	constructor := New().(messagesconstructor.StructuredOutputMessagesConstructor)
	evalMetric := buildTemplateEvalMetric("Answer: {{answer}}", nil)
	evalMetric.Criterion.LLMJudge.Template.StructuredOutputName = "missing"
	_, err := constructor.StructuredOutput(context.Background(), nil, nil, evalMetric)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported structured output "missing"`)
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
	values, err := resolveTemplateValues(nil, nil, nil, []*criterionllm.TemplateVariableBinding{nil})
	require.Error(t, err)
	assert.Nil(t, values)
	assert.Contains(t, err.Error(), "template binding is nil")
	values, err = resolveTemplateValues(nil, nil, nil, []*criterionllm.TemplateVariableBinding{{
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
	value, err := resolveBindingValue(nil, nil, nil, nil)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "source is nil")
	value, err = resolveBindingValue(nil, nil, buildTemplateEvalMetric("{{x}}"), &criterionllm.TemplateVariableSource{
		Scope: criterionllm.TemplateVariableScopeMetric,
		Field: criterionllm.TemplateVariableFieldFinalResponse,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "unsupported source metric.finalResponse")
}

func TestResolveBindingValueRejectsEmptyRubrics(t *testing.T) {
	value, err := resolveBindingValue(nil, nil, buildTemplateEvalMetric("{{rubrics}}"), &criterionllm.TemplateVariableSource{
		Scope: criterionllm.TemplateVariableScopeMetric,
		Field: criterionllm.TemplateVariableFieldRubrics,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "metric rubrics are empty")
}

func TestResolveActualValueRejectsInvalidActualInput(t *testing.T) {
	value, err := resolveActualValue(nil, &criterionllm.TemplateVariableSource{
		Field: criterionllm.TemplateVariableFieldFinalResponse,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "actuals is empty")
	value, err = resolveActualValue([]*evalset.Invocation{nil}, &criterionllm.TemplateVariableSource{
		Field: criterionllm.TemplateVariableFieldFinalResponse,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "actual invocation is nil")
}

func TestResolveExpectedValueRejectsInvalidExpectedInput(t *testing.T) {
	value, err := resolveExpectedValue(nil, &criterionllm.TemplateVariableSource{
		Field: criterionllm.TemplateVariableFieldFinalResponse,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "expecteds is empty")
	value, err = resolveExpectedValue([]*evalset.Invocation{nil}, &criterionllm.TemplateVariableSource{
		Field: criterionllm.TemplateVariableFieldFinalResponse,
	})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "expected invocation is nil")
	value, err = resolveExpectedValue([]*evalset.Invocation{{FinalResponse: &model.Message{Content: "ok"}}},
		&criterionllm.TemplateVariableSource{Field: criterionllm.TemplateVariableFieldUserContent})
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "unsupported source expected.userContent")
}

func TestResolveTraceStepErrors(t *testing.T) {
	tests := []struct {
		name    string
		actuals []*evalset.Invocation
		source  *criterionllm.TemplateVariableSource
		wantErr string
	}{
		{
			name: "missing trace",
			actuals: []*evalset.Invocation{{
				InvocationID: "inv-1",
			}},
			source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldTraceStepOutput,
				Selector: &criterionllm.TemplateVariableSelector{
					NodeID: "fetch",
				},
			},
			wantErr: "executionTrace is empty for actual.traceStepOutput at invocation index 0",
		},
		{
			name:    "missing selector",
			actuals: []*evalset.Invocation{{ExecutionTrace: &agenttrace.Trace{}}},
			source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldTraceStepOutput,
			},
			wantErr: "trace selector nodeID is required",
		},
		{
			name:    "empty node id",
			actuals: []*evalset.Invocation{{ExecutionTrace: &agenttrace.Trace{}}},
			source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldTraceStepOutput,
				Selector: &criterionllm.TemplateVariableSelector{
					NodeID: "",
				},
			},
			wantErr: "trace selector nodeID is required",
		},
		{
			name: "space node id is not trimmed",
			actuals: []*evalset.Invocation{{
				ExecutionTrace: &agenttrace.Trace{Steps: []agenttrace.Step{{NodeID: "fetch"}}},
			}},
			source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldTraceStepOutput,
				Selector: &criterionllm.TemplateVariableSelector{
					NodeID: " ",
				},
			},
			wantErr: `trace step not found for actual.traceStepOutput nodeID " " at invocation index 0`,
		},
		{
			name: "no matching step",
			actuals: []*evalset.Invocation{{
				ExecutionTrace: &agenttrace.Trace{Steps: []agenttrace.Step{{NodeID: "other"}}},
			}},
			source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldTraceStepOutput,
				Selector: &criterionllm.TemplateVariableSelector{
					NodeID: "fetch",
				},
			},
			wantErr: `trace step not found for actual.traceStepOutput nodeID "fetch" at invocation index 0`,
		},
		{
			name: "empty snapshot",
			actuals: []*evalset.Invocation{{
				ExecutionTrace: &agenttrace.Trace{Steps: []agenttrace.Step{{NodeID: "fetch"}}},
			}},
			source: &criterionllm.TemplateVariableSource{
				Scope: criterionllm.TemplateVariableScopeActual,
				Field: criterionllm.TemplateVariableFieldTraceStepOutput,
				Selector: &criterionllm.TemplateVariableSelector{
					NodeID: "fetch",
				},
			},
			wantErr: `trace snapshot is empty for actual.traceStepOutput nodeID "fetch" at invocation index 0`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value, err := resolveActualValue(tc.actuals, tc.source)
			require.Error(t, err)
			assert.Empty(t, value)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func buildTemplateEvalMetric(promptText string,
	bindings ...*criterionllm.TemplateVariableBinding) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             promptText,
					ResponseScorerName: operatorregistry.ResponseScorerSingleScoreName,
					VariableBindings:   bindings,
				},
			},
		},
	}
}
