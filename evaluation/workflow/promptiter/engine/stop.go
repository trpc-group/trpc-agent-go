//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

// StopPolicy defines hard and soft conditions to end the multi-round run.
type StopPolicy struct {
	// MaxRoundsWithoutAcceptance fails the run after this many rounds without acceptance.
	MaxRoundsWithoutAcceptance int
	// TargetScore, when set, stops the run after this score is reached or exceeded.
	TargetScore *float64
}

// StopDecision records whether the engine should terminate and why.
type StopDecision struct {
	// ShouldStop indicates whether execution should continue.
	ShouldStop bool
	// Reason records the stop trigger matched by policy.
	Reason string
}

// stop evaluates current progress against stop policy and returns the decision.
func (e *engine) stop() error {
	return nil
}
