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
	"reflect"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	"trpc.group/trpc-go/trpc-agent-go/runner"
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

type fakeAgentEvaluator struct {
	evaluate func(ctx context.Context, evalSetID string, opt ...evaluation.Option) (*evaluation.EvaluationResult, error)
}

func (f *fakeAgentEvaluator) Evaluate(
	ctx context.Context,
	evalSetID string,
	opt ...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	if f.evaluate != nil {
		return f.evaluate(ctx, evalSetID, opt...)
	}
	return &evaluation.EvaluationResult{}, nil
}

func (f *fakeAgentEvaluator) Close() error {
	return nil
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
	var observedEvents []Event
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
			observedEvents = append(observedEvents, *event)
		}
		return nil
	}))
	assert.NoError(t, err)
	assert.NotNil(t, result)
	require.Len(t, observedEvents, 11)
	assert.Equal(t, EventKindStructureSnapshot, observedEvents[0].Kind)
	assert.Zero(t, observedEvents[0].Round)
	assert.IsType(t, &astructure.Snapshot{}, observedEvents[0].Payload)
	assert.Equal(t, EventKindBaselineValidation, observedEvents[1].Kind)
	assert.Zero(t, observedEvents[1].Round)
	assert.IsType(t, &EvaluationResult{}, observedEvents[1].Payload)
	assert.Equal(t, EventKindRoundStarted, observedEvents[2].Kind)
	assert.Equal(t, 1, observedEvents[2].Round)
	assert.Nil(t, observedEvents[2].Payload)
	assert.Equal(t, EventKindRoundTrainEvaluation, observedEvents[3].Kind)
	assert.Equal(t, 1, observedEvents[3].Round)
	assert.IsType(t, &EvaluationResult{}, observedEvents[3].Payload)
	assert.Equal(t, EventKindRoundLosses, observedEvents[4].Kind)
	assert.Equal(t, 1, observedEvents[4].Round)
	assert.IsType(t, []promptiter.CaseLoss{}, observedEvents[4].Payload)
	assert.Equal(t, EventKindRoundBackward, observedEvents[5].Kind)
	assert.Equal(t, 1, observedEvents[5].Round)
	assert.IsType(t, &BackwardResult{}, observedEvents[5].Payload)
	assert.Equal(t, EventKindRoundAggregation, observedEvents[6].Kind)
	assert.Equal(t, 1, observedEvents[6].Round)
	assert.IsType(t, &AggregationResult{}, observedEvents[6].Payload)
	assert.Equal(t, EventKindRoundPatchSet, observedEvents[7].Kind)
	assert.Equal(t, 1, observedEvents[7].Round)
	assert.IsType(t, &promptiter.PatchSet{}, observedEvents[7].Payload)
	assert.Equal(t, EventKindRoundOutputProfile, observedEvents[8].Kind)
	assert.Equal(t, 1, observedEvents[8].Round)
	assert.IsType(t, &promptiter.Profile{}, observedEvents[8].Payload)
	assert.Equal(t, EventKindRoundValidation, observedEvents[9].Kind)
	assert.Equal(t, 1, observedEvents[9].Round)
	assert.IsType(t, &EvaluationResult{}, observedEvents[9].Payload)
	assert.Equal(t, EventKindRoundCompleted, observedEvents[10].Kind)
	assert.Equal(t, 1, observedEvents[10].Round)
	assert.IsType(t, &RoundCompleted{}, observedEvents[10].Payload)
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

func TestCompileProfileRunOptionsRejectsEmptyModelProvider(t *testing.T) {
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
	assert.ErrorContains(t, err, "model provider is empty")
	assert.Nil(t, runOptions)
}

func TestEvaluateValidatesRequests(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	require.NoError(t, err)
	engineInstance := &engine{agentEvaluator: &fakeAgentEvaluator{}}
	result, runErr := engineInstance.evaluate(context.Background(), structure, nil)
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "evaluation request is nil")
	result, runErr = engineInstance.evaluate(context.Background(), nil, &EvaluationRequest{EvalSetIDs: []string{"validation"}})
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "structure state is nil")
	result, runErr = engineInstance.evaluate(context.Background(), structure, &EvaluationRequest{})
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "evaluation set ids are empty")
	engineInstance.agentEvaluator = nil
	result, runErr = engineInstance.evaluate(context.Background(), structure, &EvaluationRequest{EvalSetIDs: []string{"validation"}})
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "agent evaluator is nil")
	engineInstance.agentEvaluator = &fakeAgentEvaluator{}
	result, runErr = engineInstance.evaluate(context.Background(), structure, &EvaluationRequest{EvalSetIDs: []string{""}})
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "evaluation set id is empty")
}

func TestBuildEvaluationCallOptionsUsesConfiguredRunnersAndFlags(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	require.NoError(t, err)
	teacher := &stubRunner{}
	judge := &stubRunner{}
	options, buildErr := buildEvaluationCallOptions(structure, &EvaluationRequest{
		EvalSetIDs: []string{"validation"},
		Teacher:    teacher,
		Judge:      judge,
		Options: EvaluationOptions{
			EvalCaseParallelism:               3,
			EvalCaseParallelInferenceEnabled:  true,
			EvalCaseParallelEvaluationEnabled: true,
		},
	})
	require.NoError(t, buildErr)
	agentEvaluator, newErr := evaluation.New("promptiter-test", &stubRunner{}, options...)
	require.NoError(t, newErr)
	t.Cleanup(func() {
		require.NoError(t, agentEvaluator.Close())
	})
	assert.Same(t, teacher, readPrivateField[runner.Runner](t, agentEvaluator, "expectedRunner"))
	assert.Same(t, judge, readPrivateField[runner.Runner](t, agentEvaluator, "judgeRunner"))
	assert.Equal(t, 1, readPrivateField[int](t, agentEvaluator, "numRuns"))
	parallelism := readPrivateField[*int](t, agentEvaluator, "evalCaseParallelism")
	require.NotNil(t, parallelism)
	assert.Equal(t, 3, *parallelism)
	parallelInferenceEnabled := readPrivateField[*bool](t, agentEvaluator, "evalCaseParallelInferenceEnabled")
	require.NotNil(t, parallelInferenceEnabled)
	assert.True(t, *parallelInferenceEnabled)
	parallelEvaluationEnabled := readPrivateField[*bool](t, agentEvaluator, "evalCaseParallelEvaluationEnabled")
	require.NotNil(t, parallelEvaluationEnabled)
	assert.True(t, *parallelEvaluationEnabled)
}

