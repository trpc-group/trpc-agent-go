//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

// AcceptancePolicy controls whether a generated profile is accepted into next round input.
type AcceptancePolicy struct {
	// MinScoreGain is the minimum score increase required to accept a round patch.
	MinScoreGain float64
}

// AcceptanceDecision records round-level pass/fail outcome and score delta.
type AcceptanceDecision struct {
	// Accepted is true if validation gains satisfy acceptance criteria.
	Accepted bool
	// ScoreDelta is the metric difference compared with previous accepted baseline.
	ScoreDelta float64
	// Reason explains why acceptance succeeded or failed.
	Reason string
}

func (e *engine) accept(
	policy AcceptancePolicy,
	baselineScore float64,
	candidateScore float64,
) *AcceptanceDecision {
	scoreDelta := candidateScore - baselineScore
	decision := &AcceptanceDecision{
		Accepted:   scoreDelta >= policy.MinScoreGain,
		ScoreDelta: scoreDelta,
	}
	if decision.Accepted {
		decision.Reason = "candidate score gain satisfies acceptance policy"
		return decision
	}
	decision.Reason = "candidate score gain does not satisfy acceptance policy"
	return decision
}
