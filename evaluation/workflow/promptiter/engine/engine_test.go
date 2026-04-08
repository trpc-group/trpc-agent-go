//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const testSurfaceID = "node_1#instruction"

type fakeBackwarder struct {
	requests []*backwarder.Request
	fn       func(ctx context.Context, request *backwarder.Request) (*backwarder.Result, error)
}

func (f *fakeBackwarder) Backward(
	ctx context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	f.requests = append(f.requests, request)
	if f.fn == nil {
		return &backwarder.Result{}, nil
	}
	return f.fn(ctx, request)
}

type fakeAggregator struct {
	requests []*aggregator.Request
	fn       func(ctx context.Context, request *aggregator.Request) (*aggregator.Result, error)
}

func (f *fakeAggregator) Aggregate(
	ctx context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	f.requests = append(f.requests, request)
	if f.fn == nil {
		return &aggregator.Result{}, nil
	}
	return f.fn(ctx, request)
}

type fakeOptimizer struct {
	requests []*optimizer.Request
	fn       func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error)
}

func (f *fakeOptimizer) Optimize(
	ctx context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	f.requests = append(f.requests, request)
	if f.fn == nil {
		return &optimizer.Result{}, nil
	}
	return f.fn(ctx, request)
}

type stubRunner struct{}

func (stubRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	_ = ctx
	_ = userID
	_ = sessionID
	_ = message
	_ = runOpts
	return nil, nil
}

func (stubRunner) Close() error {
	return nil
}

type scriptedEvalOutcome struct {
	score             float64
	metricStatus      status.EvalStatus
	reason            string
	appliedSurfaceIDs []string
	missingTrace      bool
	executionTrace    *atrace.Trace
}

type providerBackedTestModel struct {
	name string
}

func (m *providerBackedTestModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *providerBackedTestModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type scriptedEvalService struct {
	profiles         []string
	runOptions       []agent.RunOptions
	profileByEvalSet map[string]string
	outcomeByEvalSet map[string]scriptedEvalOutcome
	script           func(evalSetID string, profileValue string) scriptedEvalOutcome
}

func newScriptedEvalService(
	script func(evalSetID string, profileValue string) scriptedEvalOutcome,
) *scriptedEvalService {
	return &scriptedEvalService{
		profileByEvalSet: make(map[string]string),
		outcomeByEvalSet: make(map[string]scriptedEvalOutcome),
		script:           script,
	}
}

func (s *scriptedEvalService) Inference(
	ctx context.Context,
	req *service.InferenceRequest,
	opt ...service.Option,
) ([]*service.InferenceResult, error) {
	callOptions := service.NewOptions(opt...)
	runOptions := agent.NewRunOptions(callOptions.RunOptions...)
	profileValue := runOptions.Instruction
	if patch, ok := surfacepatch.PatchForNode(runOptions.CustomAgentConfigs, "node_1"); ok {
		if instruction, ok := patch.Instruction(); ok {
			profileValue = instruction
		}
	}
	if profileValue == "" {
		profileValue = "base prompt"
	}
	outcome := s.script(req.EvalSetID, profileValue)
	s.profiles = append(s.profiles, profileValue)
	s.runOptions = append(s.runOptions, runOptions)
	s.profileByEvalSet[req.EvalSetID] = profileValue
	s.outcomeByEvalSet[req.EvalSetID] = outcome
	result := &service.InferenceResult{
		AppName:    req.AppName,
		EvalSetID:  req.EvalSetID,
		EvalCaseID: "case_1",
		SessionID:  "session_1",
		UserID:     "user_1",
		Status:     status.EvalStatusPassed,
		Inferences: []*evalset.Invocation{
			{
				InvocationID: "invocation_1",
			},
		},
		ExecutionTraces: []*atrace.Trace{
			buildScriptedExecutionTrace(outcome),
		},
	}
	if err := runAfterInferenceCaseCallbacks(ctx, callOptions.Callbacks, req, result); err != nil {
		return nil, err
	}
	return []*service.InferenceResult{result}, nil
}

func (s *scriptedEvalService) Evaluate(
	ctx context.Context,
	req *service.EvaluateRequest,
	opt ...service.Option,
) (*service.EvalSetRunResult, error) {
	_ = ctx
	_ = opt
	outcome, ok := s.outcomeByEvalSet[req.EvalSetID]
	if !ok {
		return nil, errors.New("missing scripted outcome")
	}
	threshold := 0.5
	if outcome.metricStatus == status.EvalStatusFailed {
		threshold = 1.0
	}
	return &service.EvalSetRunResult{
		AppName:   req.AppName,
		EvalSetID: req.EvalSetID,
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID:       req.EvalSetID,
				EvalID:          "case_1",
				FinalEvalStatus: outcome.metricStatus,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "quality",
						Score:      outcome.score,
						EvalStatus: outcome.metricStatus,
						Threshold:  threshold,
						Details: &evalresult.EvalMetricResultDetails{
							Reason: outcome.reason,
							Score:  outcome.score,
						},
					},
				},
				SessionID: "session_1",
				UserID:    "user_1",
			},
		},
	}, nil
}

