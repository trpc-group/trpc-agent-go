//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import (
	"context"
	"fmt"
	"math/rand"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// BaselineEvaluator runs baseline evaluation on train and validation sets.
// In fake_deterministic mode it produces deterministic, pre-programmed results
// based on case IDs and round number so the pipeline runs without API keys.
type BaselineEvaluator struct {
	mode    Mode
	rng     *rand.Rand
	prompts map[string]string
}

// NewBaselineEvaluator builds a baseline evaluator.
func NewBaselineEvaluator(mode Mode, seed int64, prompts map[string]string) *BaselineEvaluator {
	return &BaselineEvaluator{
		mode:    mode,
		rng:     rand.New(rand.NewSource(seed)),
		prompts: prompts,
	}
}

// fakeCaseOutcome describes the deterministic outcome of one eval case.
type fakeCaseOutcome struct {
	Labels        []string
	BaselineScore float64
	OptDelta      map[string]float64 // surface -> score delta when patched
	PassBaseline  bool
	PassOptimized map[string]bool // surface -> pass after patch
	Response      string
	Tools         []ToolStep
	FailReason    FailureCategory
	FailReasonStr string
}

// caseOutcomes maps EvalCaseID -> deterministic outcome.
// Covers the three required categories:
//   - "optimizable": baseline fails / optimized passes (score rises)
//   - "noeffect": baseline passes / optimized stays same (score flat)
//   - "degrade": baseline passes / optimized regresses (score drops)
var caseOutcomes = map[string]fakeCaseOutcome{
	// ======== TRAIN SET ========
	"train_case_opt_01": {
		Labels:        []string{"train", "optimizable", "response_mismatch"},
		BaselineScore: 0.45, PassBaseline: false,
		OptDelta:      map[string]float64{"system_prompt": 0.50, "agent_instruction": 0.30},
		PassOptimized: map[string]bool{"system_prompt": true, "agent_instruction": false},
		Response:      "I don't have enough info to answer that.",
		FailReason:    FinalResponseMismatch,
		FailReasonStr: "baseline agent hedges instead of answering; expected answer: Paris",
		Tools:         nil,
	},
	"train_case_opt_02": {
		Labels:        []string{"train", "optimizable", "tool_arg_error"},
		BaselineScore: 0.50, PassBaseline: false,
		OptDelta:      map[string]float64{"tool_desc_calc": 0.45, "system_prompt": 0.20},
		PassOptimized: map[string]bool{"tool_desc_calc": true, "system_prompt": false},
		Response:      "The result is: (error) invalid operand",
		FailReason:    ToolArgumentError,
		FailReasonStr: "baseline passes string operand to calculator; expects numeric",
		Tools: []ToolStep{
			{ToolName: "calculator", Args: map[string]any{"a": "five", "b": "3", "op": "+"}, Error: "strconv.ParseFloat: invalid syntax"},
		},
	},
	"train_case_opt_03": {
		Labels:        []string{"train", "noeffect"},
		BaselineScore: 1.00, PassBaseline: true,
		OptDelta:      map[string]float64{"system_prompt": 0.00, "agent_instruction": 0.00, "tool_desc_calc": 0.00},
		PassOptimized: map[string]bool{"system_prompt": true, "agent_instruction": true, "tool_desc_calc": true},
		Response:      "Answer: 42",
	},

	// ======== VALIDATION SET ========
	"val_case_opt_01": {
		Labels:        []string{"validation", "optimizable", "knowledge_recall"},
		BaselineScore: 0.40, PassBaseline: false,
		OptDelta:      map[string]float64{"system_prompt": 0.55, "agent_instruction": 0.25},
		PassOptimized: map[string]bool{"system_prompt": true, "agent_instruction": false},
		Response:      "Not sure about that.",
		FailReason:    KnowledgeRecallInsufficient,
		FailReasonStr: "baseline fails to remember the skill policy; should recall 'Always cite source when answering factual question'",
	},
	"val_case_opt_02": {
		Labels:        []string{"validation", "degrade", "route_error"},
		BaselineScore: 0.95, PassBaseline: true,
		// Only the router_prompt patch causes degradation (round 3).
		// system_prompt (round 1), tool_desc_calc (round 2), and agent_instruction
		// leave this case untouched so earlier rounds can be accepted or rejected
		// by other gates cleanly.
		OptDelta:      map[string]float64{"router_prompt": -0.40, "system_prompt": 0.00, "agent_instruction": 0.00, "tool_desc_calc": 0.00},
		PassOptimized: map[string]bool{"router_prompt": false, "system_prompt": true, "agent_instruction": true, "tool_desc_calc": true},
		Response:      "Routed to MathAgent: answer is 7.0 (expects EmailAgent)",
		FailReason:    RouteError,
		FailReasonStr: "candidate over-optimized routing; sends email task to MathAgent",
		Tools: []ToolStep{
			{ToolName: "route", Args: map[string]any{"target": "MathAgent"}, Output: "7.0"},
		},
	},
	"val_case_opt_03": {
		Labels:        []string{"validation", "noeffect", "hardfail_guard"},
		BaselineScore: 1.00, PassBaseline: true,
		OptDelta:      map[string]float64{"system_prompt": 0.00, "agent_instruction": 0.00, "router_prompt": 0.00, "tool_desc_calc": 0.00},
		PassOptimized: map[string]bool{"system_prompt": true, "agent_instruction": true, "router_prompt": true, "tool_desc_calc": true},
		Response:      "OK: email sent, id=msg_001",
	},
}

