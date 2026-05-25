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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
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

type fakeJudgeRunner struct {
	events                []*event.Event
	runCalls              int
	structuredOutputNames []string
}

func (f *fakeJudgeRunner) Run(_ context.Context, _ string, _ string, _ model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	f.runCalls++
	runOpts := &agent.RunOptions{}
	for _, opt := range opts {
		opt(runOpts)
	}
	if runOpts.StructuredOutput != nil && runOpts.StructuredOutput.JSONSchema != nil {
		f.structuredOutputNames = append(f.structuredOutputNames, runOpts.StructuredOutput.JSONSchema.Name)
	}
	ch := make(chan *event.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeJudgeRunner) Close() error { return nil }

var _ runner.Runner = (*fakeJudgeRunner)(nil)

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

type structuredLLMEvaluator struct {
	scriptedLLMEvaluator
	structuredOutput *model.StructuredOutput
	structuredErr    error
	structuredCalls  int
	actualLens       []int
	expectedLens     []int
}

func (s *structuredLLMEvaluator) StructuredOutput(_ context.Context, actuals, expecteds []*evalset.Invocation,
	_ *metric.EvalMetric) (*model.StructuredOutput, error) {
	s.structuredCalls++
	s.actualLens = append(s.actualLens, len(actuals))
	s.expectedLens = append(s.expectedLens, len(expecteds))
	if s.structuredErr != nil {
		return nil, s.structuredErr
	}
	return s.structuredOutput, nil
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

func TestLLMBaseEvaluator_UsesJudgeRunnerAndIgnoresJudgeModelNumSamples(t *testing.T) {
	stub := &fakeLLMEvaluator{}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	evalMetric := buildEvalMetric("unknown-provider", 3)

	r := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
				Done:    true,
			}),
		},
	}
	evalMetric.Criterion.LLMJudge.JudgeRunnerOptions = &llm.JudgeRunnerOptions{Runner: r}

	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}},
		[]*evalset.Invocation{{InvocationID: "b"}},
		evalMetric,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, r.runCalls)
}

func TestLLMBaseEvaluator_UsesJudgeRunnerNumSamples(t *testing.T) {
	stub := &fakeLLMEvaluator{}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	evalMetric := buildEvalMetric("unknown-provider", 1)

	r := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
				Done:    true,
			}),
		},
	}
	numSamples := 3
	evalMetric.Criterion.LLMJudge.JudgeRunnerOptions = &llm.JudgeRunnerOptions{
		Runner:     r,
		NumSamples: &numSamples,
	}

	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}},
		[]*evalset.Invocation{{InvocationID: "b"}},
		evalMetric,
	)
	require.NoError(t, err)
	assert.Equal(t, 3, r.runCalls)
	require.Len(t, stub.receivedSamples, 3)
}

func TestLLMBaseEvaluator_RejectsInvalidJudgeRunnerNumSamples(t *testing.T) {
	base := &LLMBaseEvaluator{LLMEvaluator: &fakeLLMEvaluator{}}
	numSamples := 0
	r := &fakeJudgeRunner{}
	evalMetric := &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				JudgeRunnerOptions: &llm.JudgeRunnerOptions{
					Runner:     r,
					NumSamples: &numSamples,
				},
			},
		},
	}

	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}},
		[]*evalset.Invocation{{InvocationID: "b"}},
		evalMetric,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "num samples must be greater than 0")
	assert.Equal(t, 0, r.runCalls)
}

func TestLLMBaseEvaluator_ResolveStructuredOutput(t *testing.T) {
	base := &LLMBaseEvaluator{}
	output, err := base.resolveStructuredOutput(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, output)
	output, err = base.resolveStructuredOutput(context.Background(), nil, nil,
		&metric.EvalMetric{MetricName: "final_response"})
	require.NoError(t, err)
	assert.Nil(t, output)
}

func TestLLMBaseEvaluator_ResolveStructuredOutputUsesProvider(t *testing.T) {
	expected := &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:   "custom_schema",
			Schema: map[string]any{"type": "object"},
		},
	}
	stub := &structuredLLMEvaluator{structuredOutput: expected}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	actuals := []*evalset.Invocation{{InvocationID: "a"}}
	expecteds := []*evalset.Invocation{{InvocationID: "b"}}
	output, err := base.resolveStructuredOutput(context.Background(), actuals, expecteds,
		&metric.EvalMetric{MetricName: "final_response"})
	require.NoError(t, err)
	assert.Same(t, expected, output)
	assert.Equal(t, 1, stub.structuredCalls)
	assert.Equal(t, []int{1}, stub.actualLens)
	assert.Equal(t, []int{1}, stub.expectedLens)
}

