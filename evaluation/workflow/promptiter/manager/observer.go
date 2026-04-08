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

func (o *observer) append(_ context.Context, event *engine.Event) error {
	if event == nil {
		return errors.New("promptiter event is nil")
	}
	switch event.Kind {
	case engine.EventKindStructureSnapshot:
		payload, ok := event.Payload.(*astructure.Snapshot)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		o.run.Structure = payload
	case engine.EventKindBaselineValidation:
		payload, ok := event.Payload.(*engine.EvaluationResult)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		o.run.BaselineValidation = payload
	case engine.EventKindRoundStarted:
		o.run.CurrentRound = event.Round
		o.ensureRound(event.Round)
	case engine.EventKindRoundTrainEvaluation:
		payload, ok := event.Payload.(*engine.EvaluationResult)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.Train = payload
	case engine.EventKindRoundLosses:
		payload, ok := event.Payload.([]promptiter.CaseLoss)
		if !ok {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.Losses = append([]promptiter.CaseLoss(nil), payload...)
	case engine.EventKindRoundBackward:
		payload, ok := event.Payload.(*engine.BackwardResult)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.Backward = payload
	case engine.EventKindRoundAggregation:
		payload, ok := event.Payload.(*engine.AggregationResult)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.Aggregation = payload
	case engine.EventKindRoundPatchSet:
		payload, ok := event.Payload.(*promptiter.PatchSet)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.Patches = payload
	case engine.EventKindRoundOutputProfile:
		payload, ok := event.Payload.(*promptiter.Profile)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.OutputProfile = payload
	case engine.EventKindRoundValidation:
		payload, ok := event.Payload.(*engine.EvaluationResult)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
		}
		round := o.ensureRound(event.Round)
		round.Validation = payload
	case engine.EventKindRoundCompleted:
		payload, ok := event.Payload.(*engine.RoundCompleted)
		if !ok || payload == nil {
			return fmt.Errorf("event %q payload is invalid", event.Kind)
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
	default:
		return fmt.Errorf("promptiter event kind %q is unsupported", event.Kind)
	}
	return o.manager.store.Update(context.Background(), o.run)
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
