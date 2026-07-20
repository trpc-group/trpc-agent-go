//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	baselineMarker  = "fixture:baseline"
	ineffectiveMark = "fixture:ineffective"
	overfitMarker   = "fixture:train-only"
	balancedMarker  = "fixture:balanced"
)

func runFakeLoop(ctx context.Context, config loopConfig) (*regression.OptimizationRun, error) {
	evaluate := func(ctx context.Context, prompt, evalSetID string, _ int64) (*regression.EvaluationOutput, error) {
		return evaluatePrompt(ctx, config, prompt, evalSetID)
	}
	generate := func(ctx context.Context, request regression.CandidateRequest) (*regression.Candidate, error) {
		if request.Round <= 0 || request.Round > len(config.candidates) {
			return nil, fmt.Errorf("candidate round %d is not configured", request.Round)
		}
		engine, counter, closeRuntime, err := newPromptIterRuntime(ctx, config, config.candidates[request.Round-1])
		if err != nil {
			return nil, err
		}
		defer closeRuntime()
		prompt, err := regression.GeneratePromptIter(ctx, engine, promptiterengine.RunRequest{
			Train:      []promptiterengine.EvalSetInput{{EvalSetID: config.train.EvalSetID}},
			Validation: []promptiterengine.EvalSetInput{{EvalSetID: config.validation.EvalSetID}},
		}, targetSurfaceID, request)
		if err != nil {
			return nil, err
		}
		return &regression.Candidate{Prompt: prompt, Cost: counter.snapshot()}, nil
	}
	return regression.Run(ctx, regression.RunRequest{
		InitialPrompt: config.baseline, TrainEvalSetID: config.train.EvalSetID,
		ValidationEvalSetID: config.validation.EvalSetID, GatePolicy: config.gate,
		MaxRounds: config.maxRounds, Seed: config.seed,
	}, evaluate, generate)
}

func evaluatePrompt(ctx context.Context, config loopConfig, prompt, evalSetID string) (_ *regression.EvaluationOutput, resultErr error) {
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	evalResultManager := evalresultinmemory.New()
	defer func() {
		resultErr = errors.Join(resultErr, evalSetManager.Close(), metricManager.Close(), evalResultManager.Close())
	}()
	fixture, err := fixtureByID(config, evalSetID)
	if err != nil {
		return nil, err
	}
	if err := addFixture(ctx, evalSetManager, metricManager, fixture, config); err != nil {
		return nil, err
	}
	counter := &costCounter{engine: config.engine}
	evaluator, err := evaluation.New(appName, &deterministicRunner{prompt: prompt, counter: counter},
		evaluation.WithEvalSetManager(evalSetManager), evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager), evaluation.WithRegistry(registry.New()), evaluation.WithNumRuns(1))
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, evaluator.Close()) }()
	result, err := evaluator.Evaluate(ctx, evalSetID, evaluation.WithRunDetailsEnabled(true))
	if err != nil {
		return nil, err
	}
	metricNames := make([]string, 0, len(config.metrics))
	for _, configuredMetric := range config.metrics {
		metricNames = append(metricNames, configuredMetric.MetricName)
	}
	return &regression.EvaluationOutput{
		Result: result, Cost: counter.snapshot(), MetricNames: metricNames,
	}, nil
}

func newPromptIterRuntime(
	ctx context.Context, config loopConfig, candidate string,
) (promptiterengine.Engine, *costCounter, func() error, error) {
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	evalResultManager := evalresultinmemory.New()
	closeManagers := func() error {
		return errors.Join(evalSetManager.Close(), metricManager.Close(), evalResultManager.Close())
	}
	for _, fixture := range []*evalset.EvalSet{config.train, config.validation} {
		if err := addFixture(ctx, evalSetManager, metricManager, fixture, config); err != nil {
			return nil, nil, closeManagers, err
		}
	}
	counter := &costCounter{engine: config.engine}
	evaluator, err := evaluation.New(appName, &deterministicRunner{prompt: config.baseline, counter: counter},
		evaluation.WithEvalSetManager(evalSetManager), evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager), evaluation.WithRegistry(registry.New()), evaluation.WithNumRuns(1))
	if err != nil {
		return nil, nil, closeManagers, err
	}
	closeRuntime := func() error { return errors.Join(evaluator.Close(), closeManagers()) }
	backwardRunner := &jsonRunner{counter: counter, output: fmt.Sprintf(
		`{"Gradients":[{"SurfaceID":%q,"Severity":"P1","Gradient":"fix failed response"}],"Upstream":[]}`, targetSurfaceID)}
	aggregatorRunner := &jsonRunner{counter: counter, output: `{"Gradients":[{"Severity":"P1","Gradient":"fix failed response"}]}`}
	candidateJSON, _ := json.Marshal(candidate)
	optimizerRunner := &jsonRunner{counter: counter, output: fmt.Sprintf(
		`{"Value":{"Text":%s},"Reason":"apply failure hints"}`, candidateJSON)}
	backwarderInstance, err := backwarder.New(ctx, backwardRunner)
	if err != nil {
		return nil, nil, closeRuntime, err
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		return nil, nil, closeRuntime, err
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		return nil, nil, closeRuntime, err
	}
	baseline := config.baseline
	structure := &astructure.Snapshot{
		StructureID: "fixture-agent", EntryNodeID: "answer",
		Nodes: []astructure.Node{{NodeID: "answer", Kind: astructure.NodeKindLLM, Name: agentName}},
		Surfaces: []astructure.Surface{{SurfaceID: targetSurfaceID, NodeID: "answer", Type: astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{Text: &baseline}}},
	}
	engine, err := promptiterengine.New(ctx, promptiterengine.WithStructure(structure),
		promptiterengine.WithAgentEvaluator(evaluator), promptiterengine.WithBackwarder(backwarderInstance),
		promptiterengine.WithAggregator(aggregatorInstance), promptiterengine.WithOptimizer(optimizerInstance))
	return engine, counter, closeRuntime, err
}

