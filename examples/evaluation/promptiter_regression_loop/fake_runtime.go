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
	"sync"

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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	deterministicNodeID    = "support-agent"
	deterministicSurfaceID = "support-agent#instruction"
)

type deterministicEvaluator struct {
	mu       sync.Mutex
	scenario string
	calls    map[string]int
}

func (e *deterministicEvaluator) Evaluate(
	ctx context.Context,
	evalSetID string,
	_ ...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	call := e.calls[evalSetID]
	e.calls[evalSetID] = call + 1
	e.mu.Unlock()

	var specs []caseSpec
	switch evalSetID {
	case "train":
		specs = []caseSpec{
			{"train_return_policy", 0.3, "knowledge recall is insufficient"},
			{"train_tool_lookup", 0.3, "tool argument is invalid"},
			{"train_response_format", 0.3, "format schema mismatch"},
		}
	case "validation":
		if call == 0 {
			specs = baselineCases()
		} else {
			var err error
			specs, err = candidateCases(e.scenario)
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unknown eval set %q", evalSetID)
	}
	return genericEvaluationResult(evalSetID, specs), nil
}

func (e *deterministicEvaluator) Close() error { return nil }

type deterministicBackwarder struct{}

func (deterministicBackwarder) Backward(
	ctx context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &backwarder.Result{Gradients: []promptiter.SurfaceGradient{{
		EvalSetID: request.EvalSetID, EvalCaseID: request.EvalCaseID, StepID: request.StepID,
		SurfaceID: deterministicSurfaceID, Severity: promptiter.LossSeverityP1,
		Gradient: "make the instruction policy-grounded and schema-preserving",
	}}}, nil
}

type deterministicAggregator struct{}

func (deterministicAggregator) Aggregate(
	ctx context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &aggregator.Result{Gradient: &promptiter.AggregatedSurfaceGradient{
		SurfaceID: request.SurfaceID, NodeID: request.NodeID, Type: request.Type,
		Gradients: request.Gradients,
	}}, nil
}

type deterministicOptimizer struct{}

func (deterministicOptimizer) Optimize(
	ctx context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prompt := "Answer with policy-grounded steps and preserve the required response schema."
	return &optimizer.Result{Patch: &promptiter.SurfacePatch{
		SurfaceID: request.Surface.SurfaceID,
		Value:     astructure.SurfaceValue{Text: &prompt},
		Reason:    "address the deterministic training failures",
	}}, nil
}

func newDeterministicEngine(ctx context.Context, scenario string) (promptiterengine.Engine, error) {
	if _, err := candidateCases(scenario); err != nil {
		return nil, err
	}
	baseline := "Answer support questions concisely."
	snapshot := &astructure.Snapshot{
		StructureID: "deterministic-support-agent",
		EntryNodeID: deterministicNodeID,
		Nodes: []astructure.Node{{
			NodeID: deterministicNodeID, Kind: astructure.NodeKindLLM, Name: "support-agent",
		}},
		Surfaces: []astructure.Surface{{
			SurfaceID: deterministicSurfaceID, NodeID: deterministicNodeID,
			Type: astructure.SurfaceTypeInstruction, Value: astructure.SurfaceValue{Text: &baseline},
		}},
	}
	return promptiterengine.New(ctx,
		promptiterengine.WithStructure(snapshot),
		promptiterengine.WithAgentEvaluator(&deterministicEvaluator{scenario: scenario, calls: make(map[string]int)}),
		promptiterengine.WithBackwarder(deterministicBackwarder{}),
		promptiterengine.WithAggregator(deterministicAggregator{}),
		promptiterengine.WithOptimizer(deterministicOptimizer{}),
	)
}

func deterministicRequest() *promptiterengine.RunRequest {
	return &promptiterengine.RunRequest{
		Train:            []promptiterengine.EvalSetInput{{EvalSetID: "train"}},
		Validation:       []promptiterengine.EvalSetInput{{EvalSetID: "validation"}},
		MaxRounds:        1,
		TargetSurfaceIDs: []string{deterministicSurfaceID},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{MinScoreGain: 0.05},
	}
}

func baselineCases() []caseSpec {
	return []caseSpec{
		{"validation_return_shipping", 0.4, "final response misses shipping policy"},
		{"validation_account_security", 1.0, ""},
		{"validation_refund_format", 0.4, "format schema mismatch"},
	}
}

func candidateCases(scenario string) ([]caseSpec, error) {
	switch scenario {
	case "success":
		return []caseSpec{{"validation_return_shipping", 1, ""}, {"validation_account_security", 1, ""}, {"validation_refund_format", 1, ""}}, nil
	case "ineffective":
		return baselineCases(), nil
	case "overfit":
		return []caseSpec{{"validation_return_shipping", 1, ""}, {"validation_account_security", 0, "routing error exposes an unsafe path"}, {"validation_refund_format", 1, ""}}, nil
	default:
		return nil, fmt.Errorf("unknown scenario %q", scenario)
	}
}

func genericEvaluationResult(evalSetID string, specs []caseSpec) *evaluation.EvaluationResult {
	cases := make([]*evaluation.EvaluationCaseResult, 0, len(specs))
	for _, spec := range specs {
		evalStatus := status.EvalStatusFailed
		if spec.score >= 0.6 {
			evalStatus = status.EvalStatusPassed
		}
		metric := &evalresult.EvalMetricResult{
			MetricName: "quality", Score: spec.score, EvalStatus: evalStatus,
			Details: &evalresult.EvalMetricResultDetails{Reason: spec.reason, Score: spec.score},
		}
		trace := &atrace.Trace{SessionID: spec.id + "-session", Status: atrace.TraceStatusCompleted, Steps: []atrace.Step{{
			StepID: spec.id + "-llm", NodeID: deterministicNodeID, NodeType: "llm",
			AppliedSurfaceIDs: []string{deterministicSurfaceID}, Usage: &model.Usage{TotalTokens: 100},
			Input: &atrace.Snapshot{Text: "input"}, Output: &atrace.Snapshot{Text: "output"},
		}}}
		runResult := &evalresult.EvalCaseResult{EvalSetID: evalSetID, EvalID: spec.id, RunID: 1, FinalEvalStatus: evalStatus, OverallEvalMetricResults: []*evalresult.EvalMetricResult{metric}}
		cases = append(cases, &evaluation.EvaluationCaseResult{
			EvalCaseID: spec.id, OverallStatus: evalStatus, EvalCaseResults: []*evalresult.EvalCaseResult{runResult}, MetricResults: []*evalresult.EvalMetricResult{metric},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1, Inference: &evaluation.EvaluationInferenceDetails{SessionID: trace.SessionID, Status: status.EvalStatusPassed, ExecutionTraces: []*atrace.Trace{trace}}}},
		})
	}
	return &evaluation.EvaluationResult{AppName: "promptiter-regression-loop", EvalSetID: evalSetID, EvalCases: cases}
}