func (s *scriptedEvalService) Close() error {
	return nil
}

func runAfterInferenceCaseCallbacks(
	ctx context.Context,
	callbacks *service.Callbacks,
	req *service.InferenceRequest,
	result *service.InferenceResult,
) error {
	if callbacks == nil {
		return nil
	}
	for _, callback := range callbacks.AfterInferenceCase {
		_, err := callback.Callback(ctx, &service.AfterInferenceCaseArgs{
			Request: req,
			Result:  result,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func buildScriptedExecutionTrace(outcome scriptedEvalOutcome) *atrace.Trace {
	if outcome.missingTrace {
		return nil
	}
	if outcome.executionTrace != nil {
		return outcome.executionTrace
	}
	return &atrace.Trace{
		RootAgentName:    "target",
		RootInvocationID: "invocation_1",
		SessionID:        "session_1",
		Status:           atrace.TraceStatusCompleted,
		Steps: []atrace.Step{
			{
				StepID:            "step_1",
				InvocationID:      "invocation_1",
				NodeID:            "node_1",
				AppliedSurfaceIDs: append([]string(nil), outcome.appliedSurfaceIDs...),
				Input:             &atrace.Snapshot{Text: "input"},
				Output:            &atrace.Snapshot{Text: "output"},
			},
		},
	}
}

func newTestAgentEvaluator(t *testing.T, evalService service.Service) evaluation.AgentEvaluator {
	t.Helper()
	manager := evalsetinmemory.New()
	seedTestEvalSet(t, manager, "train")
	seedTestEvalSet(t, manager, "validation")
	agentEvaluator, err := evaluation.New(
		"promptiter-test",
		stubRunner{},
		evaluation.WithEvaluationService(evalService),
		evaluation.WithEvalSetManager(manager),
	)
	assert.NoError(t, err)
	return agentEvaluator
}

func seedTestEvalSet(t *testing.T, manager evalset.Manager, evalSetID string) {
	t.Helper()
	_, err := manager.Create(context.Background(), "promptiter-test", evalSetID)
	assert.NoError(t, err)
	err = manager.AddCase(context.Background(), "promptiter-test", evalSetID, &evalset.EvalCase{
		EvalID: "case_1",
	})
	assert.NoError(t, err)
}

type fakeStructureAgent struct {
	snapshot  *astructure.Snapshot
	exportErr error
}

func (f *fakeStructureAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	_ = ctx
	_ = invocation
	return nil, errors.New("should not call agent run")
}

func (f *fakeStructureAgent) Tools() []tool.Tool {
	return nil
}

func (f *fakeStructureAgent) Info() agent.Info {
	return agent.Info{Name: "target"}
}

func (f *fakeStructureAgent) SubAgents() []agent.Agent {
	return nil
}

func (f *fakeStructureAgent) FindSubAgent(name string) agent.Agent {
	_ = name
	return nil
}

func (f *fakeStructureAgent) Export(
	ctx context.Context,
	exportChild astructure.ChildExporter,
) (*astructure.Snapshot, error) {
	_ = ctx
	_ = exportChild
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return f.snapshot, nil
}

func TestDescribeUsesStructureSnapshot(t *testing.T) {
	structure := testStructureSnapshot(t)
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	result, err := engineInstance.Describe(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, structure, result)
}

func TestRunAcceptsFirstRoundAndStopsAfterRejectedNextRound(t *testing.T) {
	backward := &fakeBackwarder{
		fn: func(ctx context.Context, request *backwarder.Request) (*backwarder.Result, error) {
			_ = ctx
			currentText := ""
			if len(request.Surfaces) > 0 && request.Surfaces[0].Value.Text != nil {
				currentText = *request.Surfaces[0].Value.Text
			}
			gradientText := "improve prompt"
			if currentText == "accepted prompt" {
				gradientText = "overfit prompt"
			}
			return &backwarder.Result{
				Gradients: []promptiter.SurfaceGradient{
					{
						EvalSetID:  request.EvalSetID,
						EvalCaseID: request.EvalCaseID,
						StepID:     request.StepID,
						SurfaceID:  testSurfaceID,
						Severity:   promptiter.LossSeverityP1,
						Gradient:   gradientText,
					},
				},
			}, nil
		},
	}
	aggregatorInstance := &fakeAggregator{
		fn: func(ctx context.Context, request *aggregator.Request) (*aggregator.Result, error) {
			_ = ctx
			return &aggregator.Result{
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: request.SurfaceID,
					NodeID:    request.NodeID,
					Type:      request.Type,
					Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
				},
			}, nil
		},
	}
	optimizerInstance := &fakeOptimizer{
		fn: func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
			_ = ctx
			nextText := "accepted prompt"
			if request.Gradient.Gradients[0].Gradient == "overfit prompt" {
				nextText = "rejected prompt"
			}
			return &optimizer.Result{
				Patch: &promptiter.SurfacePatch{
					SurfaceID: request.Surface.SurfaceID,
					Value: astructure.SurfaceValue{
						Text: stringPtr(nextText),
					},
					Reason: "update prompt",
				},
			}, nil
		},
	}
	evalService := newScriptedEvalService(scriptedOutcome)
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, evalService),
		backward,
		aggregatorInstance,
		optimizerInstance,
	)
	assert.NoError(t, err)
	result, err := engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		InitialProfile:       nil,
		AcceptancePolicy: AcceptancePolicy{
			MinScoreGain: 0.1,
		},
		StopPolicy: StopPolicy{
			MaxRoundsWithoutAcceptance: 1,
		},
		MaxRounds: 5,
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"base prompt",
		"base prompt",
		"accepted prompt",
		"accepted prompt",
		"rejected prompt",
	}, evalService.profiles)
	assert.Len(t, result.Rounds, 2)
	assert.Equal(t, "accepted prompt", profileText(result.AcceptedProfile))
	assert.True(t, result.Rounds[0].Acceptance.Accepted)
	assert.False(t, result.Rounds[1].Acceptance.Accepted)
	assert.Equal(t, "max rounds without acceptance reached", result.Rounds[1].Stop.Reason)
	assert.Len(t, backward.requests, 2)
	assert.Equal(t, "base prompt", *backward.requests[0].Surfaces[0].Value.Text)
	assert.Equal(t, "accepted prompt", *backward.requests[1].Surfaces[0].Value.Text)
}

