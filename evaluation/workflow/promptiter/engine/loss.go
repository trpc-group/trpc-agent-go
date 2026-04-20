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
	"errors"
	"fmt"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func (e *engine) loss(result *EvaluationResult) ([]promptiter.CaseLoss, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	losses := make([]promptiter.CaseLoss, 0)
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			if evalCase.Trace != nil && evalCase.Trace.Status == atrace.TraceStatusIncomplete {
				continue
			}
			caseLoss := promptiter.CaseLoss{
				EvalSetID:      evalCase.EvalSetID,
				EvalCaseID:     evalCase.EvalCaseID,
				TerminalLosses: make([]promptiter.TerminalLoss, 0),
			}
			for _, metric := range evalCase.Metrics {
				if metric.Status != status.EvalStatusFailed {
					continue
				}
				if strings.TrimSpace(metric.Reason) == "" {
					return nil, fmt.Errorf(
						"metric %q for eval case %q is missing loss reason",
						metric.MetricName,
						evalCase.EvalCaseID,
					)
				}
				terminalStepIDs, err := traceTerminalStepIDs(evalCase.Trace)
				if err != nil {
					return nil, fmt.Errorf(
						"resolve terminal step for eval case %q: %w",
						evalCase.EvalCaseID,
						err,
					)
				}
				for _, terminalStepID := range terminalStepIDs {
					caseLoss.TerminalLosses = append(caseLoss.TerminalLosses, promptiter.TerminalLoss{
						EvalSetID:  evalCase.EvalSetID,
						EvalCaseID: evalCase.EvalCaseID,
						MetricName: metric.MetricName,
						StepID:     terminalStepID,
						Loss:       strings.TrimSpace(metric.Reason),
					})
				}
			}
			if len(caseLoss.TerminalLosses) == 0 {
				continue
			}
			sort.SliceStable(caseLoss.TerminalLosses, func(i, j int) bool {
				if caseLoss.TerminalLosses[i].MetricName != caseLoss.TerminalLosses[j].MetricName {
					return caseLoss.TerminalLosses[i].MetricName < caseLoss.TerminalLosses[j].MetricName
				}
				if caseLoss.TerminalLosses[i].StepID != caseLoss.TerminalLosses[j].StepID {
					return caseLoss.TerminalLosses[i].StepID < caseLoss.TerminalLosses[j].StepID
				}
				return caseLoss.TerminalLosses[i].Loss < caseLoss.TerminalLosses[j].Loss
			})
			losses = append(losses, caseLoss)
		}
	}
	sort.SliceStable(losses, func(i, j int) bool {
		if losses[i].EvalSetID != losses[j].EvalSetID {
			return losses[i].EvalSetID < losses[j].EvalSetID
		}
		return losses[i].EvalCaseID < losses[j].EvalCaseID
	})
	return losses, nil
}

func traceTerminalStepIDs(trace *atrace.Trace) ([]string, error) {
	if trace == nil {
		return nil, errors.New("execution trace is nil")
	}
	if len(trace.Steps) == 0 {
		return nil, errors.New("execution trace has no steps")
	}
	stepIndex := make(map[string]struct{}, len(trace.Steps))
	stepOrder := make(map[string]int, len(trace.Steps))
	referenced := make(map[string]struct{}, len(trace.Steps))
	for _, step := range trace.Steps {
		if step.StepID == "" {
			return nil, errors.New("execution trace step id is empty")
		}
		if _, ok := stepIndex[step.StepID]; ok {
			return nil, fmt.Errorf("duplicate execution trace step id %q", step.StepID)
		}
		stepIndex[step.StepID] = struct{}{}
		stepOrder[step.StepID] = len(stepOrder)
	}
	for _, step := range trace.Steps {
		for _, predecessorStepID := range step.PredecessorStepIDs {
			if predecessorStepID == "" {
				return nil, fmt.Errorf("execution trace step %q predecessor step id is empty", step.StepID)
			}
			if _, ok := stepIndex[predecessorStepID]; !ok {
				return nil, fmt.Errorf(
					"execution trace step %q references unknown predecessor step id %q",
					step.StepID,
					predecessorStepID,
				)
			}
			referenced[predecessorStepID] = struct{}{}
		}
	}
	terminalStepIDs := make(map[string]struct{})
	for _, step := range trace.Steps {
		if _, ok := referenced[step.StepID]; ok {
			continue
		}
		terminalStepIDs[step.StepID] = struct{}{}
	}
	terminalIDs := sortStepIDs(terminalStepIDs, stepOrder)
	if len(terminalIDs) == 0 {
		return nil, errors.New("execution trace has no terminal step")
	}
	return terminalIDs, nil
}

func sortStepIDs(stepIDs map[string]struct{}, stepOrder map[string]int) []string {
	ids := make([]string, 0, len(stepIDs))
	for stepID := range stepIDs {
		ids = append(ids, stepID)
	}
	sort.Slice(ids, func(i, j int) bool {
		return stepOrder[ids[i]] < stepOrder[ids[j]]
	})
	return ids
}