func TestBuildEvaluationCallOptionsRejectsInvalidRequest(t *testing.T) {
	options, err := buildEvaluationCallOptions(nil, nil)
	assert.Nil(t, options)
	assert.EqualError(t, err, "evaluation request is nil")
	structure, buildErr := newStructureState(testStructureSnapshot(t))
	require.NoError(t, buildErr)
	options, err = buildEvaluationCallOptions(structure, &EvaluationRequest{
		Profile: &promptiter.Profile{
			StructureID: "other",
		},
	})
	assert.Nil(t, options)
	assert.ErrorContains(t, err, "profile structure id")
}

func TestCompileProfileRunOptionsValidationErrors(t *testing.T) {
	runOptions, err := compileProfileRunOptions(nil, nil)
	assert.Nil(t, runOptions)
	assert.EqualError(t, err, "structure state is nil")
	structure, buildErr := newStructureState(testStructureSnapshot(t))
	require.NoError(t, buildErr)
	runOptions, err = compileProfileRunOptions(structure, &promptiter.Profile{StructureID: "other"})
	assert.Nil(t, runOptions)
	assert.ErrorContains(t, err, "profile structure id")
	runOptions, err = compileProfileRunOptions(structure, &promptiter.Profile{
		StructureID: structure.snapshot.StructureID,
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: "missing#instruction",
				Value: astructure.SurfaceValue{
					Text: stringPtr("patched"),
				},
			},
		},
	})
	assert.Nil(t, runOptions)
	assert.ErrorContains(t, err, "unknown surface id")
}

func TestApplySurfaceOverrideToPatchValidationErrors(t *testing.T) {
	var patch agent.SurfacePatch
	err := applySurfaceOverrideToPatch(nil, astructure.Surface{}, astructure.SurfaceValue{})
	assert.EqualError(t, err, "surface patch is nil")
	err = applySurfaceOverrideToPatch(&patch, astructure.Surface{
		SurfaceID: "node_1#instruction",
		Type:      astructure.SurfaceTypeInstruction,
	}, astructure.SurfaceValue{})
	assert.ErrorContains(t, err, "instruction value is nil")
	err = applySurfaceOverrideToPatch(&patch, astructure.Surface{
		SurfaceID: "node_1#global_instruction",
		Type:      astructure.SurfaceTypeGlobalInstruction,
	}, astructure.SurfaceValue{})
	assert.ErrorContains(t, err, "global instruction value is nil")
	err = applySurfaceOverrideToPatch(&patch, astructure.Surface{
		SurfaceID: "node_1#few_shot",
		Type:      astructure.SurfaceTypeFewShot,
	}, astructure.SurfaceValue{
		FewShot: []astructure.FewShotExample{
			{
				Messages: []astructure.FewShotMessage{
					{
						Role:    "invalid",
						Content: "question",
					},
				},
			},
		},
	})
	assert.ErrorContains(t, err, "few-shot value is invalid")
	err = applySurfaceOverrideToPatch(&patch, astructure.Surface{
		SurfaceID: "node_1#tool",
		Type:      astructure.SurfaceTypeTool,
	}, astructure.SurfaceValue{})
	assert.ErrorContains(t, err, "is not supported by generic evaluation")
}

func TestBuildModelInstanceUsesVariant(t *testing.T) {
	const providerName = "promptiter_variant_provider"
	var capturedOptions provider.Options
	provider.Register(providerName, func(opts *provider.Options) (model.Model, error) {
		capturedOptions = *opts
		return &providerBackedTestModel{name: opts.ModelName}, nil
	})
	modelInstance, err := buildModelInstance(&astructure.ModelRef{
		Provider: providerName,
		Name:     "test-model",
		Variant:  "mini",
	})
	require.NoError(t, err)
	require.NotNil(t, modelInstance)
	assert.Equal(t, "mini", capturedOptions.Variant)
}