func TestRunObserverReceivesRuntimeEvents(t *testing.T) {
	backward := &fakeBackwarder{
		fn: func(ctx context.Context, request *backwarder.Request) (*backwarder.Result, error) {
			_ = ctx
			return &backwarder.Result{
				Gradients: []promptiter.SurfaceGradient{
					{
						EvalSetID:  request.EvalSetID,
						EvalCaseID: request.EvalCaseID,
						StepID:     request.StepID,
						SurfaceID:  testSurfaceID,
						Severity:   promptiter.LossSeverityP1,
						Gradient:   "improve prompt",
					},
				},
			}, nil
		},
	}
	aggregatorInstance := &fakeAggregator{
		fn: func(ctx context.Context, request *aggregator.Request) (*aggregator.Result, error) {
			_ = ctx
			return &aggregator.Result{
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: request.SurfaceID,
					NodeID:    request.NodeID,
					Type:      request.Type,
					Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
				},
			}, nil
		},
	}
	optimizerInstance := &fakeOptimizer{
		fn: func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
			_ = ctx
			return &optimizer.Result{
				Patch: &promptiter.SurfacePatch{
					SurfaceID: request.Surface.SurfaceID,
					Value: astructure.SurfaceValue{
						Text: stringPtr("accepted prompt"),
					},
					Reason: "update prompt",
				},
			}, nil
		},
	}
	evalService := newScriptedEvalService(scriptedOutcome)
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, evalService),
		backward,
		aggregatorInstance,
		optimizerInstance,
	)
	assert.NoError(t, err)
	var observedKinds []EventKind
	result, err := engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		AcceptancePolicy: AcceptancePolicy{
			MinScoreGain: 0.1,
		},
		StopPolicy: StopPolicy{
			MaxRoundsWithoutAcceptance: 1,
		},
		MaxRounds: 1,
	}, WithObserver(func(ctx context.Context, event *Event) error {
		_ = ctx
		if assert.NotNil(t, event) {
			observedKinds = append(observedKinds, event.Kind)
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []EventKind{
		EventKindStructureSnapshot,
		EventKindBaselineValidation,
		EventKindRoundStarted,
		EventKindRoundTrainEvaluation,
		EventKindRoundLosses,
		EventKindRoundBackward,
		EventKindRoundAggregation,
		EventKindRoundPatchSet,
		EventKindRoundOutputProfile,
		EventKindRoundValidation,
		EventKindRoundCompleted,
	}, observedKinds)
}

func TestRunCompilesProfileIntoEvaluationRunOptions(t *testing.T) {
	backward := &fakeBackwarder{
		fn: func(ctx context.Context, request *backwarder.Request) (*backwarder.Result, error) {
			_ = ctx
			return &backwarder.Result{
				Gradients: []promptiter.SurfaceGradient{
					{
						EvalSetID:  request.EvalSetID,
						EvalCaseID: request.EvalCaseID,
						StepID:     request.StepID,
						SurfaceID:  testSurfaceID,
						Severity:   promptiter.LossSeverityP1,
						Gradient:   "improve prompt",
					},
				},
			}, nil
		},
	}
	aggregatorInstance := &fakeAggregator{
		fn: func(ctx context.Context, request *aggregator.Request) (*aggregator.Result, error) {
			_ = ctx
			return &aggregator.Result{
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: request.SurfaceID,
					NodeID:    request.NodeID,
					Type:      request.Type,
					Gradients: append([]promptiter.SurfaceGradient(nil), request.Gradients...),
				},
			}, nil
		},
	}
	optimizerInstance := &fakeOptimizer{
		fn: func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
			_ = ctx
			return &optimizer.Result{
				Patch: &promptiter.SurfacePatch{
					SurfaceID: request.Surface.SurfaceID,
					Value: astructure.SurfaceValue{
						Text: stringPtr("accepted prompt"),
					},
					Reason: "update prompt",
				},
			}, nil
		},
	}
	evalService := newScriptedEvalService(scriptedOutcome)
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, evalService),
		backward,
		aggregatorInstance,
		optimizerInstance,
	)
	assert.NoError(t, err)
	result, err := engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		AcceptancePolicy: AcceptancePolicy{
			MinScoreGain: 0.1,
		},
		MaxRounds: 1,
	})
	assert.NoError(t, err)
	assert.Len(t, result.Rounds, 1)
	assert.Len(t, evalService.runOptions, 3)
	assert.Empty(t, evalService.runOptions[0].Instruction)
	assert.Empty(t, evalService.runOptions[1].Instruction)
	_, ok := surfacepatch.PatchForNode(evalService.runOptions[0].CustomAgentConfigs, "node_1")
	assert.False(t, ok)
	_, ok = surfacepatch.PatchForNode(evalService.runOptions[1].CustomAgentConfigs, "node_1")
	assert.False(t, ok)
	patch, ok := surfacepatch.PatchForNode(evalService.runOptions[2].CustomAgentConfigs, "node_1")
	assert.True(t, ok)
	instruction, ok := patch.Instruction()
	assert.True(t, ok)
	assert.Equal(t, "accepted prompt", instruction)
	assert.True(t, evalService.runOptions[0].ExecutionTraceEnabled)
	assert.True(t, evalService.runOptions[1].ExecutionTraceEnabled)
	assert.True(t, evalService.runOptions[2].ExecutionTraceEnabled)
	assert.Equal(t, "accepted prompt", profileText(result.Rounds[0].OutputProfile))
	assert.Equal(t, "accepted prompt", profileText(result.AcceptedProfile))
}

