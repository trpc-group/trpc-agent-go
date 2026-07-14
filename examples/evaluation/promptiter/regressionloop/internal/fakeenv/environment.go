//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fakeenv assembles the deterministic, file-backed PromptIter example.
package fakeenv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/config"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/fakemodel"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Public environment identifiers match the bundled example data and surface.
const (
	AppName   = "promptiter-recap-app"
	SurfaceID = "candidate#instruction"
)

// Environment contains the real PromptIter engine and the evaluator used for candidate-train regression.
type Environment struct {
	Engine         engine.Engine
	Evaluator      *Evaluator
	InitialProfile *promptiter.Profile
	close          func() error
}

// Close releases runner and evaluation resources.
func (e *Environment) Close() error {
	if e == nil || e.close == nil {
		return nil
	}
	return e.close()
}

// New loads the configured input files and constructs a deterministic Agent plus the standard local evaluation service.
// The JSON files, rather than an in-code outcome table, are the sole source of expected responses and metric policy.
func New(ctx context.Context, baseDir, baseline string, cfg *config.Config) (*Environment, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	if err := loadInputs(ctx, baseDir, cfg, evalSetManager, metricManager); err != nil {
		return nil, err
	}
	candidateModel := fakemodel.New(fakemodel.RoleCandidate)
	candidateAgent := llmagent.New(
		"candidate",
		llmagent.WithModel(candidateModel),
		llmagent.WithInstruction(baseline),
		llmagent.WithTools([]tool.Tool{lookupGameTool()}),
	)
	candidateRunner := runner.NewRunner(AppName, candidateAgent)
	agentEvaluator, err := evaluation.New(
		AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultinmemory.New()),
		evaluation.WithRegistry(registry.New()),
	)
	if err != nil {
		_ = candidateRunner.Close()
		return nil, fmt.Errorf("create file-backed evaluator: %w", err)
	}
	meteredEvaluator := newMeteredEvaluator(agentEvaluator, candidateModel)
	structure := &astructure.Snapshot{
		StructureID: "recap-structure-v1",
		EntryNodeID: "candidate",
		Nodes:       []astructure.Node{{NodeID: "candidate", Kind: astructure.NodeKindLLM, Name: "candidate"}},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: SurfaceID,
				NodeID:    "candidate",
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: stringPointer(baseline)},
			},
			{
				SurfaceID: "candidate#model",
				NodeID:    "candidate",
				Type:      astructure.SurfaceTypeModel,
				Value:     astructure.SurfaceValue{Model: &astructure.ModelRef{Name: "fake-deterministic"}},
			},
			{
				SurfaceID: "candidate#tool.lookup_game",
				NodeID:    "candidate",
				Type:      astructure.SurfaceTypeTool,
				Value:     astructure.SurfaceValue{Tools: []astructure.ToolRef{{ID: "lookup_game", Description: "Look up a game by game_id."}}},
			},
		},
	}
	optimizerModel := fakemodel.New(fakemodel.RoleOptimizer)
	engineInstance, err := engine.New(
		ctx,
		engine.WithStructure(structure),
		engine.WithAgentEvaluator(meteredEvaluator),
		engine.WithBackwarder(scriptedBackwarder{}),
		engine.WithAggregator(scriptedAggregator{}),
		engine.WithOptimizer(&scriptedOptimizer{model: optimizerModel}),
	)
	if err != nil {
		_ = agentEvaluator.Close()
		_ = candidateRunner.Close()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &Environment{
		Engine: engineInstance,
		Evaluator: &Evaluator{
			evaluator: meteredEvaluator, meter: meteredEvaluator, candidateModel: candidateModel, optimizerModel: optimizerModel,
		},
		InitialProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: SurfaceID,
			Value:     astructure.SurfaceValue{Text: stringPointer(baseline)},
		}}},
		close: func() error {
			return errors.Join(agentEvaluator.Close(), candidateRunner.Close())
		},
	}, nil
}

