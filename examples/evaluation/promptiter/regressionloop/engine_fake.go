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
	"fmt"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	regressionloop "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regressionloop"
)

func newEnginePromptIterator(
	ctx context.Context,
	baseDir string,
	cfg regressionloop.Config,
) (regressionloop.PromptIterator, error) {
	surfaceID := firstTargetSurfaceID(cfg)
	structure := singleSurfaceStructure(surfaceID, "baseline prompt")
	metrics, err := regressionloop.LoadMetricDefinitions(cfg.MetricsPath)
	if err != nil {
		return nil, err
	}
	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithStructure(structure),
		promptiterengine.WithAgentEvaluator(&engineFakeEvaluator{
			baseDir:   baseDir,
			surfaceID: surfaceID,
			scenario:  scenarioOrDefault(cfg.Scenario),
			metrics:   metrics,
		}),
		promptiterengine.WithBackwarder(engineFakeBackwarder{surfaceID: surfaceID}),
		promptiterengine.WithAggregator(engineFakeAggregator{}),
		promptiterengine.WithOptimizer(engineFakeOptimizer{
			surfaceID: surfaceID,
			scenario:  scenarioOrDefault(cfg.Scenario),
		}),
	)
	if err != nil {
		return nil, err
	}
	return regressionloop.EnginePromptIterator{
		Engine: engineInstance,
	}, nil
}

type engineFakeEvaluator struct {
	baseDir         string
	surfaceID       string
	scenario        string
	metrics         []regressionloop.MetricDefinition
	validationCalls int
}

func (e *engineFakeEvaluator) Evaluate(
	_ context.Context,
	evalSetID string,
	_ ...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	variant := "baseline"
	if evalSetID == "validation" {
		e.validationCalls++
		if e.validationCalls > 1 {
			variant = candidateVariant(e.scenario)
		}
	} else {
		variant = candidateVariant(e.scenario)
	}
	result, err := loadFakeEvaluation(e.baseDir, evalSetID, variant, e.metrics)
	if err != nil {
		return nil, err
	}
	return engineEvaluationResult(result, e.surfaceID), nil
}

func (e *engineFakeEvaluator) Close() error { return nil }

type engineFakeBackwarder struct {
	surfaceID string
}

func (b engineFakeBackwarder) Backward(
	_ context.Context,
	req *backwarder.Request,
) (*backwarder.Result, error) {
	if req == nil {
		return nil, fmt.Errorf("backward request is nil")
	}
	return &backwarder.Result{
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  req.EvalSetID,
				EvalCaseID: req.EvalCaseID,
				StepID:     req.StepID,
				SurfaceID:  b.surfaceID,
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "preserve validation-critical behavior while fixing train failure attribution",
			},
		},
	}, nil
}

type engineFakeAggregator struct{}

func (engineFakeAggregator) Aggregate(
	_ context.Context,
	req *aggregator.Request,
) (*aggregator.Result, error) {
	if req == nil {
		return nil, fmt.Errorf("aggregation request is nil")
	}
	return &aggregator.Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: req.SurfaceID,
			NodeID:    req.NodeID,
			Type:      req.Type,
			Gradients: req.Gradients,
		},
	}, nil
}

type engineFakeOptimizer struct {
	surfaceID string
	scenario  string
}

func (o engineFakeOptimizer) Optimize(
	_ context.Context,
	req *optimizer.Request,
) (*optimizer.Result, error) {
	if req == nil || req.Surface == nil {
		return nil, fmt.Errorf("optimizer request surface is nil")
	}
	prompt := optimizedPromptForScenario(o.scenario)
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: req.Surface.SurfaceID,
			Value:     astructure.SurfaceValue{Text: &prompt},
			Reason:    "deterministic fake optimizer generated a scenario-specific candidate",
		},
	}, nil
}

