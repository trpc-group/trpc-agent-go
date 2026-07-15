//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptEvaluationResultConvertsScoresAndRunDetails(t *testing.T) {
	result, err := AdaptEvaluationResult(&evaluation.EvaluationResult{
		EvalSetID: "validation",
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				EvalCaseID: "case",
				MetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "final_response",
						Score:      0.5,
						EvalStatus: status.EvalStatusFailed,
						Details:    &evalresult.EvalMetricResultDetails{Reason: "answer mismatch"},
					},
				},
				RunDetails: []*evaluation.EvaluationCaseRunDetails{
					{
						Inference: &evaluation.EvaluationInferenceDetails{
							SessionID: "session",
							ExecutionTraces: []*atrace.Trace{
								{
									SessionID: "trace-session",
									Status:    atrace.TraceStatusCompleted,
								},
							},
						},
					},
				},
				EvalCaseResults: []*evalresult.EvalCaseResult{
					{
						EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
							{
								ActualInvocation: &evalset.Invocation{
									FinalResponse: assistantMessage("actual"),
								},
								ExpectedInvocation: &evalset.Invocation{
									FinalResponse: assistantMessage("expected"),
								},
								EvalMetricResults: []*evalresult.EvalMetricResult{
									{MetricName: "final_response", EvalStatus: status.EvalStatusFailed},
								},
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 0.5, result.OverallScore)
	require.Len(t, result.EvalSets, 1)
	require.Len(t, result.EvalSets[0].Cases, 1)
	assert.Equal(t, "trace-session", result.EvalSets[0].Cases[0].Trace.SessionID)
	assert.Equal(t, "answer mismatch", result.EvalSets[0].Cases[0].Metrics[0].Reason)
	assert.Equal(t, "actual", result.EvalSets[0].Cases[0].ActualInvocation.FinalResponse.Content)
	assert.Equal(t, "expected", result.EvalSets[0].Cases[0].ExpectedInvocation.FinalResponse.Content)
	assert.Equal(t, "actual", result.EvalSets[0].Cases[0].Metrics[0].ActualInvocation.FinalResponse.Content)
	assert.Equal(t, "expected", result.EvalSets[0].Cases[0].Metrics[0].ExpectedInvocation.FinalResponse.Content)
}

func TestAdaptEvaluationResultUsesFailingInvocationForMetricEvidence(t *testing.T) {
	firstActual := &evalset.Invocation{InvocationID: "actual-1"}
	secondActual := &evalset.Invocation{InvocationID: "actual-2"}
	secondExpected := &evalset.Invocation{InvocationID: "expected-2"}
	result, err := AdaptEvaluationResult(&evaluation.EvaluationResult{
		EvalSetID: "validation",
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				EvalCaseID: "case",
				MetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "quality",
						Score:      0,
						EvalStatus: status.EvalStatusFailed,
						Details:    &evalresult.EvalMetricResultDetails{Reason: "second turn failed"},
					},
				},
				EvalCaseResults: []*evalresult.EvalCaseResult{
					{
						EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
							{
								ActualInvocation: firstActual,
								EvalMetricResults: []*evalresult.EvalMetricResult{
									{MetricName: "quality", EvalStatus: status.EvalStatusPassed},
								},
							},
							{
								ActualInvocation:   secondActual,
								ExpectedInvocation: secondExpected,
								EvalMetricResults: []*evalresult.EvalMetricResult{
									{MetricName: "quality", EvalStatus: status.EvalStatusFailed},
								},
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.EvalSets, 1)
	require.Len(t, result.EvalSets[0].Cases, 1)
	require.Len(t, result.EvalSets[0].Cases[0].Metrics, 1)
	assert.Same(t, firstActual, result.EvalSets[0].Cases[0].ActualInvocation)
	assert.Same(t, secondActual, result.EvalSets[0].Cases[0].Metrics[0].ActualInvocation)
	assert.Same(t, secondExpected, result.EvalSets[0].Cases[0].Metrics[0].ExpectedInvocation)
}

func TestAdaptEvaluationResultDisambiguatesFailedInvocationsByReason(t *testing.T) {
	firstActual := &evalset.Invocation{InvocationID: "actual-1"}
	secondActual := &evalset.Invocation{InvocationID: "actual-2"}
	secondExpected := &evalset.Invocation{InvocationID: "expected-2"}
	result, err := AdaptEvaluationResult(&evaluation.EvaluationResult{
		EvalSetID: "validation",
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				EvalCaseID: "case",
				MetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "quality",
						Score:      0,
						EvalStatus: status.EvalStatusFailed,
						Details:    &evalresult.EvalMetricResultDetails{Reason: "second turn failed"},
					},
				},
				EvalCaseResults: []*evalresult.EvalCaseResult{
					{
						EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
							{
								ActualInvocation: firstActual,
								EvalMetricResults: []*evalresult.EvalMetricResult{
									{
										MetricName: "quality",
										EvalStatus: status.EvalStatusFailed,
										Details:    &evalresult.EvalMetricResultDetails{Reason: "first turn failed"},
									},
								},
							},
							{
								ActualInvocation:   secondActual,
								ExpectedInvocation: secondExpected,
								EvalMetricResults: []*evalresult.EvalMetricResult{
									{
										MetricName: "quality",
										EvalStatus: status.EvalStatusFailed,
										Details:    &evalresult.EvalMetricResultDetails{Reason: "second turn failed"},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.EvalSets, 1)
	require.Len(t, result.EvalSets[0].Cases, 1)
	require.Len(t, result.EvalSets[0].Cases[0].Metrics, 1)
	assert.Same(t, secondActual, result.EvalSets[0].Cases[0].Metrics[0].ActualInvocation)
	assert.Same(t, secondExpected, result.EvalSets[0].Cases[0].Metrics[0].ExpectedInvocation)
}

func TestEnginePromptIteratorRejectsNilEngine(t *testing.T) {
	_, err := EnginePromptIterator{}.Run(context.Background(), &promptiterengine.RunRequest{})
	assert.ErrorContains(t, err, "promptiter engine is nil")
}

func TestEnginePromptIteratorDelegates(t *testing.T) {
	expected := &promptiterengine.RunResult{Status: promptiterengine.RunStatusSucceeded}
	engine := &adapterFakeEngine{result: expected}
	got, err := EnginePromptIterator{Engine: engine}.Run(context.Background(), &promptiterengine.RunRequest{MaxRounds: 2})
	require.NoError(t, err)
	assert.Same(t, expected, got)
	require.NotNil(t, engine.request)
	assert.Equal(t, 2, engine.request.MaxRounds)
}

func TestEvaluationServiceEvaluatorRejectsNilEvaluator(t *testing.T) {
	_, err := EvaluationServiceEvaluator{}.Evaluate(context.Background(), EvaluationRequest{})
	assert.ErrorContains(t, err, "evaluation service evaluator is nil")
}

func TestEvaluationServiceEvaluatorPropagatesErrors(t *testing.T) {
	_, err := EvaluationServiceEvaluator{
		Evaluator: &adapterFakeEvaluator{err: errors.New("eval failed")},
	}.Evaluate(context.Background(), EvaluationRequest{EvalSetID: "validation"})
	assert.ErrorContains(t, err, "eval failed")

	_, err = EvaluationServiceEvaluator{
		Evaluator: &adapterFakeEvaluator{},
		PromptApplier: PromptApplierFunc(func(EvaluationRequest) ([]evaluation.Option, error) {
			return nil, errors.New("apply failed")
		}),
	}.Evaluate(context.Background(), EvaluationRequest{EvalSetID: "validation"})
	assert.ErrorContains(t, err, "apply prompt")
}

func TestEvaluationServiceEvaluatorDelegatesAndAdapts(t *testing.T) {
	evaluator := &adapterFakeEvaluator{
		result: &evaluation.EvaluationResult{
			EvalSetID: "validation",
			EvalCases: []*evaluation.EvaluationCaseResult{
				{
					EvalCaseID: "case",
					MetricResults: []*evalresult.EvalMetricResult{
						{MetricName: "m", Score: 1, EvalStatus: status.EvalStatusPassed},
					},
					RunDetails: []*evaluation.EvaluationCaseRunDetails{{Inference: &evaluation.EvaluationInferenceDetails{SessionID: "session"}}},
				},
			},
		},
	}
	result, err := EvaluationServiceEvaluator{Evaluator: evaluator}.Evaluate(
		context.Background(),
		EvaluationRequest{EvalSetID: "validation"},
	)
	require.NoError(t, err)
	assert.Equal(t, "validation", evaluator.evalSetID)
	assert.Equal(t, 1.0, result.OverallScore)
}

func TestEvaluationServiceEvaluatorAppliesPromptSurfaceOptions(t *testing.T) {
	evaluator := &adapterFakeEvaluator{
		result: &evaluation.EvaluationResult{
			EvalSetID: "validation",
			EvalCases: []*evaluation.EvaluationCaseResult{
				{
					EvalCaseID: "case",
					MetricResults: []*evalresult.EvalMetricResult{
						{MetricName: "m", Score: 1, EvalStatus: status.EvalStatusPassed},
					},
				},
			},
		},
	}
	var seen EvaluationRequest
	_, err := EvaluationServiceEvaluator{
		Evaluator: evaluator,
		PromptApplier: PromptApplierFunc(func(request EvaluationRequest) ([]evaluation.Option, error) {
			seen = request
			return TextPromptSurfaceApplier{SurfaceIDs: []string{"support_agent#instruction"}}.
				EvaluationOptions(request)
		}),
	}.Evaluate(
		context.Background(),
		EvaluationRequest{
			EvalSetID: "validation",
			Prompt:    "baseline prompt from file",
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "validation", seen.EvalSetID)
	assert.Equal(t, "baseline prompt from file", seen.Prompt)
}

func TestEvaluationServiceEvaluatorPatchesPromptThroughEvaluationService(t *testing.T) {
	ctx := context.Background()
	evalSetManager := evalsetinmemory.New()
	_, err := evalSetManager.Create(ctx, "app", "validation")
	require.NoError(t, err)
	require.NoError(t, evalSetManager.AddCase(ctx, "app", "validation", &evalset.EvalCase{EvalID: "case"}))
	capturing := &adapterCapturingService{}
	agentEvaluator, err := evaluation.New(
		"app",
		adapterNoopRunner{},
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithEvaluationService(capturing),
		evaluation.WithRunDetailsEnabled(true),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agentEvaluator.Close() })

	result, err := EvaluationServiceEvaluator{
		Evaluator: agentEvaluator,
		PromptApplier: TextPromptSurfaceApplier{
			SurfaceIDs: []string{"support_agent#instruction"},
		},
	}.Evaluate(ctx, EvaluationRequest{
		EvalSetID: "validation",
		Prompt:    "patched instruction from prompt source",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, capturing.inferenceOptions, 1)
	runOptions := agent.NewRunOptions(capturing.inferenceOptions[0].RunOptions...)
	assert.NotEmpty(t, runOptions.CustomAgentConfigs)
}

func TestTextPromptSurfaceApplierPreservesCallableToolAndSiblings(t *testing.T) {
	lookupCalled := false
	siblingCalled := false
	lookupTool := adapterCallableTool{
		decl: &tool.Declaration{
			Name:        "lookup",
			Description: "old lookup description",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"id": {Type: "string", Description: "old id"},
				},
			},
		},
		call: func(_ context.Context, raw []byte) (any, error) {
			lookupCalled = true
			assert.JSONEq(t, `{"id":"A"}`, string(raw))
			return "lookup:A", nil
		},
	}
	siblingTool := adapterCallableTool{
		decl: &tool.Declaration{Name: "sibling", Description: "sibling description"},
		call: func(context.Context, []byte) (any, error) {
			siblingCalled = true
			return "sibling", nil
		},
	}
	runOption, err := promptSurfaceRunOption("support_agent#tool.lookup", "patched lookup description")
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOption)
	patch, ok := surfacepatch.PatchForNode(opts.CustomAgentConfigs, "support_agent")
	require.True(t, ok)
	declarations, ok := patch.ToolDeclarations()
	require.True(t, ok)
	effectiveTools := itool.ApplyDeclarations([]tool.Tool{lookupTool, siblingTool}, declarations)

	toolsByName := toolsByDeclarationName(effectiveTools)
	require.Contains(t, toolsByName, "lookup")
	require.Contains(t, toolsByName, "sibling")
	assert.Equal(t, "patched lookup description", toolsByName["lookup"].Declaration().Description)
	require.NotNil(t, toolsByName["lookup"].Declaration().InputSchema)
	assert.Equal(t, "old id", toolsByName["lookup"].Declaration().InputSchema.Properties["id"].Description)

	lookupCallable, ok := toolsByName["lookup"].(tool.CallableTool)
	require.True(t, ok)
	lookupResult, err := lookupCallable.Call(context.Background(), []byte(`{"id":"A"}`))
	require.NoError(t, err)
	assert.Equal(t, "lookup:A", lookupResult)
	assert.True(t, lookupCalled)

	siblingCallable, ok := toolsByName["sibling"].(tool.CallableTool)
	require.True(t, ok)
	siblingResult, err := siblingCallable.Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "sibling", siblingResult)
	assert.True(t, siblingCalled)
}

func TestTextPromptSurfaceApplierAppliesProfileWithoutPromptText(t *testing.T) {
	profile, err := BuildPromptProfile([]string{"support_agent#tool.lookup"}, "patched lookup description")
	require.NoError(t, err)
	options, err := TextPromptSurfaceApplier{SurfaceIDs: []string{"support_agent#tool.lookup"}}.
		EvaluationOptions(EvaluationRequest{Profile: profile})
	require.NoError(t, err)
	require.Len(t, options, 1)
}

func TestTextPromptSurfaceApplierRejectsUnsupportedBuiltInSurfaces(t *testing.T) {
	_, err := TextPromptSurfaceApplier{SurfaceIDs: []string{"support_agent#few_shot"}}.
		EvaluationOptions(EvaluationRequest{Prompt: "prompt"})
	assert.ErrorContains(t, err, "custom PromptIterator/PromptApplier integration")

	_, err = TextPromptSurfaceApplier{SurfaceIDs: []string{"support_agent#instruction", "router#instruction"}}.
		EvaluationOptions(EvaluationRequest{Prompt: "prompt"})
	assert.ErrorContains(t, err, "requires exactly one target surface id")
}

func TestTextPromptSurfaceApplierNoopsForEmptyInputs(t *testing.T) {
	options, err := TextPromptSurfaceApplier{SurfaceIDs: []string{"support_agent#instruction"}}.
		EvaluationOptions(EvaluationRequest{})
	require.NoError(t, err)
	assert.Nil(t, options)

	options, err = TextPromptSurfaceApplier{}.EvaluationOptions(EvaluationRequest{Prompt: "prompt"})
	require.NoError(t, err)
	assert.Nil(t, options)
}

func TestTextPromptSurfaceApplierRejectsModelSurfaceWithoutAdapter(t *testing.T) {
	_, err := TextPromptSurfaceApplier{SurfaceIDs: []string{"support_agent#model"}}.
		EvaluationOptions(EvaluationRequest{Prompt: "prompt"})
	assert.ErrorContains(t, err, "concrete model")
}

func TestPromptApplierFuncNilReturnsNoOptions(t *testing.T) {
	options, err := (PromptApplierFunc)(nil).EvaluationOptions(EvaluationRequest{})
	require.NoError(t, err)
	assert.Nil(t, options)
}

func TestPromptSurfaceRunOptionPatchesAgentRunOptions(t *testing.T) {
	runOption, err := promptSurfaceRunOption("support_agent#instruction", "baseline prompt")
	require.NoError(t, err)
	opts := agent.NewRunOptions(runOption)
	assert.NotEmpty(t, opts.CustomAgentConfigs)
}

func TestPromptSurfaceRunOptionSupportsTextSurfaceTypesAndErrors(t *testing.T) {
	for _, surfaceID := range []string{
		"support_agent#global_instruction",
	} {
		runOption, err := promptSurfaceRunOption(surfaceID, "prompt")
		require.NoError(t, err, surfaceID)
		assert.NotEmpty(t, agent.NewRunOptions(runOption).CustomAgentConfigs)
	}
	runOption, err := promptSurfaceRunOption("support_agent#tool.billing_lookup", "prompt")
	require.NoError(t, err)
	assert.NotEmpty(t, agent.NewRunOptions(runOption).CustomAgentConfigs)
	_, err = promptSurfaceRunOption("support_agent#skill.refund_policy", "prompt")
	assert.ErrorContains(t, err, "custom PromptIterator/PromptApplier integration")
	_, err = promptSurfaceRunOption("bad-surface", "prompt")
	assert.ErrorContains(t, err, "invalid prompt surface id")
	_, err = promptSurfaceRunOption("support_agent#unknown", "prompt")
	assert.ErrorContains(t, err, "not a supported prompt surface")
}

func TestBuildTextPromptProfileUsesPromptSourceText(t *testing.T) {
	profile, err := BuildTextPromptProfile(
		[]string{"support_agent#instruction"},
		"baseline prompt\n",
	)
	require.NoError(t, err)
	require.NotNil(t, profile)
	require.Len(t, profile.Overrides, 1)
	require.NotNil(t, profile.Overrides[0].Value.Text)
	assert.Equal(t, "baseline prompt\n", *profile.Overrides[0].Value.Text)
}

func TestBuildPromptProfileCoversEmptyInvalidAndUnsupportedSurfaces(t *testing.T) {
	profile, err := BuildPromptProfile([]string{"support_agent#instruction"}, "")
	require.NoError(t, err)
	assert.Nil(t, profile)

	profile, err = BuildPromptProfile(nil, "prompt")
	require.NoError(t, err)
	assert.Nil(t, profile)

	_, err = BuildPromptProfile([]string{"support_agent#instruction", "support_agent#global_instruction"}, "prompt")
	assert.ErrorContains(t, err, "requires exactly one target surface id")

	_, err = BuildPromptProfile([]string{"support_agent#few_shot"}, "few shot prompt")
	assert.ErrorContains(t, err, "custom PromptIterator/PromptApplier integration")

	profile, err = BuildPromptProfile([]string{"support_agent#tool.billing_lookup"}, "prompt")
	require.NoError(t, err)
	require.Len(t, profile.Overrides, 1)
	assert.Equal(t, "billing_lookup", profile.Overrides[0].Value.Tools[0].ID)
	assert.Equal(t, "prompt", profile.Overrides[0].Value.Tools[0].Description)

	_, err = BuildPromptProfile([]string{"support_agent#model"}, "prompt")
	assert.ErrorContains(t, err, "model surface")
	_, err = BuildPromptProfile([]string{"invalid"}, "prompt")
	assert.ErrorContains(t, err, "invalid prompt surface id")
}

func TestPromptSurfaceValueRejectsUnsupportedSurface(t *testing.T) {
	_, err := promptSurfaceValue("agent", astructure.SurfaceType("bad"), "", "prompt")
	assert.ErrorContains(t, err, "unsupported surface type")
}

func toolsByDeclarationName(tools []tool.Tool) map[string]tool.Tool {
	byName := make(map[string]tool.Tool, len(tools))
	for _, candidate := range tools {
		if candidate == nil || candidate.Declaration() == nil {
			continue
		}
		byName[candidate.Declaration().Name] = candidate
	}
	return byName
}

type adapterCallableTool struct {
	decl *tool.Declaration
	call func(context.Context, []byte) (any, error)
}

func (t adapterCallableTool) Declaration() *tool.Declaration {
	return t.decl
}

func (t adapterCallableTool) Call(ctx context.Context, args []byte) (any, error) {
	return t.call(ctx, args)
}

func TestAdaptEvaluationResultRejectsNilAndNoScores(t *testing.T) {
	_, err := AdaptEvaluationResult(nil)
	assert.ErrorContains(t, err, "evaluation result is nil")

	_, err = AdaptEvaluationResult(&evaluation.EvaluationResult{
		EvalSetID: "empty",
		EvalCases: []*evaluation.EvaluationCaseResult{
			nil,
			{
				EvalCaseID: "not-evaluated",
				MetricResults: []*evalresult.EvalMetricResult{
					nil,
					{MetricName: "m", EvalStatus: status.EvalStatusNotEvaluated},
				},
			},
		},
	})
	assert.ErrorContains(t, err, "has no metric scores")

	sessionID, trace := firstRunDetails(nil)
	assert.Empty(t, sessionID)
	assert.Nil(t, trace)
	sessionID, trace = firstRunDetails([]*evaluation.EvaluationCaseRunDetails{{}})
	assert.Empty(t, sessionID)
	assert.Nil(t, trace)
}

type adapterFakeEvaluator struct {
	evalSetID string
	result    *evaluation.EvaluationResult
	err       error
}

func (e *adapterFakeEvaluator) Evaluate(_ context.Context, evalSetID string, _ ...evaluation.Option) (*evaluation.EvaluationResult, error) {
	e.evalSetID = evalSetID
	if e.err != nil {
		return nil, e.err
	}
	return e.result, nil
}

func (e *adapterFakeEvaluator) Close() error { return nil }

type adapterFakeEngine struct {
	request *promptiterengine.RunRequest
	result  *promptiterengine.RunResult
}

func (e *adapterFakeEngine) Describe(context.Context) (*astructure.Snapshot, error) {
	return &astructure.Snapshot{}, nil
}

func (e *adapterFakeEngine) Run(
	_ context.Context,
	request *promptiterengine.RunRequest,
	_ ...promptiterengine.Option,
) (*promptiterengine.RunResult, error) {
	e.request = request
	return e.result, nil
}

type adapterNoopRunner struct{}

func (adapterNoopRunner) Run(
	context.Context,
	string,
	string,
	model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (adapterNoopRunner) Close() error { return nil }

type adapterCapturingService struct {
	inferenceOptions []*service.Options
	evaluateOptions  []*service.Options
}

func (s *adapterCapturingService) Inference(
	_ context.Context,
	req *service.InferenceRequest,
	opt ...service.Option,
) ([]*service.InferenceResult, error) {
	s.inferenceOptions = append(s.inferenceOptions, service.NewOptions(opt...))
	return []*service.InferenceResult{
		{
			AppName:    req.AppName,
			EvalSetID:  req.EvalSetID,
			EvalCaseID: "case",
			SessionID:  "session",
			Status:     status.EvalStatusPassed,
		},
	}, nil
}

func (s *adapterCapturingService) Evaluate(
	_ context.Context,
	req *service.EvaluateRequest,
	opt ...service.Option,
) (*service.EvalSetRunResult, error) {
	s.evaluateOptions = append(s.evaluateOptions, service.NewOptions(opt...))
	return &service.EvalSetRunResult{
		AppName:   req.AppName,
		EvalSetID: req.EvalSetID,
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalID:          "case",
				FinalEvalStatus: status.EvalStatusPassed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "m",
						Score:      1,
						Threshold:  1,
						EvalStatus: status.EvalStatusPassed,
					},
				},
				SessionID: "session",
				UserID:    "user",
			},
		},
	}, nil
}

func (s *adapterCapturingService) Close() error { return nil }