func TestConvertFewShotExamplesConvertsMessages(t *testing.T) {
	examples, err := convertFewShotExamples([]astructure.FewShotExample{
		{
			Messages: []astructure.FewShotMessage{
				{
					Role:    string(model.RoleSystem),
					Content: "follow the format",
				},
				{
					Role:    string(model.RoleUser),
					Content: "question",
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, examples, 1)
	require.Len(t, examples[0], 2)
	assert.Equal(t, model.RoleSystem, examples[0][0].Role)
	assert.Equal(t, "follow the format", examples[0][0].Content)
	assert.Equal(t, model.RoleUser, examples[0][1].Role)
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

func TestAggregateValidatesDependenciesAndResponses(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	require.NoError(t, err)
	t.Run("nil aggregator", func(t *testing.T) {
		engineInstance := &engine{}
		result, runErr := engineInstance.aggregate(context.Background(), structure, nil, targetSurfaceSet{})
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "aggregator is nil")
	})
	t.Run("nil structure", func(t *testing.T) {
		engineInstance := &engine{aggregator: &fakeAggregator{}}
		result, runErr := engineInstance.aggregate(context.Background(), nil, nil, targetSurfaceSet{})
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "structure state is nil")
	})
	t.Run("unknown surface", func(t *testing.T) {
		engineInstance := &engine{aggregator: &fakeAggregator{}}
		result, runErr := engineInstance.aggregate(context.Background(), structure, &BackwardResult{
			Cases: []CaseBackwardResult{
				{
					StepGradients: []promptiter.StepGradient{
						{
							Gradients: []promptiter.SurfaceGradient{
								{
									SurfaceID: "missing#instruction",
									Gradient:  "fix this",
								},
							},
						},
					},
				},
			},
		}, targetSurfaceSet{"missing#instruction": {}})
		assert.Nil(t, result)
		assert.ErrorContains(t, runErr, "aggregated surface id")
	})
	t.Run("aggregator error", func(t *testing.T) {
		engineInstance := &engine{
			aggregator: &fakeAggregator{
				fn: func(ctx context.Context, request *aggregator.Request) (*aggregator.Result, error) {
					_ = ctx
					_ = request
					return nil, errors.New("aggregate failed")
				},
			},
		}
		result, runErr := engineInstance.aggregate(context.Background(), structure, &BackwardResult{
			Cases: []CaseBackwardResult{
				{
					StepGradients: []promptiter.StepGradient{
						{
							Gradients: []promptiter.SurfaceGradient{
								{
									SurfaceID: testSurfaceID,
									Gradient:  "fix this",
								},
							},
						},
					},
				},
			},
		}, targetSurfaceSet{testSurfaceID: {}})
		assert.Nil(t, result)
		assert.ErrorContains(t, runErr, "aggregate surface")
	})
	t.Run("empty result", func(t *testing.T) {
		engineInstance := &engine{
			aggregator: &fakeAggregator{
				fn: func(ctx context.Context, request *aggregator.Request) (*aggregator.Result, error) {
					_ = ctx
					_ = request
					return &aggregator.Result{}, nil
				},
			},
		}
		result, runErr := engineInstance.aggregate(context.Background(), structure, &BackwardResult{
			Cases: []CaseBackwardResult{
				{
					StepGradients: []promptiter.StepGradient{
						{
							Gradients: []promptiter.SurfaceGradient{
								{
									SurfaceID: testSurfaceID,
									Gradient:  "fix this",
								},
							},
						},
					},
				},
			},
		}, targetSurfaceSet{testSurfaceID: {}})
		assert.Nil(t, result)
		assert.ErrorContains(t, runErr, "returned empty result")
	})
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

func TestOptimizeValidatesDependenciesAndResponses(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	require.NoError(t, err)
	t.Run("nil optimizer", func(t *testing.T) {
		engineInstance := &engine{}
		patchSet, runErr := engineInstance.optimize(context.Background(), structure, nil, nil, targetSurfaceSet{})
		assert.Nil(t, patchSet)
		assert.EqualError(t, runErr, "optimizer is nil")
	})
	t.Run("nil structure", func(t *testing.T) {
		engineInstance := &engine{optimizer: &fakeOptimizer{}}
		patchSet, runErr := engineInstance.optimize(context.Background(), nil, nil, nil, targetSurfaceSet{})
		assert.Nil(t, patchSet)
		assert.EqualError(t, runErr, "structure state is nil")
	})
	t.Run("resolve profile surface error", func(t *testing.T) {
		engineInstance := &engine{optimizer: &fakeOptimizer{}}
		patchSet, runErr := engineInstance.optimize(context.Background(), structure, nil, &AggregationResult{
			Surfaces: []promptiter.AggregatedSurfaceGradient{
				{
					SurfaceID: "missing#instruction",
					NodeID:    "missing",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "missing#instruction", Gradient: "fix"}},
				},
			},
		}, targetSurfaceSet{"missing#instruction": {}})
		assert.Nil(t, patchSet)
		assert.ErrorContains(t, runErr, "surface id")
	})
	t.Run("empty optimizer result", func(t *testing.T) {
		engineInstance := &engine{
			optimizer: &fakeOptimizer{
				fn: func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
					_ = ctx
					_ = request
					return &optimizer.Result{}, nil
				},
			},
		}
		patchSet, runErr := engineInstance.optimize(context.Background(), structure, nil, &AggregationResult{
			Surfaces: []promptiter.AggregatedSurfaceGradient{
				{
					SurfaceID: testSurfaceID,
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: testSurfaceID, Gradient: "fix"}},
				},
			},
		}, targetSurfaceSet{testSurfaceID: {}})
		assert.Nil(t, patchSet)
		assert.ErrorContains(t, runErr, "returned empty result")
	})
	t.Run("sort patches by surface id", func(t *testing.T) {
		secondSurfaceID := astructure.SurfaceID("node_1", astructure.SurfaceTypeGlobalInstruction)
		structureWithSecondSurface, buildErr := newStructureState(&astructure.Snapshot{
			StructureID: "structure_1",
			EntryNodeID: "node_1",
			Nodes: []astructure.Node{
				{NodeID: "node_1", Kind: astructure.NodeKindLLM, Name: "writer"},
			},
			Surfaces: []astructure.Surface{
				{
					SurfaceID: secondSurfaceID,
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeGlobalInstruction,
					Value:     astructure.SurfaceValue{Text: stringPtr("global")},
				},
				{
					SurfaceID: testSurfaceID,
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Value:     astructure.SurfaceValue{Text: stringPtr("instruction")},
				},
			},
		})
		require.NoError(t, buildErr)
		engineInstance := &engine{
			optimizer: &fakeOptimizer{
				fn: func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
					_ = ctx
					return &optimizer.Result{
						Patch: &promptiter.SurfacePatch{
							SurfaceID: request.Surface.SurfaceID,
							Value:     request.Surface.Value,
							Reason:    "keep",
						},
					}, nil
				},
			},
		}
		patchSet, runErr := engineInstance.optimize(context.Background(), structureWithSecondSurface, nil, &AggregationResult{
			Surfaces: []promptiter.AggregatedSurfaceGradient{
				{
					SurfaceID: testSurfaceID,
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: testSurfaceID, Gradient: "fix instruction"}},
				},
				{
					SurfaceID: secondSurfaceID,
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeGlobalInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: secondSurfaceID, Gradient: "fix global"}},
				},
			},
		}, targetSurfaceSet{testSurfaceID: {}, secondSurfaceID: {}})
		require.NoError(t, runErr)
		require.NotNil(t, patchSet)
		require.Len(t, patchSet.Patches, 2)
		assert.Less(t, patchSet.Patches[0].SurfaceID, patchSet.Patches[1].SurfaceID)
	})
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

func TestRunRejectsNilRequest(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	result, runErr := engineInstance.Run(context.Background(), nil)
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "run request is nil")
}

func TestRunRejectsEmptyTrainEvalSetIDs(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	result, runErr := engineInstance.Run(context.Background(), &RunRequest{
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
	})
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "train evaluation set ids are empty")
}

func TestRunRejectsNonPositiveMaxRounds(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.NoError(t, err)
	result, runErr := engineInstance.Run(context.Background(), &RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            0,
	})
	assert.Nil(t, result)
	assert.EqualError(t, runErr, "max rounds must be greater than 0")
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

func TestCalculateEvaluationScore(t *testing.T) {
	score, err := calculateEvaluationScore(nil)
	assert.EqualError(t, err, "evaluation result is nil")
	assert.Zero(t, score)
	score, err = calculateEvaluationScore(&evaluation.EvaluationResult{
		EvalCases: []*evaluation.EvaluationCaseResult{
			nil,
			{
				MetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "ignored", EvalStatus: status.EvalStatusNotEvaluated, Score: 0.3},
				},
			},
		},
	})
	assert.EqualError(t, err, "evaluation result has no metric scores")
	assert.Zero(t, score)
	score, err = calculateEvaluationScore(&evaluation.EvaluationResult{
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				MetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "quality", EvalStatus: status.EvalStatusPassed, Score: 0.6},
					nil,
				},
			},
			{
				MetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "quality", EvalStatus: status.EvalStatusFailed, Score: 0.9},
				},
			},
		},
	})
	assert.NoError(t, err)
	assert.InDelta(t, 0.75, score, 0.0001)
}

