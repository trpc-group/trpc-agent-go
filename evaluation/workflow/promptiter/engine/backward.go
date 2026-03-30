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
	"errors"
	"fmt"
	"sort"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	iloss "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/loss"
)

// CaseBackwardResult stores all step gradients produced for one eval case.
type CaseBackwardResult struct {
	// EvalSetID identifies the source evaluation set.
	EvalSetID string
	// EvalCaseID identifies the source evaluation case.
	EvalCaseID string
	// StepGradients stores gradients per step in topological order.
	StepGradients []promptiter.StepGradient
}

// BackwardResult stores aggregated backward outputs for every case in this round.
type BackwardResult struct {
	// Cases stores per-case backward outputs for downstream aggregation.
	Cases []CaseBackwardResult
}

func (e *engine) backward(
	ctx context.Context,
	structure *structureState,
	profile *promptiter.Profile,
	train *EvaluationResult,
	losses []promptiter.CaseLoss,
) (*BackwardResult, error) {
	if e.backwarder == nil {
		return nil, errors.New("backwarder is nil")
	}
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	caseIndex := indexCaseResults(train)
	overrideIndex := buildOverrideIndex(profile)
	result := &BackwardResult{
		Cases: make([]CaseBackwardResult, 0, len(losses)),
	}
	for _, caseLoss := range losses {
		caseKey := caseResultKey{evalSetID: caseLoss.EvalSetID, evalCaseID: caseLoss.EvalCaseID}
		evalCase, ok := caseIndex[caseKey]
		if !ok {
			return nil, fmt.Errorf(
				"eval case %q from eval set %q is missing from training result",
				caseLoss.EvalCaseID,
				caseLoss.EvalSetID,
			)
		}
		caseResult, err := e.backwardCase(ctx, structure, overrideIndex, evalCase, caseLoss)
		if err != nil {
			return nil, err
		}
		if caseResult == nil {
			continue
		}
		result.Cases = append(result.Cases, *caseResult)
	}
	sort.SliceStable(result.Cases, func(i, j int) bool {
		if result.Cases[i].EvalSetID != result.Cases[j].EvalSetID {
			return result.Cases[i].EvalSetID < result.Cases[j].EvalSetID
		}
		return result.Cases[i].EvalCaseID < result.Cases[j].EvalCaseID
	})
	return result, nil
}

type caseResultKey struct {
	evalSetID  string
	evalCaseID string
}

func indexCaseResults(result *EvaluationResult) map[caseResultKey]CaseResult {
	index := make(map[caseResultKey]CaseResult)
	if result == nil {
		return index
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			index[caseResultKey{
				evalSetID:  evalCase.EvalSetID,
				evalCaseID: evalCase.EvalCaseID,
			}] = evalCase
		}
	}
	return index
}

func (e *engine) backwardCase(
	ctx context.Context,
	structure *structureState,
	overrideIndex map[string]promptiter.SurfaceOverride,
	evalCase CaseResult,
	caseLoss promptiter.CaseLoss,
) (*CaseBackwardResult, error) {
	if evalCase.Trace == nil {
		return nil, fmt.Errorf(
			"trace is nil for eval case %q in eval set %q",
			evalCase.EvalCaseID,
			evalCase.EvalSetID,
		)
	}
	stepIndex, err := indexTraceSteps(structure, evalCase.Trace)
	if err != nil {
		return nil, fmt.Errorf(
			"index trace steps for eval case %q in eval set %q: %w",
			evalCase.EvalCaseID,
			evalCase.EvalSetID,
			err,
		)
	}
	inbox := make(map[string][]backwarder.GradientPacket, len(evalCase.Trace.Steps))
	for _, terminalLoss := range caseLoss.TerminalLosses {
		if _, ok := stepIndex[terminalLoss.StepID]; !ok {
			return nil, fmt.Errorf(
				"terminal loss step id %q is not part of trace for eval case %q",
				terminalLoss.StepID,
				evalCase.EvalCaseID,
			)
		}
		inbox[terminalLoss.StepID] = append(inbox[terminalLoss.StepID], backwarder.GradientPacket{
			FromStepID: terminalLoss.StepID,
			Severity:   terminalLoss.Severity,
			Gradient:   terminalLoss.Loss,
		})
	}
	caseResult := &CaseBackwardResult{
		EvalSetID:     caseLoss.EvalSetID,
		EvalCaseID:    caseLoss.EvalCaseID,
		StepGradients: make([]promptiter.StepGradient, 0),
	}
	for stepIndexInTrace := len(evalCase.Trace.Steps) - 1; stepIndexInTrace >= 0; stepIndexInTrace-- {
		step := evalCase.Trace.Steps[stepIndexInTrace]
		incoming := normalizeIncomingPackets(inbox[step.StepID])
		if len(incoming) == 0 {
			continue
		}
		request, err := buildBackwardRequest(structure, overrideIndex, stepIndex, evalCase, step, incoming)
		if err != nil {
			return nil, fmt.Errorf(
				"build backward request for eval case %q step %q: %w",
				evalCase.EvalCaseID,
				step.StepID,
				err,
			)
		}
		response, err := e.backwarder.Backward(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"backward eval case %q step %q: %w",
				evalCase.EvalCaseID,
				step.StepID,
				err,
			)
		}
		if len(response.Gradients) > 0 {
			caseResult.StepGradients = append(caseResult.StepGradients, promptiter.StepGradient{
				StepID:    step.StepID,
				NodeID:    step.NodeID,
				Gradients: append([]promptiter.SurfaceGradient(nil), response.Gradients...),
			})
		}
		for _, propagation := range response.Upstream {
			inbox[propagation.PredecessorStepID] = append(
				inbox[propagation.PredecessorStepID],
				append([]backwarder.GradientPacket(nil), propagation.Gradients...)...,
			)
		}
	}
	sort.SliceStable(caseResult.StepGradients, func(i, j int) bool {
		leftOrder := stepIndex[caseResult.StepGradients[i].StepID].order
		rightOrder := stepIndex[caseResult.StepGradients[j].StepID].order
		return leftOrder < rightOrder
	})
	return caseResult, nil
}

