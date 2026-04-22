//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package judger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type marshalingFailure struct {
	F func()
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

type capturingModel struct {
	fakeModel
	request *model.Request
}

func (m *capturingModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.request = req
	return m.fakeModel.GenerateContent(ctx, req)
}

type fakeJudgeRunner struct {
	events []*event.Event
}

func (f *fakeJudgeRunner) Run(_ context.Context, _ string, _ string, _ model.Message,
	_ ...agent.RunOption) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeJudgeRunner) Close() error {
	return nil
}

var _ runner.Runner = (*fakeJudgeRunner)(nil)

type capturingJudgeRunner struct {
	events  []*event.Event
	runOpts *agent.RunOptions
}

func (r *capturingJudgeRunner) Run(_ context.Context, _ string, _ string, _ model.Message,
	runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	opts := &agent.RunOptions{}
	for _, opt := range runOpts {
		opt(opts)
	}
	r.runOpts = opts
	ch := make(chan *event.Event, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (r *capturingJudgeRunner) Close() error {
	return nil
}

type errorJudgeRunner struct {
	err error
}

func (e *errorJudgeRunner) Run(_ context.Context, _ string, _ string, _ model.Message,
	_ ...agent.RunOption) (<-chan *event.Event, error) {
	return nil, e.err
}

func (e *errorJudgeRunner) Close() error {
	return nil
}

func buildEvalMetric(providerName string, numSamples int) *metric.EvalMetric {
	return &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeModel: &criterionllm.JudgeModelOptions{
					ProviderName: providerName,
					ModelName:    "fake-model",
					NumSamples:   &numSamples,
					Generation:   &model.GenerationConfig{},
				},
			},
		},
	}
}

func TestJudgeWithRunner_JudgeRunnerNil(t *testing.T) {
	_, err := judgeWithRunner(context.Background(), nil, []model.Message{}, nil)
	require.Error(t, err)
}

func TestJudge_MissingRequiredFields(t *testing.T) {
	_, err := Judge(context.Background(), []model.Message{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required fields in eval metric")
}

func TestJudge_UsesRunnerWhenConfigured(t *testing.T) {
	finalResponse := &model.Response{
		Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
		Done:    true,
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeRunnerOptions: &criterionllm.JudgeRunnerOptions{
					Runner: &fakeJudgeRunner{
						events: []*event.Event{
							event.NewResponseEvent("inv", "judge", finalResponse),
						},
					},
				},
			},
		},
	}
	got, err := Judge(context.Background(), []model.Message{{Role: model.RoleUser, Content: "prompt"}}, evalMetric)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "ok", got.Choices[0].Message.Content)
	assert.NotSame(t, finalResponse, got)
}

func TestJudgeWithRunner_EventError(t *testing.T) {
	r := &fakeJudgeRunner{
		events: []*event.Event{
			nil,
			event.NewErrorEvent("inv", "judge", model.ErrorTypeRunError, "bad"),
		},
	}
	_, err := judgeWithRunner(
		context.Background(),
		r,
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		nil,
	)
	require.Error(t, err)
}

func TestJudgeWithRunner_RunError(t *testing.T) {
	_, err := judgeWithRunner(
		context.Background(),
		&errorJudgeRunner{err: assert.AnError},
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runner run")
}

func TestJudge_UnknownProvider(t *testing.T) {
	evalMetric := buildEvalMetric("unknown-provider", 1)
	_, err := Judge(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)
}

func TestJudge_ModelErrors(t *testing.T) {
	provider.Register("llm-error-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{err: assert.AnError}, nil
	})
	evalMetric := buildEvalMetric("llm-error-provider", 1)
	_, err := Judge(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)

	provider.Register("llm-response-error-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Error: &model.ResponseError{Message: "bad"},
			Done:  true,
		}}}, nil
	})
	evalMetric = buildEvalMetric("llm-response-error-provider", 1)
	_, err = Judge(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)

	provider.Register("llm-no-final-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{}}, nil
	})
	evalMetric = buildEvalMetric("llm-no-final-provider", 1)
	_, err = Judge(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)
}

func TestJudge_UsesDefaultGenerationWhenUnset(t *testing.T) {
	provider.Register("llm-default-generation-provider", func(_ *provider.Options) (model.Model, error) {
		return &fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}}, nil
	})
	evalMetric := buildEvalMetric("llm-default-generation-provider", 1)
	evalMetric.Criterion.LLMJudge.JudgeModel.Generation = nil
	_, err := Judge(context.Background(), []model.Message{{Role: model.RoleUser, Content: "prompt"}}, evalMetric)
	require.NoError(t, err)
}