func engineEvaluationResult(
	result *promptiterengine.EvaluationResult,
	surfaceID string,
) *evaluation.EvaluationResult {
	out := &evaluation.EvaluationResult{
		AppName:       "eval-optimization-regression-app",
		OverallStatus: status.EvalStatusPassed,
	}
	for _, set := range result.EvalSets {
		out.EvalSetID = set.EvalSetID
		for _, c := range set.Cases {
			overall := status.EvalStatusPassed
			metrics := make([]*evalresult.EvalMetricResult, 0, len(c.Metrics))
			for _, m := range c.Metrics {
				if m.Status == status.EvalStatusFailed {
					overall = status.EvalStatusFailed
					out.OverallStatus = status.EvalStatusFailed
				}
				metrics = append(metrics, &evalresult.EvalMetricResult{
					MetricName: m.MetricName,
					Score:      m.Score,
					EvalStatus: m.Status,
					Details: &evalresult.EvalMetricResultDetails{
						Reason: m.Reason,
						Score:  m.Score,
					},
				})
			}
			run := &evalresult.EvalCaseResult{
				EvalSetID:                set.EvalSetID,
				EvalID:                   c.EvalCaseID,
				RunID:                    1,
				FinalEvalStatus:          overall,
				OverallEvalMetricResults: metrics,
				SessionID:                "session-" + c.EvalCaseID,
				UserID:                   "user-fake",
			}
			out.EvalCases = append(out.EvalCases, &evaluation.EvaluationCaseResult{
				EvalCaseID:      c.EvalCaseID,
				OverallStatus:   overall,
				EvalCaseResults: []*evalresult.EvalCaseResult{run},
				MetricResults:   metrics,
				RunDetails: []*evaluation.EvaluationCaseRunDetails{
					{
						RunID: 1,
						Inference: &evaluation.EvaluationInferenceDetails{
							SessionID:       run.SessionID,
							UserID:          run.UserID,
							Status:          status.EvalStatusPassed,
							ExecutionTraces: []*atrace.Trace{caseTrace(c.EvalCaseID, surfaceID, overall)},
						},
					},
				},
			})
		}
	}
	return out
}

func caseTrace(evalCaseID, surfaceID string, overall status.EvalStatus) *atrace.Trace {
	traceStatus := atrace.TraceStatusCompleted
	stepError := ""
	return &atrace.Trace{
		RootAgentName:    "support_agent",
		RootInvocationID: "invocation-" + evalCaseID,
		SessionID:        "session-" + evalCaseID,
		Status:           traceStatus,
		Steps: []atrace.Step{
			{
				StepID:            "step-" + evalCaseID,
				InvocationID:      "invocation-" + evalCaseID,
				AgentName:         "support_agent",
				NodeID:            nodeIDFromSurface(surfaceID),
				AppliedSurfaceIDs: []string{surfaceID},
				Input:             &atrace.Snapshot{Text: "user request for " + evalCaseID},
				Output:            &atrace.Snapshot{Text: "candidate response for " + evalCaseID},
				Error:             stepError,
			},
		},
	}
}

func singleSurfaceStructure(surfaceID, prompt string) *astructure.Snapshot {
	nodeID := nodeIDFromSurface(surfaceID)
	return &astructure.Snapshot{
		StructureID: "fake-regression-loop-structure",
		EntryNodeID: nodeID,
		Nodes: []astructure.Node{
			{NodeID: nodeID, Kind: astructure.NodeKindLLM, Name: nodeID},
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: surfaceID,
				NodeID:    nodeID,
				Type:      astructure.SurfaceTypeInstruction,
				Value:     astructure.SurfaceValue{Text: &prompt},
			},
		},
	}
}

func firstTargetSurfaceID(cfg regressionloop.Config) string {
	if len(cfg.TargetSurfaceIDs) > 0 && cfg.TargetSurfaceIDs[0] != "" {
		return cfg.TargetSurfaceIDs[0]
	}
	return "support_agent#instruction"
}

func nodeIDFromSurface(surfaceID string) string {
	nodeID, _, ok := strings.Cut(surfaceID, "#")
	if ok && nodeID != "" {
		return nodeID
	}
	return "support_agent"
}