func TestLLMBaseEvaluator_ResolveStructuredOutputRespectsProviderNil(t *testing.T) {
	base := &LLMBaseEvaluator{LLMEvaluator: &structuredLLMEvaluator{}}
	output, err := base.resolveStructuredOutput(context.Background(), nil, nil,
		&metric.EvalMetric{MetricName: "final_response"})
	require.NoError(t, err)
	assert.Nil(t, output)
}

func TestLLMBaseEvaluator_EvaluateResolvesStructuredOutputAfterConstructMessages(t *testing.T) {
	stub := &structuredLLMEvaluator{
		scriptedLLMEvaluator: scriptedLLMEvaluator{constructErr: assert.AnError},
	}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{}},
		buildEvalMetric("unknown-provider", 1),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "construct messages")
	assert.Equal(t, 0, stub.structuredCalls)
}

func TestLLMBaseEvaluator_EvaluateResolvesStructuredOutputPerInvocation(t *testing.T) {
	r := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
				Done:    true,
			}, event.WithStructuredOutputPayload(map[string]any{"score": 1})),
		},
	}
	stub := &structuredLLMEvaluator{
		scriptedLLMEvaluator: scriptedLLMEvaluator{scoreValue: 1},
		structuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "custom_schema",
				Schema: map[string]any{"type": "object"},
				Strict: true,
			},
		},
	}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	evalMetric := buildEvalMetric("unused-provider", 1)
	evalMetric.Criterion.LLMJudge.JudgeRunnerOptions = &llm.JudgeRunnerOptions{Runner: r}
	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}, {InvocationID: "b"}},
		[]*evalset.Invocation{{InvocationID: "c"}, {InvocationID: "d"}},
		evalMetric,
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, stub.actualLens)
	assert.Equal(t, []int{1, 2}, stub.expectedLens)
	assert.Equal(t, []string{"custom_schema", "custom_schema"}, r.structuredOutputNames)
}

func TestLLMBaseEvaluator_EvaluateRejectsStructuredOutputError(t *testing.T) {
	stub := &structuredLLMEvaluator{
		scriptedLLMEvaluator: scriptedLLMEvaluator{scoreValue: 1},
		structuredErr:        assert.AnError,
	}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	evalMetric := buildEvalMetric("unknown-provider", 1)
	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{}},
		[]*evalset.Invocation{{}},
		evalMetric,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve structured output")
}

func TestLLMBaseEvaluator_AllowsJudgeRunnerWithoutJudgeModel(t *testing.T) {
	stub := &fakeLLMEvaluator{}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}

	r := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
				Done:    true,
			}),
		},
	}

	evalMetric := &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{},
		},
	}
	evalMetric.Criterion.LLMJudge.JudgeRunnerOptions = &llm.JudgeRunnerOptions{Runner: r}

	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}},
		[]*evalset.Invocation{{InvocationID: "b"}},
		evalMetric,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, r.runCalls)
}

func TestLLMBaseEvaluator_EvaluateMissingJudgeModelAndRunner(t *testing.T) {
	base := &LLMBaseEvaluator{LLMEvaluator: &scriptedLLMEvaluator{scoreValue: 1}}
	evalMetric := &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{},
		},
	}

	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}},
		[]*evalset.Invocation{{InvocationID: "b"}},
		evalMetric,
	)
	require.Error(t, err)
}

func TestLLMBaseEvaluator_EvaluateUsesDefaultNumSamplesWhenNil(t *testing.T) {
	provider.Register("llm-default-num-samples-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}}, nil
	})
	stub := &fakeLLMEvaluator{}
	base := &LLMBaseEvaluator{LLMEvaluator: stub}
	evalMetric := buildEvalMetric("llm-default-num-samples-provider", 3)
	evalMetric.Criterion.LLMJudge.JudgeModel.NumSamples = nil

	_, err := base.Evaluate(
		context.Background(),
		[]*evalset.Invocation{{InvocationID: "a"}},
		[]*evalset.Invocation{{InvocationID: "b"}},
		evalMetric,
	)
	require.NoError(t, err)
}