func TestAdaptMetricResults(t *testing.T) {
	metrics, err := adaptMetricResults([]*evalresult.EvalMetricResult{
		nil,
		{
			MetricName: "quality",
			Score:      0.7,
			EvalStatus: status.EvalStatusPassed,
			Details: &evalresult.EvalMetricResultDetails{
				Reason: "  accepted  ",
			},
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, []MetricResult{
		{
			MetricName: "quality",
			Score:      0.7,
			Status:     status.EvalStatusPassed,
			Reason:     "accepted",
		},
	}, metrics)
	metrics, err = adaptMetricResults([]*evalresult.EvalMetricResult{
		{
			MetricName: "quality",
			Score:      0.1,
			EvalStatus: status.EvalStatusFailed,
		},
	})
	assert.Nil(t, metrics)
	assert.EqualError(t, err, `metric "quality" is missing loss reason`)
}

func TestValidateTraceAgainstStructure(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	assert.EqualError(t, validateTraceAgainstStructure(nil, &atrace.Trace{}), "structure state is nil")
	assert.EqualError(t, validateTraceAgainstStructure(structure, nil), "execution trace is nil")
	err = validateTraceAgainstStructure(structure, &atrace.Trace{
		Steps: []atrace.Step{{NodeID: "node_1"}},
	})
	assert.EqualError(t, err, "execution trace step id is empty")
	err = validateTraceAgainstStructure(structure, &atrace.Trace{
		Steps: []atrace.Step{
			{
				StepID:            "step_1",
				NodeID:            "node_1",
				AppliedSurfaceIDs: []string{"unknown#instruction"},
			},
		},
	})
	assert.EqualError(t, err, `execution trace step "step_1" references unknown surface id "unknown#instruction"`)
	assert.NoError(t, validateTraceAgainstStructure(structure, &atrace.Trace{
		Steps: []atrace.Step{
			{
				StepID:            "step_1",
				NodeID:            "node_1",
				AppliedSurfaceIDs: []string{testSurfaceID},
			},
		},
	}))
}

func TestExtractInferenceTraceDetails(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	trace, sessionID, err := extractInferenceTraceDetails(structure, "case_1", nil)
	assert.Nil(t, trace)
	assert.Empty(t, sessionID)
	assert.EqualError(t, err, "inference result is nil")
	trace, sessionID, err = extractInferenceTraceDetails(structure, "case_1", &evaluation.EvaluationInferenceDetails{
		ExecutionTraces: []*atrace.Trace{},
	})
	assert.Nil(t, trace)
	assert.Empty(t, sessionID)
	assert.EqualError(t, err, `inference result for eval case "case_1" must contain exactly one execution trace`)
	expectedTrace := &atrace.Trace{
		SessionID: "trace_session",
		Steps: []atrace.Step{
			{
				StepID:            "step_1",
				NodeID:            "node_1",
				AppliedSurfaceIDs: []string{testSurfaceID},
			},
		},
	}
	trace, sessionID, err = extractInferenceTraceDetails(structure, "case_1", &evaluation.EvaluationInferenceDetails{
		SessionID:       "fallback_session",
		ExecutionTraces: []*atrace.Trace{expectedTrace},
	})
	assert.NoError(t, err)
	assert.Equal(t, expectedTrace, trace)
	assert.Equal(t, "trace_session", sessionID)
	trace, sessionID, err = extractInferenceTraceDetails(structure, "case_1", &evaluation.EvaluationInferenceDetails{
		SessionID: "fallback_session",
		ExecutionTraces: []*atrace.Trace{
			{
				Steps: []atrace.Step{
					{
						StepID:            "step_1",
						NodeID:            "node_1",
						AppliedSurfaceIDs: []string{testSurfaceID},
					},
				},
			},
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, "fallback_session", sessionID)
}

func TestAdaptEvaluationCaseResultValidationErrors(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	result, err := adaptEvaluationCaseResult(structure, "validation", nil)
	assert.Nil(t, result)
	assert.EqualError(t, err, "evaluation case result is nil")
	result, err = adaptEvaluationCaseResult(structure, "validation", &evaluation.EvaluationCaseResult{})
	assert.Nil(t, result)
	assert.EqualError(t, err, "evaluation case id is empty")
	result, err = adaptEvaluationCaseResult(structure, "validation", &evaluation.EvaluationCaseResult{
		EvalCaseID: "case_1",
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `evaluation case "case_1" has no run results`)
	result, err = adaptEvaluationCaseResult(structure, "validation", &evaluation.EvaluationCaseResult{
		EvalCaseID:      "case_1",
		EvalCaseResults: []*evalresult.EvalCaseResult{nil},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `evaluation case "case_1" run result is nil`)
	result, err = adaptEvaluationCaseResult(structure, "validation", &evaluation.EvaluationCaseResult{
		EvalCaseID:      "case_1",
		EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 1}},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `evaluation case "case_1" has no run details`)
	result, err = adaptEvaluationCaseResult(structure, "validation", &evaluation.EvaluationCaseResult{
		EvalCaseID:      "case_1",
		EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 1}},
		RunDetails:      []*evaluation.EvaluationCaseRunDetails{nil},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `evaluation case "case_1" run detail is nil`)
	result, err = adaptEvaluationCaseResult(structure, "validation", &evaluation.EvaluationCaseResult{
		EvalCaseID:      "case_1",
		EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 1}},
		RunDetails: []*evaluation.EvaluationCaseRunDetails{
			{RunID: 2},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `evaluation case "case_1" run detail id 2 does not match run result id 1`)
}

func TestAdaptEvaluationSetResultValidationErrors(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	evalSet, err := adaptEvaluationSetResult(nil, "validation", &evaluation.EvaluationResult{})
	assert.Nil(t, evalSet)
	assert.EqualError(t, err, "structure state is nil")
	evalSet, err = adaptEvaluationSetResult(structure, "validation", nil)
	assert.Nil(t, evalSet)
	assert.EqualError(t, err, "evaluation result is nil")
	evalSet, err = adaptEvaluationSetResult(structure, "validation", &evaluation.EvaluationResult{})
	assert.Nil(t, evalSet)
	assert.EqualError(t, err, "evaluation result eval set id is empty")
	evalSet, err = adaptEvaluationSetResult(structure, "validation", &evaluation.EvaluationResult{
		EvalSetID: "train",
	})
	assert.Nil(t, evalSet)
	assert.EqualError(t, err, `evaluation result eval set id "train" does not match request "validation"`)
	evalSet, err = adaptEvaluationSetResult(structure, "validation", &evaluation.EvaluationResult{
		EvalSetID: "validation",
	})
	assert.Nil(t, evalSet)
	assert.EqualError(t, err, "evaluation result has no metric scores")
}

func TestBuildModelInstanceAndConvertFewShotExamplesValidation(t *testing.T) {
	modelInstance, err := buildModelInstance(nil)
	assert.Nil(t, modelInstance)
	assert.EqualError(t, err, "model ref is nil")
	modelInstance, err = buildModelInstance(&astructure.ModelRef{Name: "gpt"})
	assert.Nil(t, modelInstance)
	assert.EqualError(t, err, "model provider is empty")
	modelInstance, err = buildModelInstance(&astructure.ModelRef{Provider: "openai"})
	assert.Nil(t, modelInstance)
	assert.EqualError(t, err, "model name is empty")
	examples, err := convertFewShotExamples([]astructure.FewShotExample{
		{
			Messages: []astructure.FewShotMessage{
				{Role: "unknown", Content: "bad"},
			},
		},
	})
	assert.Nil(t, examples)
	assert.EqualError(t, err, `example 0 message 0 role "unknown" is invalid`)
}

func TestNewStructureStateValidationErrors(t *testing.T) {
	state, err := newStructureState(nil)
	assert.Nil(t, state)
	assert.EqualError(t, err, "structure snapshot is nil")
	state, err = newStructureState(&astructure.Snapshot{})
	assert.Nil(t, state)
	assert.EqualError(t, err, "structure id is empty")
	state, err = newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{},
		},
	})
	assert.Nil(t, state)
	assert.EqualError(t, err, "node id is empty")
	state, err = newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1"},
			{NodeID: "node_1"},
		},
	})
	assert.Nil(t, state)
	assert.EqualError(t, err, `duplicate node id "node_1"`)
	state, err = newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "candidate#instruction",
				NodeID:    "unknown",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("prompt"),
				},
			},
		},
	})
	assert.Nil(t, state)
	assert.EqualError(t, err, `surface "candidate#instruction" references unknown node id "unknown"`)
	state, err = newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "candidate#instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("prompt"),
				},
			},
			{
				SurfaceID: "candidate#global_instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("other"),
				},
			},
		},
	})
	assert.Nil(t, state)
	assert.EqualError(t, err, `duplicate surface type "instruction" for node id "node_1"`)
	state, err = newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "candidate#instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("prompt"),
				},
			},
			{
				SurfaceID: "candidate#instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeModel,
				Value: astructure.SurfaceValue{
					Model: &astructure.ModelRef{Provider: "openai", Name: "gpt"},
				},
			},
		},
	})
	assert.Nil(t, state)
	assert.EqualError(t, err, `build surface index: duplicate surface id "candidate#instruction"`)
	state, err = newStructureState(&astructure.Snapshot{
		StructureID: "structure_1",
		Nodes: []astructure.Node{
			{NodeID: "node_1"},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "candidate#unsupported",
				NodeID:    "node_1",
				Type:      astructure.SurfaceType("unsupported"),
			},
			{
				SurfaceID: "candidate#instruction",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: stringPtr("prompt"),
				},
			},
		},
	})
	assert.NoError(t, err)
	require.NotNil(t, state)
	assert.Contains(t, state.surfaceIndex, "candidate#instruction")
	assert.NotContains(t, state.surfaceIndex, "candidate#unsupported")
}