type indexedTraceStep struct {
	step  atrace.Step
	order int
}

func indexTraceSteps(
	structure *structureState,
	trace *atrace.Trace,
) (map[string]indexedTraceStep, error) {
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	if trace == nil {
		return nil, errors.New("trace is nil")
	}
	index := make(map[string]indexedTraceStep, len(trace.Steps))
	for i, step := range trace.Steps {
		if step.StepID == "" {
			return nil, errors.New("trace step id is empty")
		}
		if step.NodeID == "" {
			return nil, fmt.Errorf("trace step %q node id is empty", step.StepID)
		}
		if _, ok := structure.nodeIndex[step.NodeID]; !ok {
			return nil, fmt.Errorf("trace step %q references unknown node id %q", step.StepID, step.NodeID)
		}
		if _, ok := index[step.StepID]; ok {
			return nil, fmt.Errorf("duplicate trace step id %q", step.StepID)
		}
		for _, predecessorStepID := range step.PredecessorStepIDs {
			if predecessorStepID == "" {
				return nil, fmt.Errorf("trace step %q predecessor step id is empty", step.StepID)
			}
			if _, ok := index[predecessorStepID]; !ok {
				return nil, fmt.Errorf(
					"trace step %q references missing or out-of-order predecessor step id %q",
					step.StepID,
					predecessorStepID,
				)
			}
		}
		index[step.StepID] = indexedTraceStep{step: step, order: i}
	}
	return index, nil
}

func normalizeIncomingPackets(packets []backwarder.GradientPacket) []backwarder.GradientPacket {
	normalized := make([]backwarder.GradientPacket, 0, len(packets))
	for _, packet := range packets {
		if strings.TrimSpace(packet.Gradient) == "" {
			continue
		}
		normalized = append(normalized, packet)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		leftRank := iloss.SeverityRank(normalized[i].Severity)
		rightRank := iloss.SeverityRank(normalized[j].Severity)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if normalized[i].FromStepID != normalized[j].FromStepID {
			return normalized[i].FromStepID < normalized[j].FromStepID
		}
		return normalized[i].Gradient < normalized[j].Gradient
	})
	return normalized
}

func buildBackwardRequest(
	structure *structureState,
	overrideIndex map[string]promptiter.SurfaceOverride,
	traceIndex map[string]indexedTraceStep,
	evalCase CaseResult,
	step atrace.Step,
	incoming []backwarder.GradientPacket,
) (*backwarder.Request, error) {
	node, ok := structure.nodeIndex[step.NodeID]
	if !ok {
		return nil, fmt.Errorf("step %q references unknown node id %q", step.StepID, step.NodeID)
	}
	surfaces := make([]astructure.Surface, 0, len(step.AppliedSurfaceIDs))
	seenSurfaces := make(map[string]struct{}, len(step.AppliedSurfaceIDs))
	for _, surfaceID := range step.AppliedSurfaceIDs {
		if surfaceID == "" {
			return nil, fmt.Errorf("step %q applied surface id is empty", step.StepID)
		}
		if _, ok := seenSurfaces[surfaceID]; ok {
			continue
		}
		seenSurfaces[surfaceID] = struct{}{}
		surface, err := resolveProfileSurface(structure, overrideIndex, surfaceID)
		if err != nil {
			return nil, err
		}
		surfaces = append(surfaces, surface)
	}
	predecessors := make([]backwarder.Predecessor, 0, len(step.PredecessorStepIDs))
	for _, predecessorStepID := range step.PredecessorStepIDs {
		predecessor, ok := traceIndex[predecessorStepID]
		if !ok {
			return nil, fmt.Errorf("step %q predecessor step id %q is unknown", step.StepID, predecessorStepID)
		}
		predecessors = append(predecessors, backwarder.Predecessor{
			StepID: predecessor.step.StepID,
			NodeID: predecessor.step.NodeID,
			Output: cloneTraceSnapshot(predecessor.step.Output),
			Error:  predecessor.step.Error,
		})
	}
	input := cloneTraceSnapshot(step.Input)
	if input == nil {
		input = &atrace.Snapshot{}
	}
	return &backwarder.Request{
		EvalSetID:    evalCase.EvalSetID,
		EvalCaseID:   evalCase.EvalCaseID,
		Node:         &node,
		StepID:       step.StepID,
		Input:        input,
		Output:       cloneTraceSnapshot(step.Output),
		Error:        step.Error,
		Surfaces:     surfaces,
		Predecessors: predecessors,
		Incoming:     incoming,
	}, nil
}

func cloneTraceSnapshot(snapshot *atrace.Snapshot) *atrace.Snapshot {
	if snapshot == nil {
		return nil
	}
	return &atrace.Snapshot{Text: snapshot.Text}
}