func addFixture(ctx context.Context, sets evalset.Manager, metrics metric.Manager, fixture *evalset.EvalSet, config loopConfig) error {
	if _, err := sets.Create(ctx, appName, fixture.EvalSetID); err != nil {
		return err
	}
	for _, evalCase := range fixture.EvalCases {
		if err := sets.AddCase(ctx, appName, fixture.EvalSetID, evalCase); err != nil {
			return err
		}
	}
	for _, evalMetric := range config.metrics {
		if err := metrics.Add(ctx, appName, fixture.EvalSetID, evalMetric); err != nil {
			return err
		}
	}
	return nil
}

func fixtureByID(config loopConfig, evalSetID string) (*evalset.EvalSet, error) {
	if config.train.EvalSetID == evalSetID {
		return config.train, nil
	}
	if config.validation.EvalSetID == evalSetID {
		return config.validation, nil
	}
	return nil, fmt.Errorf("evalset %q is not configured", evalSetID)
}

type deterministicRunner struct {
	prompt  string
	counter *costCounter
}

func (r *deterministicRunner) Run(_ context.Context, _, _ string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	var runOptions agent.RunOptions
	for _, option := range opts {
		option(&runOptions)
	}
	prompt := r.prompt
	if runOptions.Instruction != "" {
		prompt = runOptions.Instruction
	}
	r.counter.record(2)
	content := "not-pong"
	if shouldReplyPong(prompt, message.Content) {
		content = "pong"
	}
	invocationID := "fixture-invocation"
	started := time.Unix(0, 0)
	events := []*event.Event{
		{InvocationID: invocationID, Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "echo-call", Function: model.FunctionDefinitionParam{Name: toolName, Arguments: []byte(`{"value":"ping"}`)}}},
		}}}}},
		{InvocationID: invocationID, Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: "echo-call", ToolName: toolName, Content: `{"value":"pong"}`}}}}},
		{InvocationID: invocationID, Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}}}},
		{InvocationID: invocationID, ExecutionTrace: &trace.Trace{
			RootAgentName: agentName, RootInvocationID: invocationID, Status: trace.TraceStatusCompleted,
			StartedAt: started, EndedAt: started.Add(time.Duration(2*r.counter.engine.LatencyMS) * time.Millisecond),
			Usage: &model.Usage{TotalTokens: 2 * (r.counter.engine.PromptTokens + r.counter.engine.CompletionTokens)},
			Steps: []trace.Step{{StepID: "answer", AgentName: agentName, NodeID: "answer", NodeType: "llm",
				Input: &trace.Snapshot{Text: message.Content}, Output: &trace.Snapshot{Text: content}, AppliedSurfaceIDs: []string{targetSurfaceID}}},
		}, Response: &model.Response{Object: model.ObjectTypeRunnerCompletion, Done: true}},
	}
	channel := make(chan *event.Event, len(events))
	for _, item := range events {
		channel <- item
	}
	close(channel)
	return channel, nil
}

func (*deterministicRunner) Close() error { return nil }

type costCounter struct {
	mu     sync.Mutex
	engine fakeEngineConfig
	calls  int
}

func (c *costCounter) record(calls int) { c.mu.Lock(); c.calls += calls; c.mu.Unlock() }
func (c *costCounter) snapshot() regression.Cost {
	c.mu.Lock()
	defer c.mu.Unlock()
	return regression.Cost{ModelCalls: c.calls, Tokens: int64(c.calls * (c.engine.PromptTokens + c.engine.CompletionTokens)), LatencyMS: int64(c.calls) * c.engine.LatencyMS}
}

type jsonRunner struct {
	counter *costCounter
	output  string
}

func (r *jsonRunner) Run(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *event.Event, error) {
	r.counter.record(1)
	channel := make(chan *event.Event, 1)
	channel <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage(r.output)}}}}
	close(channel)
	return channel, nil
}
func (*jsonRunner) Close() error { return nil }

func shouldReplyPong(prompt, user string) bool {
	prompt, user = strings.ToLower(prompt), strings.ToLower(user)
	switch {
	case strings.Contains(prompt, balancedMarker):
		return true
	case strings.Contains(prompt, overfitMarker):
		return strings.Contains(user, "basic") || strings.Contains(user, "polite") || strings.Contains(user, "noisy")
	case strings.Contains(prompt, ineffectiveMark), strings.Contains(prompt, baselineMarker):
		return strings.Contains(user, "basic") || strings.Contains(user, "short")
	default:
		return false
	}
}

var _ runner.Runner = (*deterministicRunner)(nil)