func loadInputs(ctx context.Context, baseDir string, cfg *config.Config, sets evalset.Manager, metrics interface {
	Add(context.Context, string, string, *metric.EvalMetric) error
}) error {
	for _, input := range []struct {
		id   string
		path string
	}{
		{id: cfg.Evaluation.TrainEvalSetID, path: cfg.Evaluation.TrainFile},
		{id: cfg.Evaluation.ValidationEvalSetID, path: cfg.Evaluation.ValidationFile},
	} {
		set, err := readEvalSet(filepath.Join(baseDir, input.path))
		if err != nil {
			return err
		}
		if set.EvalSetID != input.id {
			return fmt.Errorf("evalset file %q has id %q, want %q", input.path, set.EvalSetID, input.id)
		}
		if _, err := sets.Create(ctx, AppName, input.id); err != nil {
			return fmt.Errorf("create evalset %s: %w", input.id, err)
		}
		for _, evalCase := range set.EvalCases {
			if err := sets.AddCase(ctx, AppName, input.id, evalCase); err != nil {
				return fmt.Errorf("add eval case %s/%s: %w", input.id, evalCase.EvalID, err)
			}
		}
	}
	configuredMetrics, err := readMetrics(filepath.Join(baseDir, cfg.Evaluation.MetricsFile))
	if err != nil {
		return err
	}
	for _, evalSetID := range []string{cfg.Evaluation.TrainEvalSetID, cfg.Evaluation.ValidationEvalSetID} {
		for _, evalMetric := range configuredMetrics {
			if err := metrics.Add(ctx, AppName, evalSetID, evalMetric); err != nil {
				return fmt.Errorf("add metric %s/%s: %w", evalSetID, evalMetric.MetricName, err)
			}
		}
	}
	return nil
}

func readEvalSet(path string) (*evalset.EvalSet, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read evalset %q: %w", path, err)
	}
	var value evalset.EvalSet
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("decode evalset %q: %w", path, err)
	}
	if value.EvalSetID == "" || len(value.EvalCases) == 0 {
		return nil, fmt.Errorf("evalset %q has no id or cases", path)
	}
	return &value, nil
}

func readMetrics(path string) ([]*metric.EvalMetric, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read metrics %q: %w", path, err)
	}
	var value []*metric.EvalMetric
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("decode metrics %q: %w", path, err)
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("metrics %q is empty", path)
	}
	return value, nil
}

// Evaluator uses the same evaluation service and metric managers as PromptIter for candidate-train measurements.
type Evaluator struct {
	evaluator      evaluation.AgentEvaluator
	meter          *meteredEvaluator
	candidateModel *fakemodel.Model
	optimizerModel *fakemodel.Model
}

// EvaluateProfile runs the real evaluator with the profile's public instruction surface patch.
func (e *Evaluator) EvaluateProfile(ctx context.Context, evalSetID string, profile *promptiter.Profile) (*engine.EvaluationResult, error) {
	runOptions := []agent.RunOption{agent.WithExecutionTraceEnabled(true)}
	if text := profileText(profile); text != "" {
		var patch agent.SurfacePatch
		patch.SetInstruction(text)
		runOptions = append(runOptions, agent.WithSurfacePatchForNode("candidate", patch))
	}
	result, err := e.evaluator.Evaluate(ctx, evalSetID, evaluation.WithRunDetailsEnabled(true), evaluation.WithRunOptions(runOptions...))
	if err != nil {
		return nil, err
	}
	return adaptEvaluationResult(evalSetID, result)
}

// ModelCalls reports all fake candidate and optimizer invocations used by the run.
func (e *Evaluator) ModelCalls() int { return int(e.candidateModel.Calls() + e.optimizerModel.Calls()) }

// Measure reports the latest independent evaluation measurement for one profile.
func (e *Evaluator) Measure(evalSetID string, profile *promptiter.Profile) regression.ResourceMeasurement {
	return e.meter.Measure(evalSetID, profileVersion(profile))
}

// TotalModelCalls returns the actual deterministic model invocation count.
func (e *Evaluator) TotalModelCalls() int { return e.ModelCalls() }

// TotalCost returns deterministic cost derived from actual model and tool calls.
func (e *Evaluator) TotalCost() float64 {
	return float64(e.ModelCalls())*fakeModelCallCost + float64(e.meter.TotalToolCalls())*fakeToolCallCost
}

