//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
)

type fakeLLMEvaluator struct {
	constructMessagesCalled   int
	scoreBasedOnResponseCalls int
	aggregateSamplesCalls     int
	aggregateInvocationsCalls int
	receivedSamples           []*evaluator.PerInvocationResult
	receivedInvocations       []*evaluator.PerInvocationResult
}

func (f *fakeLLMEvaluator) Name() string { return "fake" }

func (f *fakeLLMEvaluator) Description() string { return "fake desc" }

func (f *fakeLLMEvaluator) Evaluate(_ context.Context, _ []*evalset.Invocation, _ []*evalset.Invocation,
	_ *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return nil, nil
}

func (f *fakeLLMEvaluator) ConstructMessages(_ context.Context, actuals, expecteds []*evalset.Invocation,
	_ *metric.EvalMetric) ([]model.Message, error) {
	f.constructMessagesCalled++
	return []model.Message{{
		Role:    "user",
		Content: actuals[0].InvocationID + expecteds[0].InvocationID,
	}}, nil
}

func (f *fakeLLMEvaluator) ScoreBasedOnResponse(_ context.Context, _ *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	f.scoreBasedOnResponseCalls++
	score := 0.9
	return &evaluator.ScoreResult{Score: score, RubricScores: nil}, nil
}

func (f *fakeLLMEvaluator) AggregateSamples(_ context.Context, samples []*evaluator.PerInvocationResult,
	_ *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	f.aggregateSamplesCalls++
	f.receivedSamples = samples
	return &evaluator.PerInvocationResult{
		Score:  samples[0].Score,
		Status: samples[0].Status,
	}, nil
}

func (f *fakeLLMEvaluator) AggregateInvocations(_ context.Context, results []*evaluator.PerInvocationResult,
	_ *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	f.aggregateInvocationsCalls++
	f.receivedInvocations = results
	return &evaluator.EvaluateResult{
		OverallScore:         results[0].Score,
		OverallStatus:        results[0].Status,
		PerInvocationResults: results,
	}, nil
}

type fakeModel struct {
	responses []*model.Response
	err       error
}

func (f *fakeModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan *model.Response, len(f.responses))
	for _, rsp := range f.responses {
		ch <- rsp
	}
	close(ch)
	return ch, nil
}

func (f *fakeModel) Info() model.Info {
	return model.Info{Name: "fake"}
}

func buildEvalMetric(providerName string, numSamples int) *metric.EvalMetric {
	return &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				JudgeModel: &llm.JudgeModelOptions{
					ProviderName: providerName,
					ModelName:    "fake-model",
					NumSamples:   &numSamples,
					Generation:   &model.GenerationConfig{},
				},
			},
		},
	}
}

func TestLLMBaseEvaluator_EvaluateSuccess(t *testing.T) {
	provider.Register("llm-test-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}}, nil
	})
	stub := &fakeLLMEvaluator{}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	evalMetric := buildEvalMetric("llm-test-provider", 1)
	actual := &evalset.Invocation{InvocationID: "a"}
	expected := &evalset.Invocation{InvocationID: "b"}

	res, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		evalMetric,
	)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, stub.constructMessagesCalled)
	assert.Equal(t, 1, stub.scoreBasedOnResponseCalls)
	assert.Equal(t, 1, stub.aggregateSamplesCalls)
	assert.Equal(t, 1, stub.aggregateInvocationsCalls)
	require.Len(t, stub.receivedSamples, 1)
	assert.Equal(t, actual, stub.receivedSamples[0].ActualInvocation)
	assert.Equal(t, expected, stub.receivedSamples[0].ExpectedInvocation)
	require.Len(t, stub.receivedInvocations, 1)
	assert.Equal(t, stub.receivedSamples[0].Score, stub.receivedInvocations[0].Score)
}

func TestLLMBaseEvaluator_EvaluateValidationErrors(t *testing.T) {
	stub := &fakeLLMEvaluator{}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}

	_, err := base.Evaluate(context.Background(), nil, nil, nil)
	require.Error(t, err)

	evalMetric := buildEvalMetric("provider", 0)
	_, err = base.Evaluate(context.Background(), nil, nil, evalMetric)
	require.Error(t, err)

	evalMetric = buildEvalMetric("provider", 1)
	_, err = base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{},
		evalMetric,
	)
	require.Error(t, err)
}

type scriptedLLMEvaluator struct {
	constructErr        error
	scoreErr            error
	scoreValue          float64
	aggregateSamplesErr error
}

func (s *scriptedLLMEvaluator) Name() string { return "scripted" }

func (s *scriptedLLMEvaluator) Description() string { return "scripted" }

func (s *scriptedLLMEvaluator) Evaluate(context.Context, []*evalset.Invocation, []*evalset.Invocation,
	*metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return nil, nil
}

func (s *scriptedLLMEvaluator) ConstructMessages(context.Context, []*evalset.Invocation, []*evalset.Invocation,
	*metric.EvalMetric) ([]model.Message, error) {
	if s.constructErr != nil {
		return nil, s.constructErr
	}
	return []model.Message{{Role: "user", Content: "prompt"}}, nil
}

