//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessages(t *testing.T) {
	ev := &finalResponseEvaluator{}
	actual := &evalset.Invocation{
		UserContent: &genai.Content{Parts: []*genai.Part{{Text: "user?"}}},
		FinalResponse: &genai.Content{Parts: []*genai.Part{
			{Text: "actual"},
		}},
	}
	expected := &evalset.Invocation{
		FinalResponse: &genai.Content{Parts: []*genai.Part{
			{Text: "expected"},
		}},
	}
	msgs, err := ev.ConstructMessages(actual, expected, nil)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleUser, msgs[0].Role)
	assert.Contains(t, msgs[0].Content, "user?")
	assert.Contains(t, msgs[0].Content, "actual")
	assert.Contains(t, msgs[0].Content, "expected")
}

func TestConstructMessagesTemplateError(t *testing.T) {
	original := finalResponsePromptTemplate
	t.Cleanup(func() { finalResponsePromptTemplate = original })
	finalResponsePromptTemplate = template.Must(template.New("err").Funcs(template.FuncMap{
		"fail": func() (string, error) { return "", fmt.Errorf("boom") },
	}).Parse(`{{fail}}`))

	ev := &finalResponseEvaluator{}
	_, err := ev.ConstructMessages(&evalset.Invocation{}, &evalset.Invocation{}, nil)
	require.Error(t, err)
}

func TestScoreBasedOnResponse(t *testing.T) {
	ev := &finalResponseEvaluator{}
	validResp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: `{"is_the_agent_response_valid":"VALID"}`,
				},
			},
		},
	}
	score, err := ev.ScoreBasedOnResponse(validResp, nil)
	require.NoError(t, err)
	require.NotNil(t, score.Score)
	assert.Equal(t, 1.0, score.Score)

	invalidResp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: `{"is_the_agent_response_valid":"INVALID"}`,
				},
			},
		},
	}
	score, err = ev.ScoreBasedOnResponse(invalidResp, nil)
	require.NoError(t, err)
	require.NotNil(t, score.Score)
	assert.Equal(t, 0.0, score.Score)

	unknownResp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Content: `{"is_the_agent_response_valid":"UNKNOWN"}`,
				},
			},
		},
	}
	_, err = ev.ScoreBasedOnResponse(unknownResp, nil)
	require.Error(t, err)

	emptyChoices := &model.Response{Choices: []model.Choice{}}
	_, err = ev.ScoreBasedOnResponse(emptyChoices, nil)
	require.Error(t, err)

	emptyContent := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{Content: ""},
			},
		},
	}
	_, err = ev.ScoreBasedOnResponse(emptyContent, nil)
	require.Error(t, err)
}

func TestAggregateSamples(t *testing.T) {
	ev := &finalResponseEvaluator{}
	evalMetric := &metric.EvalMetric{Threshold: 0.5}
	positive := &evaluator.PerInvocationResult{Score: 1, Status: status.EvalStatusPassed}
	negative := &evaluator.PerInvocationResult{Score: 0, Status: status.EvalStatusFailed}

	result, err := ev.AggregateSamples([]*evaluator.PerInvocationResult{positive, negative, positive}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, positive, result)

	result, err = ev.AggregateSamples([]*evaluator.PerInvocationResult{negative, negative, positive}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, negative, result)

	// No samples returns error.
	_, err = ev.AggregateSamples([]*evaluator.PerInvocationResult{}, evalMetric)
	require.Error(t, err)

	// Mixed but empty positive/negative (NotEvaluated) falls back to first sample.
	result, err = ev.AggregateSamples([]*evaluator.PerInvocationResult{
		{Score: 0, Status: status.EvalStatusNotEvaluated},
	}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.Status)
}

func TestAggregateInvocations(t *testing.T) {
	ev := &finalResponseEvaluator{}
	evalMetric := &metric.EvalMetric{Threshold: 0.6}
	results := []*evaluator.PerInvocationResult{
		{Score: 1, Status: status.EvalStatusPassed},
		{Score: 0, Status: status.EvalStatusFailed},
		{Score: 0, Status: status.EvalStatusNotEvaluated},
	}
	agg, err := ev.AggregateInvocations(results, evalMetric)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, agg.OverallScore, 1e-9)
	assert.Equal(t, status.EvalStatusFailed, agg.OverallStatus)
	assert.Equal(t, results, agg.PerInvocationResults)

	agg, err = ev.AggregateInvocations([]*evaluator.PerInvocationResult{
		{Score: 0, Status: status.EvalStatusNotEvaluated},
	}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, agg.OverallStatus)
}

func TestGetTextFromContent(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(getTextFromContent(nil))
	assert.Equal(t, "", buf.String())

	content := &genai.Content{Parts: []*genai.Part{{Text: "hello "}, {Text: "world"}}}
	assert.Equal(t, "hello world", getTextFromContent(content))
}

func TestExtractLabel(t *testing.T) {
	assert.Equal(t, LabelValid, extractLabel(`"is_the_agent_response_valid":"VALID"`))
	assert.Equal(t, LabelInvalid, extractLabel(`"is_the_agent_response_valid":"INVALID"`))
	assert.Equal(t, LabelInvalid, extractLabel(`no label`))
	assert.Equal(t, Label("UNKNOWN"), extractLabel(`"is_the_agent_response_valid":"UNKNOWN"`))
}

type stubLLMBase struct {
	evaluateCalled bool
	result         *evaluator.EvaluateResult
}

func (s *stubLLMBase) Name() string { return "stub" }

func (s *stubLLMBase) Description() string { return "stub desc" }

func (s *stubLLMBase) Evaluate(_ context.Context, _ []*evalset.Invocation, _ []*evalset.Invocation,
	_ *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	s.evaluateCalled = true
	return s.result, nil
}

func (s *stubLLMBase) ConstructMessages(*evalset.Invocation, *evalset.Invocation,
	*metric.EvalMetric) ([]model.Message, error) {
	return nil, nil
}

func (s *stubLLMBase) ScoreBasedOnResponse(*model.Response, *metric.EvalMetric) (*evalresult.ScoreResult, error) {
	return nil, nil
}

func (s *stubLLMBase) AggregateSamples([]*evaluator.PerInvocationResult,
	*metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return nil, nil
}

func (s *stubLLMBase) AggregateInvocations([]*evaluator.PerInvocationResult,
	*metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return s.result, nil
}

func TestFinalResponseEvaluator_ConstructorsAndEvaluate(t *testing.T) {
	ev := New()
	evaluatorImpl, ok := ev.(*finalResponseEvaluator)
	require.True(t, ok)

	// Override base to avoid calling real LLM flow.
	stub := &stubLLMBase{result: &evaluator.EvaluateResult{OverallStatus: status.EvalStatusPassed}}
	evaluatorImpl.llmBaseEvaluator = stub

	got, err := evaluatorImpl.Evaluate(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	assert.True(t, stub.evaluateCalled)
	assert.Equal(t, status.EvalStatusPassed, got.OverallStatus)
	assert.Equal(t, "llm_final_response", evaluatorImpl.Name())
	assert.Equal(t, "LLM judge for final responses", evaluatorImpl.Description())
}