const (
	fakeModelCallCost    = 0.001
	fakeToolCallCost     = 0.0002
	fakeCaseLatency      = 0.005
	fakeModelCallLatency = 0.01
	fakeToolCallLatency  = 0.002
)

type meteredEvaluator struct {
	evaluation.AgentEvaluator
	candidateModel *fakemodel.Model
	evaluationMu   sync.Mutex
	mu             sync.Mutex
	measurements   map[string]regression.ResourceMeasurement
	totalToolCalls int
}

func newMeteredEvaluator(evaluator evaluation.AgentEvaluator, candidateModel *fakemodel.Model) *meteredEvaluator {
	return &meteredEvaluator{AgentEvaluator: evaluator, candidateModel: candidateModel, measurements: make(map[string]regression.ResourceMeasurement)}
}

func (m *meteredEvaluator) Evaluate(ctx context.Context, evalSetID string, options ...evaluation.Option) (*evaluation.EvaluationResult, error) {
	m.evaluationMu.Lock()
	defer m.evaluationMu.Unlock()
	before := m.candidateModel.CallsByVersion()
	result, err := m.AgentEvaluator.Evaluate(ctx, evalSetID, options...)
	if err != nil {
		return nil, err
	}
	after := m.candidateModel.CallsByVersion()
	version, modelCalls := changedProfileVersion(before, after)
	caseRuns := len(result.EvalCases)
	toolCalls := countActualToolCalls(result)
	measurement := regression.ResourceMeasurement{
		Usage:          regression.Usage{EvaluationCaseRuns: caseRuns, ModelCalls: modelCalls, ToolCalls: toolCalls},
		LatencySeconds: float64(caseRuns)*fakeCaseLatency + float64(modelCalls)*fakeModelCallLatency + float64(toolCalls)*fakeToolCallLatency,
		Cost:           float64(modelCalls)*fakeModelCallCost + float64(toolCalls)*fakeToolCallCost,
	}
	m.mu.Lock()
	m.measurements[measurementKey(evalSetID, version)] = measurement
	m.totalToolCalls += toolCalls
	m.mu.Unlock()
	return result, nil
}

func (m *meteredEvaluator) Measure(evalSetID, version string) regression.ResourceMeasurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.measurements[measurementKey(evalSetID, version)]
}

func (m *meteredEvaluator) TotalToolCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalToolCalls
}

func changedProfileVersion(before, after map[string]int) (string, int) {
	version := "baseline"
	calls := 0
	for candidate, count := range after {
		if delta := count - before[candidate]; delta > 0 {
			version = candidate
			calls += delta
		}
	}
	return version, calls
}

func countActualToolCalls(result *evaluation.EvaluationResult) int {
	total := 0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		for _, details := range evalCase.RunDetails {
			if details == nil || details.Inference == nil {
				continue
			}
			for _, invocation := range details.Inference.Inferences {
				if invocation != nil {
					total += len(invocation.Tools)
				}
			}
		}
	}
	return total
}

func measurementKey(evalSetID, version string) string { return evalSetID + "\x00" + version }

func profileVersion(profile *promptiter.Profile) string {
	text := profileText(profile)
	for _, version := range []string{"v3", "v2", "v1"} {
		if strings.Contains(text, "version: "+version) {
			return version
		}
	}
	return "baseline"
}