func TestNormalizeProfileApplyPatchSetAndScopeHelpers(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	structureID := structure.snapshot.StructureID
	profile, err := normalizeProfile(nil, nil)
	assert.Nil(t, profile)
	assert.EqualError(t, err, "structure state is nil")
	profile, err = normalizeProfile(structure, &promptiter.Profile{
		StructureID: "other",
	})
	assert.Nil(t, profile)
	assert.EqualError(t, err, `profile structure id "other" does not match structure id "`+structureID+`"`)
	profile, err = normalizeProfile(structure, &promptiter.Profile{
		Overrides: []promptiter.SurfaceOverride{
			{SurfaceID: "unknown"},
		},
	})
	assert.Nil(t, profile)
	assert.EqualError(t, err, `profile override surface id "unknown" is unknown`)
	profile, err = normalizeProfile(structure, &promptiter.Profile{
		StructureID: structureID,
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: testSurfaceID,
				Value: astructure.SurfaceValue{
					Text: stringPtr("base prompt"),
				},
			},
			{
				SurfaceID: testSurfaceID,
				Value: astructure.SurfaceValue{
					Text: stringPtr("mutated prompt"),
				},
			},
		},
	})
	assert.Nil(t, profile)
	assert.EqualError(t, err, `duplicate profile override surface id "node_1#instruction"`)
	profile, err = normalizeProfile(structure, &promptiter.Profile{
		StructureID: structureID,
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: testSurfaceID,
				Value: astructure.SurfaceValue{
					Text: stringPtr("mutated prompt"),
				},
			},
		},
	})
	assert.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, structureID, profile.StructureID)
	require.Len(t, profile.Overrides, 1)
	assert.Equal(t, "mutated prompt", *profile.Overrides[0].Value.Text)
	applied, err := applyPatchSet(nil, nil, nil)
	assert.Nil(t, applied)
	assert.EqualError(t, err, "structure state is nil")
	applied, err = applyPatchSet(structure, profile, &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{
			{SurfaceID: ""},
		},
	})
	assert.Nil(t, applied)
	assert.EqualError(t, err, "patch surface id is empty")
	applied, err = applyPatchSet(structure, profile, &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{
			{
				SurfaceID: "unknown",
				Value: astructure.SurfaceValue{
					Text: stringPtr("x"),
				},
			},
		},
	})
	assert.Nil(t, applied)
	assert.EqualError(t, err, `patch surface id "unknown" is unknown`)
	applied, err = applyPatchSet(structure, profile, &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{
			{
				SurfaceID: testSurfaceID,
				Value: astructure.SurfaceValue{
					Text: stringPtr("base prompt"),
				},
			},
		},
	})
	assert.NoError(t, err)
	require.NotNil(t, applied)
	assert.Empty(t, applied.Overrides)
	applied, err = applyPatchSet(structure, profile, &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{
			{
				SurfaceID: testSurfaceID,
				Value: astructure.SurfaceValue{
					Text: stringPtr("override one"),
				},
			},
			{
				SurfaceID: testSurfaceID,
				Value: astructure.SurfaceValue{
					Text: stringPtr("override two"),
				},
			},
		},
	})
	assert.Nil(t, applied)
	assert.EqualError(t, err, `duplicate patch surface id "node_1#instruction"`)
	applied, err = applyPatchSet(structure, profile, &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{
			{
				SurfaceID: testSurfaceID,
				Value:     astructure.SurfaceValue{},
			},
		},
	})
	assert.Nil(t, applied)
	assert.EqualError(t, err, `sanitize patch "node_1#instruction": text is nil`)
	surface, err := resolveProfileSurface(nil, nil, testSurfaceID)
	assert.Equal(t, astructure.Surface{}, surface)
	assert.EqualError(t, err, "structure state is nil")
	surface, err = resolveProfileSurface(structure, nil, "unknown")
	assert.Equal(t, astructure.Surface{}, surface)
	assert.EqualError(t, err, `surface id "unknown" is unknown`)
	surface, err = resolveProfileSurface(structure, map[string]promptiter.SurfaceOverride{
		testSurfaceID: {
			SurfaceID: testSurfaceID,
			Value: astructure.SurfaceValue{
				Text: stringPtr("override"),
			},
		},
	}, testSurfaceID)
	assert.NoError(t, err)
	assert.Equal(t, "override", *surface.Value.Text)
	targets, err := compileTargetSurfaceIDs(nil, []string{testSurfaceID})
	assert.Nil(t, targets)
	assert.EqualError(t, err, "structure state is nil")
	targets, err = compileTargetSurfaceIDs(structure, []string{})
	assert.Nil(t, targets)
	assert.EqualError(t, err, "target surface ids must not be empty")
	targets, err = compileTargetSurfaceIDs(structure, []string{""})
	assert.Nil(t, targets)
	assert.EqualError(t, err, "target surface ids must not contain empty values")
	targets, err = compileTargetSurfaceIDs(structure, []string{"unknown"})
	assert.Nil(t, targets)
	assert.EqualError(t, err, `target surface id "unknown" is unknown`)
	targets, err = compileTargetSurfaceIDs(structure, []string{testSurfaceID})
	assert.NoError(t, err)
	assert.True(t, targets.contains(testSurfaceID))
	assert.False(t, targets.contains("unknown"))
	var nilTargets targetSurfaceSet
	assert.True(t, nilTargets.contains(testSurfaceID))
}