func (s *scriptedLLMEvaluator) ScoreBasedOnResponse(context.Context, *model.Response,
	*metric.EvalMetric) (*evaluator.ScoreResult, error) {
	if s.scoreErr != nil {
		return nil, s.scoreErr
	}
	score := s.scoreValue
	return &evaluator.ScoreResult{Score: score, RubricScores: nil}, nil
}

func (s *scriptedLLMEvaluator) AggregateSamples(_ context.Context, samples []*evaluator.PerInvocationResult,
	_ *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	if s.aggregateSamplesErr != nil {
		return nil, s.aggregateSamplesErr
	}
	return &evaluator.PerInvocationResult{
		ActualInvocation:   samples[0].ActualInvocation,
		ExpectedInvocation: samples[0].ExpectedInvocation,
		Score:              samples[0].Score,
		Status:             samples[0].Status,
	}, nil
}

func (s *scriptedLLMEvaluator) AggregateInvocations(_ context.Context, results []*evaluator.PerInvocationResult,
	_ *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return &evaluator.EvaluateResult{
		OverallScore:         results[0].Score,
		OverallStatus:        results[0].Status,
		PerInvocationResults: results,
	}, nil
}

func TestLLMBaseEvaluator_ErrorPaths(t *testing.T) {
	provider.Register("llm-test-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}}, nil
	})
	evalMetric := buildEvalMetric("llm-test-provider", 1)
	base := &LLMBaseEvaluator{LLMEvaluator: &scriptedLLMEvaluator{constructErr: assert.AnError}}
	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{}},
		evalMetric,
	)
	require.Error(t, err)

	base.LLMEvaluator = &scriptedLLMEvaluator{scoreErr: assert.AnError, scoreValue: 1}
	_, err = base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{}},
		evalMetric,
	)
	require.Error(t, err)

	base.LLMEvaluator = &scriptedLLMEvaluator{aggregateSamplesErr: assert.AnError, scoreValue: 1}
	_, err = base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{}},
		evalMetric,
	)
	require.Error(t, err)
}

func TestLLMBaseEvaluator_ScoreBelowThreshold(t *testing.T) {
	provider.Register("llm-low-score-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}}, nil
	})
	base := &LLMBaseEvaluator{LLMEvaluator: &scriptedLLMEvaluator{scoreValue: 0}}
	evalMetric := buildEvalMetric("llm-low-score-provider", 1)
	actual := &evalset.Invocation{InvocationID: "a"}
	expected := &evalset.Invocation{InvocationID: "b"}

	res, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		evalMetric,
	)
	require.NoError(t, err)
	require.Len(t, res.PerInvocationResults, 1)
	assert.Equal(t, status.EvalStatusFailed, res.PerInvocationResults[0].Status)
}

func TestJudgeModelResponse_UnknownProvider(t *testing.T) {
	evalMetric := buildEvalMetric("unknown-provider", 1)
	_, err := judgeModelResponse(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)
}

func TestLLMBaseEvaluator_JudgeModelError(t *testing.T) {
	base := &LLMBaseEvaluator{LLMEvaluator: &scriptedLLMEvaluator{scoreValue: 1}}
	evalMetric := buildEvalMetric("unknown-provider", 1)
	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{}},
		evalMetric,
	)
	require.Error(t, err)
}

func TestJudgeModelResponseErrors(t *testing.T) {
	provider.Register("llm-error-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{err: assert.AnError}, nil
	})
	evalMetric := buildEvalMetric("llm-error-provider", 1)
	_, err := judgeModelResponse(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)

	provider.Register("llm-response-error-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Error: &model.ResponseError{Message: "bad"},
			Done:  true,
		}}}, nil
	})
	evalMetric = buildEvalMetric("llm-response-error-provider", 1)
	_, err = judgeModelResponse(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)

	provider.Register("llm-no-final-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{}}, nil
	})
	evalMetric = buildEvalMetric("llm-no-final-provider", 1)
	_, err = judgeModelResponse(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)
}

func TestJudgeModelResponsePassesVariantToProvider(t *testing.T) {
	var capturedVariant string
	provider.Register("llm-variant-provider", func(opts *provider.Options) (model.Model, error) {
		capturedVariant = opts.Variant
		return &fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}}, nil
	})
	evalMetric := buildEvalMetric("llm-variant-provider", 1)
	evalMetric.Criterion.LLMJudge.JudgeModel.Variant = "deepseek"

	_, err := judgeModelResponse(context.Background(), []model.Message{{Role: "user", Content: "prompt"}}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, "deepseek", capturedVariant)
}

func TestLLMBaseEvaluator_NameDescription(t *testing.T) {
	base := &LLMBaseEvaluator{}
	assert.Equal(t, "llm_base_evaluator", base.Name())
	assert.Equal(t, "Base evaluator for LLM judge", base.Description())
}

func TestLLMBaseEvaluator_New(t *testing.T) {
	stub := &fakeLLMEvaluator{}
	res := New(stub)
	base, ok := res.(*LLMBaseEvaluator)
	require.True(t, ok)
	assert.Equal(t, stub, base.LLMEvaluator)
}