func TestCompileProfileRunOptionsUsesNodeSurfacePatchForNonEntryNode(t *testing.T) {
	structure, err := newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "entry",
		Nodes: []astructure.Node{
			{NodeID: "entry", Kind: astructure.NodeKindLLM, Name: "entry"},
			{NodeID: "reviewer", Kind: astructure.NodeKindLLM, Name: "reviewer"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "entry#instruction",
				NodeID:    "entry",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("entry prompt"),
				},
			},
			{
				SurfaceID: "reviewer#instruction",
				NodeID:    "reviewer",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("review prompt"),
				},
			},
		},
	})
	assert.NoError(t, err)
	runOptions, err := compileProfileRunOptions(structure, &promptiter.Profile{
		StructureID: "structure_1",
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: "reviewer#instruction",
				Value: astructure.SurfaceValue{
					Text: stringPtr("patched review prompt"),
				},
			},
		},
	})
	assert.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	assert.Empty(t, opts.Instruction)
	assert.True(t, opts.ExecutionTraceEnabled)
	patch, ok := surfacepatch.PatchForNode(opts.CustomAgentConfigs, "reviewer")
	assert.True(t, ok)
	instruction, ok := patch.Instruction()
	assert.True(t, ok)
	assert.Equal(t, "patched review prompt", instruction)
}