func adaptEvaluationResult(evalSetID string, result *evaluation.EvaluationResult) (*engine.EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	set := engine.EvalSetResult{EvalSetID: evalSetID, Cases: make([]engine.CaseResult, 0, len(result.EvalCases))}
	metricCount := 0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		item := engine.CaseResult{EvalSetID: evalSetID, EvalCaseID: evalCase.EvalCaseID}
		if len(evalCase.RunDetails) > 0 && evalCase.RunDetails[0] != nil && evalCase.RunDetails[0].Inference != nil {
			inference := evalCase.RunDetails[0].Inference
			item.SessionID = inference.SessionID
			item.ActualInvocations = append([]*evalset.Invocation(nil), inference.Inferences...)
			if len(inference.ExecutionTraces) > 0 {
				item.Trace = inference.ExecutionTraces[0]
			}
		}
		if len(evalCase.EvalCaseResults) > 0 && evalCase.EvalCaseResults[0] != nil {
			for _, perInvocation := range evalCase.EvalCaseResults[0].EvalMetricResultPerInvocation {
				if perInvocation != nil && perInvocation.ExpectedInvocation != nil {
					item.ExpectedInvocations = append(item.ExpectedInvocations, perInvocation.ExpectedInvocation)
				}
			}
		}
		for _, metricResult := range evalCase.MetricResults {
			if metricResult == nil {
				continue
			}
			reason := ""
			if metricResult.Details != nil {
				reason = metricResult.Details.Reason
			}
			item.Metrics = append(item.Metrics, engine.MetricResult{MetricName: metricResult.MetricName, Score: metricResult.Score, Status: metricResult.EvalStatus, Reason: reason})
			set.OverallScore += metricResult.Score
			metricCount++
		}
		sort.Slice(item.Metrics, func(i, j int) bool {
			return item.Metrics[i].MetricName < item.Metrics[j].MetricName
		})
		set.Cases = append(set.Cases, item)
	}
	if metricCount == 0 {
		return nil, errors.New("evaluation produced no metric results")
	}
	set.OverallScore /= float64(metricCount)
	sort.Slice(set.Cases, func(i, j int) bool {
		return set.Cases[i].EvalCaseID < set.Cases[j].EvalCaseID
	})
	return &engine.EvaluationResult{OverallScore: set.OverallScore, EvalSets: []engine.EvalSetResult{set}}, nil
}

type scriptedBackwarder struct{}

func (scriptedBackwarder) Backward(_ context.Context, request *backwarder.Request) (*backwarder.Result, error) {
	if len(request.AllowedGradientSurfaceIDs) == 0 {
		return nil, errors.New("no target surface for deterministic backward")
	}
	return &backwarder.Result{Gradients: []promptiter.SurfaceGradient{{
		EvalSetID: request.EvalSetID, EvalCaseID: request.EvalCaseID, StepID: request.StepID,
		SurfaceID: request.AllowedGradientSurfaceIDs[0], Severity: promptiter.LossSeverityP1,
		Gradient: "address the measured failure without regressing validation",
	}}}, nil
}

type scriptedAggregator struct{}

func (scriptedAggregator) Aggregate(_ context.Context, request *aggregator.Request) (*aggregator.Result, error) {
	return &aggregator.Result{Gradient: &promptiter.AggregatedSurfaceGradient{
		SurfaceID: request.SurfaceID, NodeID: request.NodeID, Type: request.Type, Gradients: request.Gradients,
	}}, nil
}

type scriptedOptimizer struct{ model model.Model }

func (s *scriptedOptimizer) Optimize(ctx context.Context, request *optimizer.Request) (*optimizer.Result, error) {
	responses, err := s.model.GenerateContent(ctx, &model.Request{})
	if err != nil {
		return nil, err
	}
	var content string
	for response := range responses {
		if response == nil || len(response.Choices) == 0 {
			continue
		}
		content += response.Choices[0].Message.Content
	}
	var proposal struct {
		Value  astructure.SurfaceValue
		Reason string
	}
	if err := json.Unmarshal([]byte(content), &proposal); err != nil {
		return nil, fmt.Errorf("decode deterministic optimizer response: %w", err)
	}
	if proposal.Value.Text == nil {
		return nil, errors.New("deterministic optimizer response has no text value")
	}
	return &optimizer.Result{Patch: &promptiter.SurfacePatch{SurfaceID: request.Surface.SurfaceID, Value: proposal.Value, Reason: proposal.Reason}}, nil
}

type lookupGameInput struct {
	GameID string `json:"game_id"`
}

func lookupGameTool() tool.Tool {
	return function.NewFunctionTool(func(_ context.Context, input lookupGameInput) (map[string]string, error) {
		return map[string]string{"game_id": input.GameID, "source": "deterministic-fixture"}, nil
	}, function.WithName("lookup_game"), function.WithDescription("Look up a game by game_id."))
}

func profileText(profile *promptiter.Profile) string {
	if profile == nil {
		return ""
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID == SurfaceID && override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return ""
}

func stringPointer(value string) *string { return &value }
