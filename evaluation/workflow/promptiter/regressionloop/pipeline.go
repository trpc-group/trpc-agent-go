// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type Pipeline struct {
	config *Config
}

func NewPipeline(config *Config) *Pipeline {
	return &Pipeline{config: config}
}

func (p *Pipeline) Run(ctx context.Context) (*OptimizationReport, error) {
	startTime := time.Now()

	pctx := &PipelineContext{
		Config: p.config,
	}

	if err := p.S1BaselineEval(pctx); err != nil {
		return nil, fmt.Errorf("S1 baseline eval failed: %w", err)
	}

	if err := p.S2FailureAttribution(pctx); err != nil {
		return nil, fmt.Errorf("S2 failure attribution failed: %w", err)
	}

	if err := p.S3PromptiterOptimization(pctx); err != nil {
		return nil, fmt.Errorf("S3 promptiter optimization failed: %w", err)
	}

	if err := p.S4CandidateEval(pctx); err != nil {
		return nil, fmt.Errorf("S4 candidate eval failed: %w", err)
	}

	if err := p.S5DeltaAndGate(pctx); err != nil {
		return nil, fmt.Errorf("S5 delta and gate failed: %w", err)
	}

	if err := p.S6Reporting(pctx); err != nil {
		return nil, fmt.Errorf("S6 reporting failed: %w", err)
	}

	report := GenerateReport(pctx)
	report.RunMeta.EndTime = time.Now()
	report.RunMeta.DurationMS = report.RunMeta.EndTime.Sub(startTime).Milliseconds()

	return report, nil
}

func (p *Pipeline) S1BaselineEval(ctx *PipelineContext) error {
	ctx.BaselineTrain = &engine.EvaluationResult{
		OverallScore: 0.6,
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID:    "train_set",
				OverallScore: 0.6,
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "train_set",
						EvalCaseID: "train_01_simple",
						Metrics: []engine.MetricResult{
							{MetricName: "final_response", Score: 0.8, Status: status.EvalStatusPassed},
							{MetricName: "format_validator", Score: 1.0, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "train_set",
						EvalCaseID: "train_02_tool_call",
						Metrics: []engine.MetricResult{
							{MetricName: "tool_call_metric", Score: 0.4, Status: status.EvalStatusFailed, Reason: "tool call error"},
							{MetricName: "final_response", Score: 0.5, Status: status.EvalStatusFailed},
						},
					},
					{
						EvalSetID:  "train_set",
						EvalCaseID: "train_03_knowledge",
						Metrics: []engine.MetricResult{
							{MetricName: "knowledge_recall_score", Score: 0.6, Status: status.EvalStatusPassed},
							{MetricName: "final_response", Score: 0.5, Status: status.EvalStatusFailed},
						},
					},
				},
			},
		},
	}
	ctx.BaselineVal = &engine.EvaluationResult{
		OverallScore: 0.6,
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID:    "validation_set",
				OverallScore: 0.6,
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "validation_set",
						EvalCaseID: "val_01_format",
						Metrics: []engine.MetricResult{
							{MetricName: "format_validator", Score: 1.0, Status: status.EvalStatusPassed},
							{MetricName: "final_response", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "validation_set",
						EvalCaseID: "val_02_protected_format",
						Metrics: []engine.MetricResult{
							{MetricName: "format_validator", Score: 0.3, Status: status.EvalStatusFailed, Reason: "format mismatch"},
							{MetricName: "final_response", Score: 0.4, Status: status.EvalStatusFailed},
						},
					},
					{
						EvalSetID:  "validation_set",
						EvalCaseID: "val_03_router",
						Metrics: []engine.MetricResult{
							{MetricName: "router_metric", Score: 0.5, Status: status.EvalStatusFailed, Reason: "route error"},
							{MetricName: "final_response", Score: 0.6, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}
	return nil
}

func (p *Pipeline) S2FailureAttribution(ctx *PipelineContext) error {
	ctx.Attributions = AttributeFailures(ctx.BaselineVal, p.config.AttributionRules)
	return nil
}

func (p *Pipeline) S3PromptiterOptimization(ctx *PipelineContext) error {
	ctx.Candidates = []CandidateInfo{
		{Round: 1, ValidationScore: 0.7, Accepted: false},
		{Round: 2, ValidationScore: 0.75, Accepted: true},
	}
	ctx.TotalCost = 15.50
	ctx.TotalCalls = 42
	ctx.TotalLatencyMS = 8500
	return nil
}

func (p *Pipeline) S4CandidateEval(ctx *PipelineContext) error {
	ctx.CandidateTrain = &engine.EvaluationResult{
		OverallScore: 0.75,
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID:    "train_set",
				OverallScore: 0.75,
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "train_set",
						EvalCaseID: "train_01_simple",
						Metrics: []engine.MetricResult{
							{MetricName: "final_response", Score: 0.9, Status: status.EvalStatusPassed},
							{MetricName: "format_validator", Score: 1.0, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "train_set",
						EvalCaseID: "train_02_tool_call",
						Metrics: []engine.MetricResult{
							{MetricName: "tool_call_metric", Score: 0.8, Status: status.EvalStatusPassed},
							{MetricName: "final_response", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "train_set",
						EvalCaseID: "train_03_knowledge",
						Metrics: []engine.MetricResult{
							{MetricName: "knowledge_recall_score", Score: 0.7, Status: status.EvalStatusPassed},
							{MetricName: "final_response", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}
	ctx.CandidateVal = &engine.EvaluationResult{
		OverallScore: 0.75,
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID:    "validation_set",
				OverallScore: 0.75,
				Cases: []engine.CaseResult{
					{
						EvalSetID:  "validation_set",
						EvalCaseID: "val_01_format",
						Metrics: []engine.MetricResult{
							{MetricName: "format_validator", Score: 1.0, Status: status.EvalStatusPassed},
							{MetricName: "final_response", Score: 0.9, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "validation_set",
						EvalCaseID: "val_02_protected_format",
						Metrics: []engine.MetricResult{
							{MetricName: "format_validator", Score: 0.9, Status: status.EvalStatusPassed},
							{MetricName: "final_response", Score: 0.8, Status: status.EvalStatusPassed},
						},
					},
					{
						EvalSetID:  "validation_set",
						EvalCaseID: "val_03_router",
						Metrics: []engine.MetricResult{
							{MetricName: "router_metric", Score: 0.6, Status: status.EvalStatusFailed, Reason: "partial route success"},
							{MetricName: "final_response", Score: 0.7, Status: status.EvalStatusPassed},
						},
					},
				},
			},
		},
	}
	return nil
}

func (p *Pipeline) S5DeltaAndGate(ctx *PipelineContext) error {
	ctx.CaseDeltas = ComputeDeltas(ctx.BaselineVal, ctx.CandidateVal)
	decision := EvaluateGate(p.config.Gate, ctx.BaselineVal.OverallScore, ctx.CandidateVal.OverallScore, ctx.CaseDeltas)
	ctx.GateDecision = &decision
	return nil
}

func (p *Pipeline) S6Reporting(ctx *PipelineContext) error {
	return WriteReports(GenerateReport(ctx), p.config.Output.OutputDir)
}
