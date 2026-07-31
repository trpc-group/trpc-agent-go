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
	"maps"
	"time"
)

// PipelineVersion is persisted in the audit report for traceability.
const PipelineVersion = "v0.1.0-closedloop"

// Pipeline orchestrates baseline evaluation → failure attribution →
// PromptIter optimization → candidate validation → acceptance gate →
// audit persistence.
type Pipeline struct {
	cfg Config

	evaluator *BaselineEvaluator
	optimizer *PromptOptimizer
	gates     *AcceptanceGates
	auditer   *Auditer
}

// New builds and returns a Pipeline.
func New(cfg Config) *Pipeline {
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 3
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeFakeDeterministic
	}
	targets := buildPromptTargets(cfg.PromptsBaseline, cfg.TargetSurfaceIDs)
	return &Pipeline{
		cfg:       cfg,
		evaluator: NewBaselineEvaluator(cfg.Mode, cfg.RandomSeed, cfg.PromptsBaseline),
		optimizer: NewPromptOptimizer(cfg.Mode, cfg.RandomSeed, targets),
		gates:     NewAcceptanceGates(cfg.GateConfig),
		auditer:   NewAuditer(cfg.OutputDir),
	}
}

func buildPromptTargets(baseline map[string]string, targetIDs []string) []PromptTarget {
	descriptions := map[string]string{
		"system_prompt":     "Top-level system prompt injected into every LLM call.",
		"agent_instruction": "General agent behavior / decision policy.",
		"router_prompt":     "Router decision rules when multi-agent routing is used.",
		"tool_desc_calc":    "Tool description for the calculator skill (used for tool-arg optimization).",
	}
	if len(targetIDs) == 0 {
		for id := range baseline {
			targetIDs = append(targetIDs, id)
		}
	}
	targets := make([]PromptTarget, 0, len(targetIDs))
	for _, id := range targetIDs {
		text := baseline[id]
		desc := descriptions[id]
		if desc == "" {
			desc = "user-provided surface " + id
		}
		targets = append(targets, PromptTarget{
			SurfaceID:    id,
			Description:  desc,
			BaselineText: text,
		})
	}
	return targets
}