func TestCompileProfileRunOptionsUsesModelSurfacePatch(t *testing.T) {
	const providerName = "promptiter_test_provider"
	var capturedOptions provider.Options
	provider.Register(providerName, func(opts *provider.Options) (model.Model, error) {
		capturedOptions = *opts
		return &providerBackedTestModel{name: opts.ModelName}, nil
	})
	structure, err := newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "entry",
		Nodes: []astructure.Node{
			{NodeID: "entry", Kind: astructure.NodeKindLLM, Name: "entry"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "entry#model",
				NodeID:    "entry",
				Type:      astructure.SurfaceTypeModel,
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{
						Provider: providerName,
						Name:     "base-model",
					},
				},
			},
		},
	})
	assert.NoError(t, err)
	runOptions, err := compileProfileRunOptions(structure, &promptiter.Profile{
		StructureID: "structure_1",
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: "entry#model",
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{
						Provider: providerName,
						Name:     "patched-model",
						BaseURL:  "https://api.example.com/v1",
						APIKey:   "secret",
						Headers:  map[string]string{"X-Test": "1"},
					},
				},
			},
		},
	})
	assert.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	patch, ok := surfacepatch.PatchForNode(opts.CustomAgentConfigs, "entry")
	assert.True(t, ok)
	modelValue, ok := patch.Model()
	assert.True(t, ok)
	assert.Equal(t, "patched-model", modelValue.Info().Name)
	assert.Equal(t, providerName, capturedOptions.ProviderName)
	assert.Equal(t, "patched-model", capturedOptions.ModelName)
	assert.Equal(t, "https://api.example.com/v1", capturedOptions.BaseURL)
	assert.Equal(t, "secret", capturedOptions.APIKey)
	assert.Equal(t, map[string]string{"X-Test": "1"}, capturedOptions.Headers)
}

func TestCompileProfileRunOptionsDefaultsModelProviderToOpenAI(t *testing.T) {
	structure, err := newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "entry",
		Nodes: []astructure.Node{
			{NodeID: "entry", Kind: astructure.NodeKindLLM, Name: "entry"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "entry#model",
				NodeID:    "entry",
				Type:      astructure.SurfaceTypeModel,
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{Name: "base-model"},
				},
			},
		},
	})
	assert.NoError(t, err)
	runOptions, err := compileProfileRunOptions(structure, &promptiter.Profile{
		StructureID: "structure_1",
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: "entry#model",
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{Name: "patched-model"},
				},
			},
		},
	})
	assert.NoError(t, err)
	opts := agent.NewRunOptions(runOptions...)
	patch, ok := surfacepatch.PatchForNode(opts.CustomAgentConfigs, "entry")
	assert.True(t, ok)
	modelValue, ok := patch.Model()
	assert.True(t, ok)
	assert.Equal(t, "patched-model", modelValue.Info().Name)
}