func TestBackwardCoversAdditionalBranches(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	engineInstance := &engine{}
	result, backwardErr := engineInstance.backward(context.Background(), structure, nil, nil, nil, nil)
	assert.Nil(t, result)
	assert.EqualError(t, backwardErr, "backwarder is nil")
	engineInstance.backwarder = &fakeBackwarder{}
	result, backwardErr = engineInstance.backward(context.Background(), nil, nil, nil, nil, nil)
	assert.Nil(t, result)
	assert.EqualError(t, backwardErr, "structure state is nil")
	result, backwardErr = engineInstance.backward(context.Background(), structure, nil, &EvaluationResult{}, []promptiter.CaseLoss{
		{EvalSetID: "train", EvalCaseID: "case_1"},
	}, nil)
	assert.Nil(t, result)
	assert.EqualError(t, backwardErr, `eval case "case_1" from eval set "train" is missing from training result`)
	caseResult, backwardErr := engineInstance.backwardCase(context.Background(), structure, nil, CaseResult{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
	}, promptiter.CaseLoss{EvalSetID: "train", EvalCaseID: "case_1"}, nil)
	assert.Nil(t, caseResult)
	assert.EqualError(t, backwardErr, `trace is nil for eval case "case_1" in eval set "train"`)
	caseResult, backwardErr = engineInstance.backwardCase(context.Background(), structure, nil, CaseResult{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
		Trace: &atrace.Trace{
			Steps: []atrace.Step{{StepID: "step_1", NodeID: "node_1"}},
		},
	}, promptiter.CaseLoss{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
		TerminalLosses: []promptiter.TerminalLoss{
			{StepID: "missing", Loss: "boom"},
		},
	}, nil)
	assert.Nil(t, caseResult)
	assert.EqualError(t, backwardErr, `terminal loss step id "missing" is not part of trace for eval case "case_1"`)
	caseResult, backwardErr = engineInstance.backwardCase(context.Background(), structure, nil, CaseResult{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
		Trace: &atrace.Trace{
			Steps: []atrace.Step{{
				StepID:            "step_1",
				NodeID:            "node_1",
				AppliedSurfaceIDs: []string{""},
			}},
		},
	}, promptiter.CaseLoss{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
		TerminalLosses: []promptiter.TerminalLoss{
			{StepID: "step_1", Loss: "boom"},
		},
	}, nil)
	assert.Nil(t, caseResult)
	assert.EqualError(t, backwardErr, `build backward request for eval case "case_1" step "step_1": step "step_1" applied surface id is empty`)
	engineInstance.backwarder = &fakeBackwarder{
		fn: func(ctx context.Context, request *backwarder.Request) (*backwarder.Result, error) {
			_ = ctx
			_ = request
			return nil, errors.New("boom")
		},
	}
	caseResult, backwardErr = engineInstance.backwardCase(context.Background(), structure, nil, CaseResult{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
		Trace: &atrace.Trace{
			Steps: []atrace.Step{{
				StepID: "step_1",
				NodeID: "node_1",
			}},
		},
	}, promptiter.CaseLoss{
		EvalSetID:  "train",
		EvalCaseID: "case_1",
		TerminalLosses: []promptiter.TerminalLoss{
			{StepID: "step_1", Loss: "boom"},
		},
	}, nil)
	assert.Nil(t, caseResult)
	assert.EqualError(t, backwardErr, `backward eval case "case_1" step "step_1": boom`)
	engineInstance.backwarder = &fakeBackwarder{
		fn: func(ctx context.Context, request *backwarder.Request) (*backwarder.Result, error) {
			_ = ctx
			_ = request
			return &backwarder.Result{
				Gradients: []promptiter.SurfaceGradient{{SurfaceID: testSurfaceID, Gradient: "g"}},
				Upstream: []backwarder.Propagation{{
					PredecessorStepID: "step_1",
					Gradients:         []backwarder.GradientPacket{{Gradient: "upstream"}},
				}},
			}, nil
		},
	}
	result, backwardErr = engineInstance.backward(context.Background(), structure, nil, &EvaluationResult{
		EvalSets: []EvalSetResult{{
			EvalSetID: "train",
			Cases: []CaseResult{{
				EvalSetID:  "set_b",
				EvalCaseID: "case_b",
				Trace: &atrace.Trace{
					Steps: []atrace.Step{{
						StepID:            "step_1",
						NodeID:            "node_1",
						AppliedSurfaceIDs: []string{testSurfaceID},
					}},
				},
			}, {
				EvalSetID:  "set_a",
				EvalCaseID: "case_a",
				Trace: &atrace.Trace{
					Steps: []atrace.Step{{
						StepID:            "step_1",
						NodeID:            "node_1",
						AppliedSurfaceIDs: []string{testSurfaceID},
					}},
				},
			}},
		}},
	}, []promptiter.CaseLoss{
		{EvalSetID: "set_b", EvalCaseID: "case_b", TerminalLosses: []promptiter.TerminalLoss{{StepID: "step_1", Loss: "b"}}},
		{EvalSetID: "set_a", EvalCaseID: "case_a", TerminalLosses: []promptiter.TerminalLoss{{StepID: "step_1", Loss: "a"}}},
	}, targetSurfaceSet{testSurfaceID: {}})
	assert.NoError(t, backwardErr)
	require.NotNil(t, result)
	require.Len(t, result.Cases, 2)
	assert.Equal(t, "set_a", result.Cases[0].EvalSetID)
	assert.Equal(t, "case_a", result.Cases[0].EvalCaseID)
	assert.Equal(t, "set_b", result.Cases[1].EvalSetID)
	assert.Equal(t, "case_b", result.Cases[1].EvalCaseID)
}