// isMemberOfSet returns true if the caseID belongs to train or validation sets.
func isMemberOfSet(setID, caseID string) bool {
	switch setID {
	case "train":
		return caseID == "train_case_opt_01" || caseID == "train_case_opt_02" || caseID == "train_case_opt_03"
	case "validation":
		return caseID == "val_case_opt_01" || caseID == "val_case_opt_02" || caseID == "val_case_opt_03"
	}
	return false
}

// EvaluateSet runs baseline evaluation for the given setID ("train" or "validation")
// and optionally an active candidate prompt patch (nil -> pure baseline).
func (e *BaselineEvaluator) EvaluateSet(
	_ context.Context,
	setID string,
	candidate *PromptCandidate,
) (*EvalSummary, error) {
	outcomes := []fakeCaseOutcome{}
	for cid, oc := range caseOutcomes {
		if isMemberOfSet(setID, cid) {
			outcomes = append(outcomes, oc)
		}
	}
	// Sort by id for deterministic ordering.
	orderedIDs := []string{}
	for cid := range caseOutcomes {
		if isMemberOfSet(setID, cid) {
			orderedIDs = append(orderedIDs, cid)
		}
	}
	// Quick insertion sort by string id.
	for i := 1; i < len(orderedIDs); i++ {
		for j := i; j > 0 && orderedIDs[j] < orderedIDs[j-1]; j-- {
			orderedIDs[j], orderedIDs[j-1] = orderedIDs[j-1], orderedIDs[j]
		}
	}

	caseResults := make([]CaseEval, 0, len(orderedIDs))
	passed := 0
	totalScore := 0.0
	totalMetrics := 0

	for _, cid := range orderedIDs {
		oc := caseOutcomes[cid]
		score := oc.BaselineScore
		passFlag := oc.PassBaseline

		if candidate != nil {
			// Apply patches: sum deltas across all patched surfaces for this case.
			delta := 0.0
			anyPatchedSurfacePasses := false
			for surface := range candidate.Patches {
				if d, ok := oc.OptDelta[surface]; ok {
					delta += d
				}
				if p, ok := oc.PassOptimized[surface]; ok && p {
					anyPatchedSurfacePasses = true
				}
			}
			score += delta
			// Clamp score to [0, 1]
			if score < 0 {
				score = 0
			} else if score > 1 {
				score = 1
			}
			passFlag = anyPatchedSurfacePasses || score >= 0.80
		}

		m := CaseMetric{
			MetricName: "overall",
			Score:      score,
			Threshold:  0.80,
			Passed:     passFlag,
			EvalStatus: status.EvalStatusFailed,
		}
		if passFlag {
			m.EvalStatus = status.EvalStatusPassed
		}
		if !passFlag {
			m.Reason = fmt.Sprintf("%s: %s", oc.FailReason, oc.FailReasonStr)
		}
		caseResult := CaseEval{
			EvalCaseID:     cid,
			EvalSetID:      setID,
			OverallPassed:  passFlag,
			Metrics:        []CaseMetric{m},
			SessionID:      fmt.Sprintf("sess_%s_%s_%d", setID, cid, e.rng.Int63n(100000)),
			FinalResponse:  oc.Response,
			ToolTrajectory: oc.Tools,
			TraceID:        fmt.Sprintf("trace_%s_%s", setID, cid),
		}
		caseResults = append(caseResults, caseResult)

		if passFlag {
			passed++
		}
		totalScore += score
		totalMetrics++
	}

	overall := 0.0
	if totalMetrics > 0 {
		overall = totalScore / float64(totalMetrics)
	}
	summary := &EvalSummary{
		EvalSetID:    setID,
		TotalCases:   len(caseResults),
		PassedCases:  passed,
		FailedCases:  len(caseResults) - passed,
		OverallScore: overall,
		PerCase:      caseResults,
	}
	// Run attribution for failing metrics.
	summary.Attribution = AttributeFailures(summary)
	return summary, nil
}