func TestBuildBackwardRequestKeepsContextSurfacesButRestrictsAllowedGradientSurfaceIDs(t *testing.T) {
	instructionText := "base prompt"
	modelRef := &astructure.ModelRef{Name: "gpt-test"}
	instructionSurfaceID := astructure.SurfaceID("node_1", astructure.SurfaceTypeInstruction)
	modelSurfaceID := astructure.SurfaceID("node_1", astructure.SurfaceTypeModel)
	structure, err := newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM, Name: "writer"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: instructionSurfaceID,
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: &instructionText,
				},
			},
			{
				SurfaceID: modelSurfaceID,
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeModel,
				Value: astructure.SurfaceValue{
					Model: modelRef,
				},
			},
		},
	})
	assert.NoError(t, err)
	request, err := buildBackwardRequest(
		structure,
		nil,
		map[string]indexedTraceStep{},
		CaseResult{EvalSetID: "train", EvalCaseID: "case_1"},
		atrace.Step{
			StepID:            "step_1",
			NodeID:            "node_1",
			AppliedSurfaceIDs: []string{instructionSurfaceID, modelSurfaceID},
		},
		[]backwarder.GradientPacket{
			{
				FromStepID: "step_2",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "focus on the live call",
			},
		},
		targetSurfaceSet{instructionSurfaceID: {}},
	)
	assert.NoError(t, err)
	if assert.NotNil(t, request) {
		assert.Len(t, request.Surfaces, 2)
		assert.Equal(t, []string{instructionSurfaceID}, request.AllowedGradientSurfaceIDs)
	}
}

func TestAggregateRejectsOutOfScopeGradient(t *testing.T) {
	modelSurfaceID := astructure.SurfaceID("node_1", astructure.SurfaceTypeModel)
	structure, err := newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM, Name: "writer"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: modelSurfaceID,
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeModel,
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{Name: "gpt-test"},
				},
			},
		},
	})
	assert.NoError(t, err)
	aggregatorInstance := &fakeAggregator{}
	engineInstance := &engine{aggregator: aggregatorInstance}
	_, err = engineInstance.aggregate(context.Background(), structure, &BackwardResult{
		Cases: []CaseBackwardResult{
			{
				StepGradients: []promptiter.StepGradient{
					{
						StepID: "step_1",
						NodeID: "node_1",
						Gradients: []promptiter.SurfaceGradient{
							{
								SurfaceID: modelSurfaceID,
								Gradient:  "change the model",
							},
						},
					},
				},
			},
		},
	}, targetSurfaceSet{testSurfaceID: {}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside target surfaces")
	assert.Empty(t, aggregatorInstance.requests)
}

func TestOptimizeRejectsOutOfScopeSurface(t *testing.T) {
	modelSurfaceID := astructure.SurfaceID("node_1", astructure.SurfaceTypeModel)
	structure, err := newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		EntryNodeID: "node_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1", Kind: astructure.NodeKindLLM, Name: "writer"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: modelSurfaceID,
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeModel,
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{Name: "gpt-test"},
				},
			},
		},
	})
	assert.NoError(t, err)
	optimizerInstance := &fakeOptimizer{}
	engineInstance := &engine{optimizer: optimizerInstance}
	_, err = engineInstance.optimize(context.Background(), structure, nil, &AggregationResult{
		Surfaces: []promptiter.AggregatedSurfaceGradient{
			{
				SurfaceID: modelSurfaceID,
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeModel,
			},
		},
	}, targetSurfaceSet{testSurfaceID: {}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside target surfaces")
	assert.Empty(t, optimizerInstance.requests)
}

func TestAdaptEvaluationCaseResultUsesFirstRunWhenMultipleRunsExist(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	evalCase := &evaluation.EvaluationCaseResult{
		EvalCaseID: "case_1",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				RunID: 1,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "quality",
						Score:      0.9,
						EvalStatus: status.EvalStatusPassed,
					},
				},
			},
			{
				RunID: 2,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{
						MetricName: "quality",
						Score:      0.1,
						EvalStatus: status.EvalStatusPassed,
					},
				},
			},
		},
		RunDetails: []*evaluation.EvaluationCaseRunDetails{
			{
				RunID: 1,
				Inference: &evaluation.EvaluationInferenceDetails{
					SessionID: "session_first",
					Inferences: []*evalset.Invocation{
						{
							InvocationID: "invocation_first",
						},
					},
					ExecutionTraces: []*atrace.Trace{
						{
							RootInvocationID: "invocation_first",
							SessionID:        "session_first",
							Status:           atrace.TraceStatusCompleted,
							Steps: []atrace.Step{
								{
									StepID:            "step_1",
									InvocationID:      "invocation_first",
									NodeID:            "node_1",
									AppliedSurfaceIDs: []string{testSurfaceID},
									Output:            &atrace.Snapshot{Text: "first output"},
								},
							},
						},
					},
				},
			},
			{
				RunID: 2,
				Inference: &evaluation.EvaluationInferenceDetails{
					SessionID: "session_second",
					Inferences: []*evalset.Invocation{
						{
							InvocationID: "invocation_second",
						},
					},
					ExecutionTraces: []*atrace.Trace{
						{
							RootInvocationID: "invocation_second",
							SessionID:        "session_second",
							Status:           atrace.TraceStatusCompleted,
							Steps: []atrace.Step{
								{
									StepID:            "step_1",
									InvocationID:      "invocation_second",
									NodeID:            "node_1",
									AppliedSurfaceIDs: []string{testSurfaceID},
									Output:            &atrace.Snapshot{Text: "second output"},
								},
							},
						},
					},
				},
			},
		},
	}
	result, err := adaptEvaluationCaseResult(structure, "validation", evalCase)
	assert.NoError(t, err)
	assert.Equal(t, "validation", result.EvalSetID)
	assert.Equal(t, "case_1", result.EvalCaseID)
	assert.Equal(t, "session_first", result.SessionID)
	assert.Len(t, result.Metrics, 1)
	assert.Equal(t, 0.9, result.Metrics[0].Score)
}