// Run executes the full closed loop and returns the final report along with
// the paths where JSON and Markdown reports have been written.
func (p *Pipeline) Run(ctx context.Context) (*OptimizationReport, string, string, error) {
	startedAt := time.Now()
	report := &OptimizationReport{
		AppName:         p.cfg.AppName,
		PipelineVersion: PipelineVersion,
		Mode:            string(p.cfg.Mode),
		StartedAt:       startedAt,
		RandomSeed:      p.cfg.RandomSeed,
		GateConfig:      p.cfg.GateConfig,
		PromptTargets:   buildPromptTargets(p.cfg.PromptsBaseline, p.cfg.TargetSurfaceIDs),
		Rounds:          make([]RoundRecord, 0, p.cfg.MaxRounds),
		PromptsFinal:    maps.Clone(p.cfg.PromptsBaseline),
		BaselineTrain:   nil,
		BaselineVal:     nil,
	}

	// Phase 1: Baseline eval (train + validation).
	var err error
	report.BaselineTrain, err = p.evaluator.EvaluateSet(ctx, p.cfg.TrainSetID, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("baseline train evaluation: %w", err)
	}
	report.BaselineVal, err = p.evaluator.EvaluateSet(ctx, p.cfg.ValSetID, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("baseline validation evaluation: %w", err)
	}

	currentPrompts := maps.Clone(p.cfg.PromptsBaseline)
	currentValScore := 0.0
	if report.BaselineVal != nil {
		currentValScore = report.BaselineVal.OverallScore
	}
	report.FinalValidationScore = currentValScore
	report.BestValidationScore = currentValScore
	report.BestRound = 0

	totalCost := CostEstimate{}

	// Phase 2: Multi-round optimization loop.
	lastAccepted := false
	for round := 1; round <= p.cfg.MaxRounds; round++ {
		roundStart := time.Now()
		roundSeed := p.cfg.RandomSeed + int64(round*31)
		roundRec := RoundRecord{
			Round:         round,
			Timestamp:     roundStart,
			RandomSeed:    roundSeed,
			PromptsBefore: maps.Clone(currentPrompts),
		}

		// 2a. Evaluate train set with current prompts (baseline on round 1
		// uses same prompts; this mirrors real PromptIter where each round
		// re-runs train to extract losses from the *current* accepted profile).
		trainSummary, err := p.evaluator.EvaluateSet(ctx, p.cfg.TrainSetID, nil)
		if err != nil {
			return nil, "", "", fmt.Errorf("round %d train eval: %w", round, err)
		}
		roundRec.TrainSummary = trainSummary
		roundRec.ValBaseline = report.BaselineVal

		// 2b. Attribute failures.
		roundRec.Attributions = AttributeFailures(trainSummary)

		// 2c. Propose candidate via PromptIter simulator.
		candidate, err := p.optimizer.ProposeCandidate(
			ctx, round, trainSummary, roundRec.Attributions, currentPrompts)
		if err != nil {
			return nil, "", "", fmt.Errorf("round %d optimizer propose: %w", round, err)
		}
		roundRec.Candidate = candidate

		// 2d. Re-run validation with candidate prompt applied.
		valCandidate, err := p.evaluator.EvaluateSet(ctx, p.cfg.ValSetID, candidate)
		if err != nil {
			return nil, "", "", fmt.Errorf("round %d validation candidate eval: %w", round, err)
		}
		roundRec.ValCandidate = valCandidate

		// 2e. Compute case delta.
		deltas, newHardFail, keyCaseDegrade, err := ComputeCaseDelta(report.BaselineVal, valCandidate)
		if err != nil {
			return nil, "", "", fmt.Errorf("round %d compute delta: %w", round, err)
		}
		roundRec.PerCaseDelta = deltas

		// 2f. Acceptance gates.
		candScore := 0.0
		if valCandidate != nil {
			candScore = valCandidate.OverallScore
		}
		cost := estimateRoundCost(round)
		roundRec.Cost = cost
		totalCost.TotalTokens += cost.TotalTokens
		totalCost.EstimatedCostUSD += cost.EstimatedCostUSD
		totalCost.WallClockDuration += time.Since(roundStart)
		roundRec.Cost.WallClockDuration = time.Since(roundStart)

		decision := p.gates.Evaluate(
			currentValScore, candScore,
			deltas, newHardFail, keyCaseDegrade, cost)
		roundRec.Acceptance = decision

		// 2g. Commit candidate on acceptance.
		nextPrompts := currentPrompts
		if decision.Accepted {
			nextPrompts = maps.Clone(currentPrompts)
			for k, v := range candidate.Patches {
				nextPrompts[k] = v
			}
			currentPrompts = nextPrompts
			currentValScore = candScore
			lastAccepted = true
			if candScore > report.BestValidationScore {
				report.BestValidationScore = candScore
				report.BestRound = round
			}
			report.Notes = append(report.Notes,
				fmt.Sprintf("round %d: accepted candidate %s (Δ=%+.4f)", round, candidate.CandidateID, decision.ScoreDelta))
		} else {
			report.Notes = append(report.Notes,
				fmt.Sprintf("round %d: rejected candidate %s (Δ=%+.4f); reason: %s",
					round, candidate.CandidateID, decision.ScoreDelta, firstRejectReason(decision)))
		}
		roundRec.PromptsAfter = maps.Clone(nextPrompts)
		report.Rounds = append(report.Rounds, roundRec)

		// Stop if we reached target score.
		if candScore >= 0.9999 {
			report.Notes = append(report.Notes, fmt.Sprintf("round %d: stop because validation score hit %.4f", round, candScore))
			break
		}
	}

	report.FinalAccepted = lastAccepted
	report.FinalValidationScore = currentValScore
	report.PromptsFinal = maps.Clone(currentPrompts)
	report.FinishedAt = time.Now()
	totalCost.WallClockDuration = report.FinishedAt.Sub(report.StartedAt)
	report.TotalCost = totalCost

	// Phase 3: Audit persistence.
	jsonPath, err := p.auditer.WriteJSONReport(report)
	if err != nil {
		return report, "", "", fmt.Errorf("write json report: %w", err)
	}
	mdPath, err := p.auditer.WriteMarkdownReport(report)
	if err != nil {
		return report, jsonPath, "", fmt.Errorf("write md report: %w", err)
	}
	return report, jsonPath, mdPath, nil
}

func firstRejectReason(d *AcceptanceDecision) string {
	for _, r := range d.Reasons {
		if contains(r, "REJECT") {
			return r
		}
	}
	if len(d.Reasons) > 0 {
		return d.Reasons[0]
	}
	return "unknown"
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && search(s, sub))
}

func search(s, sub string) bool {
	n, m := len(s), len(sub)
	if m == 0 {
		return true
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return true
		}
	}
	return false
}

// estimateRoundCost produces a synthetic cost estimate that scales with
// the round number so the pipeline can exercise the MaxCostBudget gate.
func estimateRoundCost(round int) CostEstimate {
	infTokens := 4_000 + round*500
	judgeTokens := 2_000 + round*200
	optimTokens := 3_000 + round*800
	total := infTokens + judgeTokens + optimTokens
	// $3 per 1M tokens as a synthetic blend.
	cost := (float64(total) / 1_000_000.0) * 3.0
	return CostEstimate{
		InferenceTokens:   infTokens,
		JudgeTokens:       judgeTokens,
		OptimizerTokens:   optimTokens,
		TotalTokens:       total,
		EstimatedCostUSD:  cost,
		WallClockDuration: 0,
	}
}