func TestLossStopAndEventHelpers(t *testing.T) {
	losses, err := (&engine{}).loss(&EvaluationResult{
		EvalSets: []EvalSetResult{{
			EvalSetID: "train",
			Cases: []CaseResult{
				{
					EvalSetID:  "train",
					EvalCaseID: "skip_incomplete",
					Trace: &atrace.Trace{
						Status: atrace.TraceStatusIncomplete,
						Steps:  []atrace.Step{{StepID: "step_1", NodeID: "node_1"}},
					},
					Metrics: []MetricResult{{
						MetricName: "quality",
						Status:     status.EvalStatusFailed,
						Reason:     "ignored",
					}},
				},
				{
					EvalSetID:  "train",
					EvalCaseID: "skip_passed",
					Trace: &atrace.Trace{
						Status: atrace.TraceStatusCompleted,
						Steps:  []atrace.Step{{StepID: "step_1", NodeID: "node_1"}},
					},
					Metrics: []MetricResult{{
						MetricName: "quality",
						Status:     status.EvalStatusPassed,
						Reason:     "ignored",
					}},
				},
			},
		}},
	})
	assert.NoError(t, err)
	assert.Empty(t, losses)
	losses, err = (&engine{}).loss(&EvaluationResult{
		EvalSets: []EvalSetResult{{
			EvalSetID: "train",
			Cases: []CaseResult{{
				EvalSetID:  "train",
				EvalCaseID: "case_1",
				Trace: &atrace.Trace{
					Status: atrace.TraceStatusCompleted,
					Steps: []atrace.Step{{
						StepID:             "step_1",
						NodeID:             "node_1",
						PredecessorStepIDs: []string{"step_1"},
					}},
				},
				Metrics: []MetricResult{{
					MetricName: "quality",
					Status:     status.EvalStatusFailed,
					Reason:     "boom",
				}},
			}},
		}},
	})
	assert.Nil(t, losses)
	assert.EqualError(t, err, `resolve terminal step for eval case "case_1": execution trace has no terminal step`)
	decision := (&engine{}).stop(1, 4, StopPolicy{
		TargetScore: func() *float64 {
			value := 0.8
			return &value
		}(),
	}, 0, 0.8)
	require.NotNil(t, decision)
	assert.True(t, decision.ShouldStop)
	assert.Equal(t, "target score reached", decision.Reason)
	decision = (&engine{}).stop(1, 4, StopPolicy{}, 0, 0.1)
	require.NotNil(t, decision)
	assert.False(t, decision.ShouldStop)
	assert.Equal(t, "continue optimization", decision.Reason)
	assert.NoError(t, appendRunEvent(context.Background(), nil, EventKindRoundStarted, 1, nil))
	err = appendRunEvent(context.Background(), func(ctx context.Context, event *Event) error {
		_ = ctx
		_ = event
		return errors.New("boom")
	}, EventKindRoundStarted, 1, nil)
	assert.EqualError(t, err, `append run event "round_started": boom`)
}

func TestOptimizeHelpersAndLossValidation(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	engineInstance := &engine{optimizer: &fakeOptimizer{}}
	patchSet, err := engineInstance.optimize(context.Background(), structure, nil, nil, nil)
	assert.NoError(t, err)
	require.NotNil(t, patchSet)
	assert.Empty(t, patchSet.Patches)
	engineInstance.optimizer = &fakeOptimizer{
		fn: func(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
			_ = ctx
			_ = request
			return nil, errors.New("boom")
		},
	}
	patchSet, err = engineInstance.optimize(context.Background(), structure, nil, &AggregationResult{
		Surfaces: []promptiter.AggregatedSurfaceGradient{
			{
				SurfaceID: testSurfaceID,
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
			},
		},
	}, targetSurfaceSet{testSurfaceID: {}})
	assert.Nil(t, patchSet)
	assert.EqualError(t, err, `optimize surface "node_1#instruction": boom`)
	originalGradient := promptiter.AggregatedSurfaceGradient{
		SurfaceID: testSurfaceID,
		Gradients: []promptiter.SurfaceGradient{
			{Gradient: "g1"},
		},
	}
	clonedGradient := cloneAggregatedGradient(originalGradient)
	clonedGradient.Gradients[0].Gradient = "mutated"
	assert.Equal(t, "mutated", clonedGradient.Gradients[0].Gradient)
	assert.Equal(t, "g1", originalGradient.Gradients[0].Gradient)
	losses, err := (&engine{}).loss(nil)
	assert.Nil(t, losses)
	assert.EqualError(t, err, "evaluation result is nil")
	losses, err = (&engine{}).loss(&EvaluationResult{
		EvalSets: []EvalSetResult{
			{
				EvalSetID: "train",
				Cases: []CaseResult{
					{
						EvalSetID:  "train",
						EvalCaseID: "case_1",
						Metrics: []MetricResult{
							{
								MetricName: "quality",
								Status:     status.EvalStatusFailed,
							},
						},
					},
				},
			},
		},
	})
	assert.Nil(t, losses)
	assert.EqualError(t, err, `metric "quality" for eval case "case_1" is missing loss reason`)
	terminalStepIDs, err := traceTerminalStepIDs(nil)
	assert.Nil(t, terminalStepIDs)
	assert.EqualError(t, err, "execution trace is nil")
	terminalStepIDs, err = traceTerminalStepIDs(&atrace.Trace{})
	assert.Nil(t, terminalStepIDs)
	assert.EqualError(t, err, "execution trace has no steps")
	terminalStepIDs, err = traceTerminalStepIDs(&atrace.Trace{
		Steps: []atrace.Step{
			{NodeID: "node_1"},
		},
	})
	assert.Nil(t, terminalStepIDs)
	assert.EqualError(t, err, "execution trace step id is empty")
	terminalStepIDs, err = traceTerminalStepIDs(&atrace.Trace{
		Steps: []atrace.Step{
			{StepID: "step_1"},
			{StepID: "step_1"},
		},
	})
	assert.Nil(t, terminalStepIDs)
	assert.EqualError(t, err, `duplicate execution trace step id "step_1"`)
	terminalStepIDs, err = traceTerminalStepIDs(&atrace.Trace{
		Steps: []atrace.Step{
			{StepID: "step_1", PredecessorStepIDs: []string{""}},
		},
	})
	assert.Nil(t, terminalStepIDs)
	assert.EqualError(t, err, `execution trace step "step_1" predecessor step id is empty`)
	terminalStepIDs, err = traceTerminalStepIDs(&atrace.Trace{
		Steps: []atrace.Step{
			{StepID: "step_1", PredecessorStepIDs: []string{"missing"}},
		},
	})
	assert.Nil(t, terminalStepIDs)
	assert.EqualError(t, err, `execution trace step "step_1" references unknown predecessor step id "missing"`)
}

