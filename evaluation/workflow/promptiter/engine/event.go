//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"context"
	"fmt"
)

// EventKind identifies one observable PromptIter runtime event.
type EventKind string

const (
	// EventKindStructureSnapshot stores the structure snapshot used by the run.
	EventKindStructureSnapshot EventKind = "structure_snapshot"
	// EventKindBaselineValidation stores the accepted baseline validation result.
	EventKindBaselineValidation EventKind = "baseline_validation"
	// EventKindRoundStarted stores the start marker for one optimization round.
	EventKindRoundStarted EventKind = "round_started"
	// EventKindRoundTrainEvaluation stores the train evaluation result for one round.
	EventKindRoundTrainEvaluation EventKind = "round_train_evaluation"
	// EventKindRoundLosses stores extracted terminal losses for one round.
	EventKindRoundLosses EventKind = "round_losses"
	// EventKindRoundBackward stores backward outputs for one round.
	EventKindRoundBackward EventKind = "round_backward"
	// EventKindRoundAggregation stores aggregation outputs for one round.
	EventKindRoundAggregation EventKind = "round_aggregation"
	// EventKindRoundPatchSet stores optimizer patches for one round.
	EventKindRoundPatchSet EventKind = "round_patch_set"
	// EventKindRoundOutputProfile stores the candidate profile created after patch application.
	EventKindRoundOutputProfile EventKind = "round_output_profile"
	// EventKindRoundValidation stores the validation evaluation result for one round.
	EventKindRoundValidation EventKind = "round_validation"
	// EventKindRoundCompleted stores the round summary after acceptance and stop checks.
	EventKindRoundCompleted EventKind = "round_completed"
)

// Event stores one observable PromptIter runtime event.
type Event struct {
	// Kind identifies which runtime stage emitted this event.
	Kind EventKind
	// Round stores the one-based optimization round, or zero for run-level events.
	Round int
	// Payload stores the event payload for this runtime stage.
	Payload any
}

// Observer receives runtime PromptIter events emitted during one run.
type Observer func(ctx context.Context, event *Event) error

// RoundCompleted stores the round-level acceptance and stop summary.
type RoundCompleted struct {
	// Accepted indicates whether this round patch was accepted.
	Accepted bool
	// AcceptanceReason stores the acceptance decision rationale.
	AcceptanceReason string
	// ScoreDelta stores the validation score delta against the accepted baseline.
	ScoreDelta float64
	// ShouldStop indicates whether this round triggered stop conditions.
	ShouldStop bool
	// StopReason stores the stop decision rationale.
	StopReason string
}

func appendRunEvent(ctx context.Context, observer Observer, kind EventKind, round int, payload any) error {
	if observer == nil {
		return nil
	}
	if err := observer(ctx, &Event{
		Kind:    kind,
		Round:   round,
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("append run event %q: %w", kind, err)
	}
	return nil
}
