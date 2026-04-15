//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package manager provides asynchronous PromptIter run lifecycle management on top of the synchronous engine.
package manager

import (
	"context"
	"errors"
	"fmt"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	iprofile "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/profile"
)

type observer struct {
	manager *manager
	run     *engine.RunResult
}

func (o *observer) append(ctx context.Context, event *engine.Event) error {
	if event == nil {
		return errors.New("promptiter event is nil")
	}
	if err := validateEventRound(event); err != nil {
		return err
	}
	if err := o.applyEvent(event); err != nil {
		return err
	}
	return o.manager.store.Update(ctx, o.run)
}

func (o *observer) applyEvent(event *engine.Event) error {
	switch event.Kind {
	case engine.EventKindStructureSnapshot:
		return o.applyStructureSnapshot(event)
	case engine.EventKindBaselineValidation:
		return o.applyBaselineValidation(event)
	case engine.EventKindRoundStarted:
		o.run.CurrentRound = event.Round
		o.ensureRound(event.Round)
	case engine.EventKindRoundTrainEvaluation:
		return o.applyRoundTrainEvaluation(event)
	case engine.EventKindRoundLosses:
		return o.applyRoundLosses(event)
	case engine.EventKindRoundBackward:
		return o.applyRoundBackward(event)
	case engine.EventKindRoundAggregation:
		return o.applyRoundAggregation(event)
	case engine.EventKindRoundPatchSet:
		return o.applyRoundPatchSet(event)
	case engine.EventKindRoundOutputProfile:
		return o.applyRoundOutputProfile(event)
	case engine.EventKindRoundValidation:
		return o.applyRoundValidation(event)
	case engine.EventKindRoundCompleted:
		return o.applyRoundCompleted(event)
	default:
		return fmt.Errorf("promptiter event kind %q is unsupported", event.Kind)
	}
	return nil
}

func (o *observer) applyStructureSnapshot(event *engine.Event) error {
	payload, ok := event.Payload.(*astructure.Snapshot)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	o.run.Structure = payload
	return nil
}

func (o *observer) applyBaselineValidation(event *engine.Event) error {
	payload, ok := event.Payload.(*engine.EvaluationResult)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	o.run.BaselineValidation = payload
	return nil
}

func (o *observer) applyRoundTrainEvaluation(event *engine.Event) error {
	payload, ok := event.Payload.(*engine.EvaluationResult)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Train = payload
	return nil
}

func (o *observer) applyRoundLosses(event *engine.Event) error {
	payload, ok := event.Payload.([]promptiter.CaseLoss)
	if !ok {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Losses = append([]promptiter.CaseLoss(nil), payload...)
	return nil
}

func (o *observer) applyRoundBackward(event *engine.Event) error {
	payload, ok := event.Payload.(*engine.BackwardResult)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Backward = payload
	return nil
}

func (o *observer) applyRoundAggregation(event *engine.Event) error {
	payload, ok := event.Payload.(*engine.AggregationResult)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Aggregation = payload
	return nil
}

func (o *observer) applyRoundPatchSet(event *engine.Event) error {
	payload, ok := event.Payload.(*promptiter.PatchSet)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Patches = payload
	return nil
}

func (o *observer) applyRoundOutputProfile(event *engine.Event) error {
	payload, ok := event.Payload.(*promptiter.Profile)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.OutputProfile = payload
	return nil
}

func (o *observer) applyRoundValidation(event *engine.Event) error {
	payload, ok := event.Payload.(*engine.EvaluationResult)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Validation = payload
	return nil
}

func (o *observer) applyRoundCompleted(event *engine.Event) error {
	payload, ok := event.Payload.(*engine.RoundCompleted)
	if !ok || payload == nil {
		return invalidEventPayloadError(event.Kind)
	}
	round := o.ensureRound(event.Round)
	round.Acceptance = &engine.AcceptanceDecision{
		Accepted:   payload.Accepted,
		ScoreDelta: payload.ScoreDelta,
		Reason:     payload.AcceptanceReason,
	}
	round.Stop = &engine.StopDecision{
		ShouldStop: payload.ShouldStop,
		Reason:     payload.StopReason,
	}
	if payload.Accepted && round.OutputProfile != nil {
		o.run.AcceptedProfile = iprofile.Clone(round.OutputProfile)
	}
	return nil
}

func invalidEventPayloadError(kind engine.EventKind) error {
	return fmt.Errorf("event %q payload is invalid", kind)
}

func validateEventRound(event *engine.Event) error {
	switch event.Kind {
	case engine.EventKindStructureSnapshot, engine.EventKindBaselineValidation:
		if event.Round != 0 {
			return fmt.Errorf("event %q must use round 0, got %d", event.Kind, event.Round)
		}
	case engine.EventKindRoundStarted,
		engine.EventKindRoundTrainEvaluation,
		engine.EventKindRoundLosses,
		engine.EventKindRoundBackward,
		engine.EventKindRoundAggregation,
		engine.EventKindRoundPatchSet,
		engine.EventKindRoundOutputProfile,
		engine.EventKindRoundValidation,
		engine.EventKindRoundCompleted:
		if event.Round <= 0 {
			return fmt.Errorf("event %q must use round >= 1, got %d", event.Kind, event.Round)
		}
	}
	return nil
}

func (o *observer) ensureRound(roundNumber int) *engine.RoundResult {
	for i := range o.run.Rounds {
		if o.run.Rounds[i].Round == roundNumber {
			return &o.run.Rounds[i]
		}
	}
	o.run.Rounds = append(o.run.Rounds, engine.RoundResult{Round: roundNumber})
	return &o.run.Rounds[len(o.run.Rounds)-1]
}