func TestJudge_PassesVariantToProvider(t *testing.T) {
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

	_, err := Judge(context.Background(), []model.Message{{Role: "user", Content: "prompt"}}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, "deepseek", capturedVariant)
}

func TestJudge_JudgeModelNil(t *testing.T) {
	evalMetric := &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{},
		},
	}
	_, err := Judge(context.Background(), []model.Message{}, evalMetric)
	require.Error(t, err)
}

func TestJudgeWithRunner_NoFinalResponse(t *testing.T) {
	r := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "partial"}}},
				Done:    false,
			}),
		},
	}
	_, err := judgeWithRunner(
		context.Background(),
		r,
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		nil,
	)
	require.Error(t, err)
}

func TestJudge_PassesStructuredOutputToModel(t *testing.T) {
	capturing := &capturingModel{
		fakeModel: fakeModel{responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
			Done:    true,
		}}},
	}
	provider.Register("llm-structured-output-provider", func(_ *provider.Options) (model.Model, error) {
		return capturing, nil
	})
	out := &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:   "judge_result",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
	}

	_, err := Judge(
		context.Background(),
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		buildEvalMetric("llm-structured-output-provider", 1),
		WithStructuredOutput(out),
	)
	require.NoError(t, err)
	require.NotNil(t, capturing.request)
	assert.Equal(t, out, capturing.request.StructuredOutput)
}

func TestJudgeWithRunner_MaterializesStructuredOutputContent(t *testing.T) {
	r := &capturingJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant}}},
				Done:    true,
			}, event.WithStructuredOutputPayload(map[string]any{
				"score":  1,
				"reason": "Looks good.",
			})),
		},
	}
	out := &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:        "judge_result",
			Schema:      map[string]any{"type": "object"},
			Strict:      true,
			Description: "Judge result schema.",
		},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeRunnerOptions: &criterionllm.JudgeRunnerOptions{Runner: r},
			},
		},
	}

	got, err := Judge(
		context.Background(),
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		evalMetric,
		WithStructuredOutput(out),
	)
	require.NoError(t, err)
	require.NotNil(t, r.runOpts)
	require.NotNil(t, r.runOpts.StructuredOutput)
	assert.Equal(t, "judge_result", r.runOpts.StructuredOutput.JSONSchema.Name)
	require.NotNil(t, got)
	require.Len(t, got.Choices, 1)
	assert.JSONEq(t, `{"score":1,"reason":"Looks good."}`, got.Choices[0].Message.Content)
}

func TestJudgeWithRunner_RequiresStructuredOutputPayload(t *testing.T) {
	r := &capturingJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "not-json"}}},
				Done:    true,
			}),
		},
	}
	out := &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:   "judge_result",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeRunnerOptions: &criterionllm.JudgeRunnerOptions{Runner: r},
			},
		},
	}

	_, err := Judge(
		context.Background(),
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		evalMetric,
		WithStructuredOutput(out),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "structured output payload is missing")
}

func TestJudgeWithRunner_RejectsUnsupportedStructuredOutput(t *testing.T) {
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeRunnerOptions: &criterionllm.JudgeRunnerOptions{Runner: &fakeJudgeRunner{}},
			},
		},
	}

	_, err := Judge(
		context.Background(),
		[]model.Message{{Role: model.RoleUser, Content: "prompt"}},
		evalMetric,
		WithStructuredOutput(&model.StructuredOutput{Type: "unsupported"}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported structured output for judge runner")
}

func TestMaterializeStructuredOutputContent(t *testing.T) {
	out := &model.StructuredOutput{
		Type: model.StructuredOutputJSONSchema,
		JSONSchema: &model.JSONSchemaConfig{
			Name:   "judge_result",
			Schema: map[string]any{"type": "object"},
		},
	}
	err := materializeStructuredOutputContent(nil, nil, nil)
	require.NoError(t, err)
	err = materializeStructuredOutputContent(nil, map[string]any{"score": 1}, &options{structuredOutput: out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response is nil")
	err = materializeStructuredOutputContent(&model.Response{}, nil, &options{structuredOutput: out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "structured output payload is missing")
	resp := &model.Response{}
	err = materializeStructuredOutputContent(resp, map[string]any{"score": 1}, &options{structuredOutput: out})
	require.NoError(t, err)
	require.Len(t, resp.Choices, 1)
	assert.JSONEq(t, `{"score":1}`, resp.Choices[0].Message.Content)
	err = materializeStructuredOutputContent(&model.Response{}, marshalingFailure{F: func() {}}, &options{structuredOutput: out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal structured output payload")
}