func TestRunRejectsEmptyValidationEvalSetIDs(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	_, err = engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs: []string{"train"},
		MaxRounds:       1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation evaluation set ids are empty")
}

func TestRunRejectsInvalidInitialProfile(t *testing.T) {
	backward := &fakeBackwarder{}
	aggregatorInstance := &fakeAggregator{}
	optimizerInstance := &fakeOptimizer{}
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		backward,
		aggregatorInstance,
		optimizerInstance,
	)
	assert.NoError(t, err)
	_, err = engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		InitialProfile: &promptiter.Profile{
			Overrides: []promptiter.SurfaceOverride{
				{
					SurfaceID: "unknown",
					Value: astructure.SurfaceValue{
						Text: stringPtr("bad"),
					},
				},
			},
		},
		MaxRounds: 1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestRunRejectsEmptyTargetSurfaceIDs(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	_, err = engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		TargetSurfaceIDs:     []string{},
		MaxRounds:            1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "target surface ids must not be empty")
}

func TestRunRejectsUnknownTargetSurfaceID(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	_, err = engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		TargetSurfaceIDs:     []string{"unknown#instruction"},
		MaxRounds:            1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestNewRejectsMissingAgentEvaluator(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		nil,
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent evaluator is nil")
	assert.Nil(t, engineInstance)
}

func TestNewRejectsMissingTargetAgent(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		nil,
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "target agent is nil")
	assert.Nil(t, engineInstance)
}

func TestRunRejectsStructureExportFailure(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		&fakeStructureAgent{exportErr: errors.New("boom")},
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	_, err = engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "describe structure")
	assert.Contains(t, err.Error(), "boom")
}

func TestLossUsesTraceTerminalStep(t *testing.T) {
	losses, err := (&engine{}).loss(&EvaluationResult{
		EvalSets: []EvalSetResult{
			{
				EvalSetID: "train",
				Cases: []CaseResult{
					{
						EvalSetID:  "train",
						EvalCaseID: "case_1",
						Trace: &atrace.Trace{
							Status: atrace.TraceStatusCompleted,
							Steps: []atrace.Step{
								{
									StepID: "step_1",
									NodeID: "node_1",
								},
								{
									StepID:             "step_2",
									NodeID:             "node_1",
									PredecessorStepIDs: []string{"step_1"},
								},
								{
									StepID:             "step_3",
									NodeID:             "node_1",
									PredecessorStepIDs: []string{"step_2"},
								},
							},
						},
						Metrics: []MetricResult{
							{
								MetricName: "quality",
								Status:     status.EvalStatusFailed,
								Reason:     "needs improvement",
							},
						},
					},
				},
			},
		},
	})
	assert.NoError(t, err)
	if assert.Len(t, losses, 1) && assert.Len(t, losses[0].TerminalLosses, 1) {
		assert.Equal(t, "step_3", losses[0].TerminalLosses[0].StepID)
		assert.Empty(t, losses[0].TerminalLosses[0].Severity)
	}
}