func TestIndexCaseResultsAndNormalizeIncomingPackets(t *testing.T) {
	index := indexCaseResults(nil)
	assert.Empty(t, index)
	index = indexCaseResults(&EvaluationResult{
		EvalSets: []EvalSetResult{
			{
				EvalSetID: "train",
				Cases: []CaseResult{
					{EvalSetID: "train", EvalCaseID: "case_1"},
				},
			},
		},
	})
	assert.Contains(t, index, caseResultKey{evalSetID: "train", evalCaseID: "case_1"})
	packets := normalizeIncomingPackets([]backwarder.GradientPacket{
		{FromStepID: "step_2", Severity: promptiter.LossSeverityP2, Gradient: " detail "},
		{FromStepID: "step_1", Severity: promptiter.LossSeverityP0, Gradient: " grounding "},
		{FromStepID: "step_3", Severity: promptiter.LossSeverityP1, Gradient: "   "},
	})
	assert.Equal(t, []backwarder.GradientPacket{
		{FromStepID: "step_1", Severity: promptiter.LossSeverityP0, Gradient: " grounding "},
		{FromStepID: "step_2", Severity: promptiter.LossSeverityP2, Gradient: " detail "},
	}, packets)
}

func TestIndexTraceStepsValidationErrors(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	index, err := indexTraceSteps(nil, &atrace.Trace{})
	assert.Nil(t, index)
	assert.EqualError(t, err, "structure state is nil")
	index, err = indexTraceSteps(structure, nil)
	assert.Nil(t, index)
	assert.EqualError(t, err, "trace is nil")
	index, err = indexTraceSteps(structure, &atrace.Trace{
		Steps: []atrace.Step{{NodeID: "node_1"}},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, "trace step id is empty")
	index, err = indexTraceSteps(structure, &atrace.Trace{
		Steps: []atrace.Step{{StepID: "step_1"}},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, `trace step "step_1" node id is empty`)
	index, err = indexTraceSteps(structure, &atrace.Trace{
		Steps: []atrace.Step{{StepID: "step_1", NodeID: "unknown"}},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, `trace step "step_1" references unknown node id "unknown"`)
	index, err = indexTraceSteps(structure, &atrace.Trace{
		Steps: []atrace.Step{
			{StepID: "step_1", NodeID: "node_1"},
			{StepID: "step_1", NodeID: "node_1"},
		},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, `duplicate trace step id "step_1"`)
	index, err = indexTraceSteps(structure, &atrace.Trace{
		Steps: []atrace.Step{
			{StepID: "step_1", NodeID: "node_1", PredecessorStepIDs: []string{""}},
		},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, `trace step "step_1" predecessor step id is empty`)
	index, err = indexTraceSteps(structure, &atrace.Trace{
		Steps: []atrace.Step{
			{StepID: "step_1", NodeID: "node_1", PredecessorStepIDs: []string{"missing"}},
		},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, `trace step "step_1" references missing or out-of-order predecessor step id "missing"`)
}

func TestBuildBackwardRequestValidationErrors(t *testing.T) {
	structure, err := newStructureState(testStructureSnapshot(t))
	assert.NoError(t, err)
	traceIndex := map[string]indexedTraceStep{
		"step_1": {
			step: atrace.Step{
				StepID: "step_1",
				NodeID: "node_1",
			},
			order: 0,
		},
	}
	request, err := buildBackwardRequest(
		structure,
		nil,
		traceIndex,
		CaseResult{EvalSetID: "train", EvalCaseID: "case_1"},
		atrace.Step{
			StepID: "step_2",
			NodeID: "unknown",
		},
		nil,
		nil,
	)
	assert.Nil(t, request)
	assert.EqualError(t, err, `step "step_2" references unknown node id "unknown"`)
	request, err = buildBackwardRequest(
		structure,
		nil,
		traceIndex,
		CaseResult{EvalSetID: "train", EvalCaseID: "case_1"},
		atrace.Step{
			StepID:            "step_2",
			NodeID:            "node_1",
			AppliedSurfaceIDs: []string{""},
		},
		nil,
		nil,
	)
	assert.Nil(t, request)
	assert.EqualError(t, err, `step "step_2" applied surface id is empty`)
	request, err = buildBackwardRequest(
		structure,
		nil,
		traceIndex,
		CaseResult{EvalSetID: "train", EvalCaseID: "case_1"},
		atrace.Step{
			StepID:             "step_2",
			NodeID:             "node_1",
			PredecessorStepIDs: []string{"missing"},
		},
		nil,
		nil,
	)
	assert.Nil(t, request)
	assert.EqualError(t, err, `step "step_2" predecessor step id "missing" is unknown`)
	request, err = buildBackwardRequest(
		structure,
		nil,
		traceIndex,
		CaseResult{EvalSetID: "train", EvalCaseID: "case_1"},
		atrace.Step{
			StepID: "step_2",
			NodeID: "node_1",
		},
		nil,
		nil,
	)
	assert.NoError(t, err)
	require.NotNil(t, request)
	assert.NotNil(t, request.Input)
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

func TestNewRejectsMissingBackwarder(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		nil,
		&fakeAggregator{},
		&fakeOptimizer{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "backwarder is nil")
	assert.Nil(t, engineInstance)
}

func TestNewRejectsMissingAggregator(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		nil,
		&fakeOptimizer{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aggregator is nil")
	assert.Nil(t, engineInstance)
}

func TestNewRejectsMissingOptimizer(t *testing.T) {
	engineInstance, err := New(
		context.Background(),
		testTargetAgent(),
		newTestAgentEvaluator(t, newScriptedEvalService(scriptedOutcome)),
		&fakeBackwarder{},
		&fakeAggregator{},
		nil,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "optimizer is nil")
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

func TestDescribeRejectsMissingTargetAgent(t *testing.T) {
	engineInstance := &engine{}
	snapshot, err := engineInstance.Describe(context.Background())
	assert.Nil(t, snapshot)
	assert.EqualError(t, err, "target agent is nil")
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

func readPrivateField[T any](t *testing.T, target any, fieldName string) T {
	t.Helper()
	value := reflect.ValueOf(target)
	require.Equal(t, reflect.Ptr, value.Kind())
	value = value.Elem()
	field := value.FieldByName(fieldName)
	require.True(t, field.IsValid())
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(T)
}
