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
	iloss "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/loss"
)

type lossHintMetricKey struct {
	evalSetID  string
	evalCaseID string
	metricName string
}

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
			losses = append(losses, caseLoss)
		}
	}
	sortCaseLosses(losses)
	return losses, nil
}

func mergeLossHints(
	losses []promptiter.CaseLoss,
	result *EvaluationResult,
	inputs []EvalSetInput,
) ([]promptiter.CaseLoss, error) {
	hintIndex := indexLossHints(inputs)
	if len(hintIndex) == 0 {
		return losses, nil
	}
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	caseIndex := indexCaseResults(result)
	lossIndex, stepIndex := indexLossHintTargets(losses)
	for caseKey, hints := range hintIndex {
		evalCase, ok := caseIndex[caseKey]
		if !ok {
			return nil, fmt.Errorf(
				"loss hint case %q from eval set %q is missing from training result",
				caseKey.evalCaseID,
				caseKey.evalSetID,
			)
		}
		for _, hint := range hints {
			metric, ok := findMetricResult(evalCase.Metrics, hint.MetricName)
			if !ok {
				return nil, fmt.Errorf(
					"loss hint metric %q for eval case %q from eval set %q is missing from training result",
					hint.MetricName,
					caseKey.evalCaseID,
					caseKey.evalSetID,
				)
			}
			if metric.Status != status.EvalStatusFailed {
				continue
			}
			metricKey := lossHintMetricKey{
				evalSetID:  caseKey.evalSetID,
				evalCaseID: caseKey.evalCaseID,
				metricName: hint.MetricName,
			}
			stepIDs := stepIndex[metricKey]
			if len(stepIDs) == 0 {
				continue
			}
			lossPosition, ok := lossIndex[caseKey]
			if !ok {
				continue
			}
			for _, stepID := range stepIDs {
				losses[lossPosition].TerminalLosses = append(losses[lossPosition].TerminalLosses,
					promptiter.TerminalLoss{
						EvalSetID:  caseKey.evalSetID,
						EvalCaseID: caseKey.evalCaseID,
						MetricName: hint.MetricName,
						Severity:   hint.Severity,
						StepID:     stepID,
						Loss:       hint.Reason,
					})
			}
		}
	}
	sortCaseLosses(losses)
	return losses, nil
}

func indexLossHints(inputs []EvalSetInput) map[caseResultKey][]LossHint {
	index := make(map[caseResultKey][]LossHint)
	for _, input := range inputs {
		for _, hint := range input.LossHints {
			hint.EvalCaseID = strings.TrimSpace(hint.EvalCaseID)
			hint.MetricName = strings.TrimSpace(hint.MetricName)
			hint.Reason = strings.TrimSpace(hint.Reason)
			key := caseResultKey{
				evalSetID:  input.EvalSetID,
				evalCaseID: hint.EvalCaseID,
			}
			index[key] = append(index[key], hint)
		}
	}
	return index
}

func indexLossHintTargets(
	losses []promptiter.CaseLoss,
) (map[caseResultKey]int, map[lossHintMetricKey][]string) {
	lossIndex := make(map[caseResultKey]int, len(losses))
	stepIndex := make(map[lossHintMetricKey][]string)
	seenSteps := make(map[lossHintMetricKey]map[string]struct{})
	for index := range losses {
		caseKey := caseResultKey{
			evalSetID:  losses[index].EvalSetID,
			evalCaseID: losses[index].EvalCaseID,
		}
		lossIndex[caseKey] = index
		for _, terminalLoss := range losses[index].TerminalLosses {
			metricKey := lossHintMetricKey{
				evalSetID:  terminalLoss.EvalSetID,
				evalCaseID: terminalLoss.EvalCaseID,
				metricName: terminalLoss.MetricName,
			}
			if _, ok := seenSteps[metricKey]; !ok {
				seenSteps[metricKey] = make(map[string]struct{})
			}
			if _, ok := seenSteps[metricKey][terminalLoss.StepID]; ok {
				continue
			}
			seenSteps[metricKey][terminalLoss.StepID] = struct{}{}
			stepIndex[metricKey] = append(stepIndex[metricKey], terminalLoss.StepID)
		}
	}
	return lossIndex, stepIndex
}

func findMetricResult(metrics []MetricResult, metricName string) (MetricResult, bool) {
	for _, metric := range metrics {
		if metric.MetricName == metricName {
			return metric, true
		}
	}
	return MetricResult{}, false
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

func sortCaseLosses(losses []promptiter.CaseLoss) {
	for index := range losses {
		sort.SliceStable(losses[index].TerminalLosses, func(i, j int) bool {
			return terminalLossLess(losses[index].TerminalLosses[i], losses[index].TerminalLosses[j])
		})
	}
	sort.SliceStable(losses, func(i, j int) bool {
		if losses[i].EvalSetID != losses[j].EvalSetID {
			return losses[i].EvalSetID < losses[j].EvalSetID
		}
		return losses[i].EvalCaseID < losses[j].EvalCaseID
	})
}

func terminalLossLess(left promptiter.TerminalLoss, right promptiter.TerminalLoss) bool {
	if iloss.SeverityRank(left.Severity) != iloss.SeverityRank(right.Severity) {
		return iloss.SeverityRank(left.Severity) < iloss.SeverityRank(right.Severity)
	}
	if left.MetricName != right.MetricName {
		return left.MetricName < right.MetricName
	}
	if left.StepID != right.StepID {
		return left.StepID < right.StepID
	}
	return left.Loss < right.Loss
}