func TestTraceTerminalStepIDsReturnsTraceTerminalSet(t *testing.T) {
	stepIDs, err := traceTerminalStepIDs(&atrace.Trace{
		Status: atrace.TraceStatusCompleted,
		Steps: []atrace.Step{
			{
				StepID:       "step_1",
				InvocationID: "root-inv",
				NodeID:       "node_1",
			},
			{
				StepID:             "step_2",
				InvocationID:       "child-inv",
				ParentInvocationID: "root-inv",
				NodeID:             "node_1",
				PredecessorStepIDs: []string{"step_1"},
			},
			{
				StepID:             "step_3",
				InvocationID:       "child-inv",
				ParentInvocationID: "root-inv",
				NodeID:             "node_1",
				PredecessorStepIDs: []string{"step_1"},
			},
			{
				StepID:       "step_4",
				InvocationID: "root-inv",
				NodeID:       "node_1",
			},
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"step_2", "step_3", "step_4"}, stepIDs)
}

func TestLossExpandsMetricAcrossTraceTerminalSteps(t *testing.T) {
	losses, err := (&engine{}).loss(&EvaluationResult{
		EvalSets: []EvalSetResult{
			{
				EvalSetID: "train",
				Cases: []CaseResult{
					{
						EvalSetID:  "train",
						EvalCaseID: "case_1",
						Trace: &atrace.Trace{
							Status: atrace.TraceStatusCompleted,
							Steps: []atrace.Step{
								{
									StepID:       "step_1",
									InvocationID: "invocation_1",
									NodeID:       "node_1",
								},
								{
									StepID:             "step_2",
									InvocationID:       "invocation_1",
									NodeID:             "node_1",
									PredecessorStepIDs: []string{"step_1"},
								},
								{
									StepID:             "step_3",
									InvocationID:       "invocation_1",
									NodeID:             "node_1",
									PredecessorStepIDs: []string{"step_1"},
								},
							},
						},
						Metrics: []MetricResult{
							{
								MetricName: "quality",
								Status:     status.EvalStatusFailed,
								Reason:     "needs improvement",
							},
						},
					},
				},
			},
		},
	})
	assert.NoError(t, err)
	if assert.Len(t, losses, 1) && assert.Len(t, losses[0].TerminalLosses, 2) {
		assert.Equal(t, "step_2", losses[0].TerminalLosses[0].StepID)
		assert.Equal(t, "step_3", losses[0].TerminalLosses[1].StepID)
	}
}

func scriptedOutcome(evalSetID string, profileValue string) scriptedEvalOutcome {
	switch evalSetID {
	case "train":
		outcome := scriptedEvalOutcome{
			score:             0.4,
			metricStatus:      status.EvalStatusFailed,
			reason:            "needs improvement",
			appliedSurfaceIDs: []string{testSurfaceID},
		}
		if profileValue == "accepted prompt" {
			outcome.score = 0.7
			outcome.reason = "still overfitting"
		}
		return outcome
	case "validation":
		score := 0.5
		if profileValue == "accepted prompt" {
			score = 0.8
		}
		if profileValue == "rejected prompt" {
			score = 0.79
		}
		return scriptedEvalOutcome{
			score:             score,
			metricStatus:      status.EvalStatusPassed,
			appliedSurfaceIDs: []string{testSurfaceID},
		}
	default:
		return scriptedEvalOutcome{
			score:             0,
			metricStatus:      status.EvalStatusPassed,
			appliedSurfaceIDs: []string{testSurfaceID},
		}
	}
}

func testStructureSnapshot(t *testing.T) *astructure.Snapshot {
	t.Helper()
	snapshot, err := astructure.Export(context.Background(), testTargetAgent())
	assert.NoError(t, err)
	return snapshot
}

func testTargetAgent() agent.Agent {
	return &fakeStructureAgent{
		snapshot: &astructure.Snapshot{
			EntryNodeID: "node_1",
			Nodes: []astructure.Node{
				{
					NodeID: "node_1",
					Kind:   astructure.NodeKindLLM,
					Name:   "writer",
				},
			},
			Surfaces: []astructure.Surface{
				{
					NodeID: "node_1",
					Type:   astructure.SurfaceTypeInstruction,
					Value: astructure.SurfaceValue{
						Text: stringPtr("base prompt"),
					},
				},
			},
		},
	}
}

func profileText(profile *promptiter.Profile) string {
	if profile == nil || len(profile.Overrides) == 0 || profile.Overrides[0].Value.Text == nil {
		return "base prompt"
	}
	return *profile.Overrides[0].Value.Text
}

func stringPtr(value string) *string {
	return &value
}
