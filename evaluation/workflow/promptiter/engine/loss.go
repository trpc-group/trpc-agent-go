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
				if strings.TrimSpace(metric.StepID) == "" {
					return nil, fmt.Errorf(
						"metric %q for eval case %q is missing step id",
						metric.MetricName,
						evalCase.EvalCaseID,
					)
				}
				if !isKnownLossSeverity(metric.Severity) {
					return nil, fmt.Errorf(
						"metric %q for eval case %q is missing valid severity",
						metric.MetricName,
						evalCase.EvalCaseID,
					)
				}
				if strings.TrimSpace(metric.Reason) == "" {
					return nil, fmt.Errorf(
						"metric %q for eval case %q is missing loss reason",
						metric.MetricName,
						evalCase.EvalCaseID,
					)
				}
				caseLoss.TerminalLosses = append(caseLoss.TerminalLosses, promptiter.TerminalLoss{
					EvalSetID:  evalCase.EvalSetID,
					EvalCaseID: evalCase.EvalCaseID,
					MetricName: metric.MetricName,
					Severity:   metric.Severity,
					StepID:     metric.StepID,
					Loss:       strings.TrimSpace(metric.Reason),
				})
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

func isKnownLossSeverity(severity promptiter.LossSeverity) bool {
	switch severity {
	case promptiter.LossSeverityP0,
		promptiter.LossSeverityP1,
		promptiter.LossSeverityP2,
		promptiter.LossSeverityP3:
		return true
	default:
		return false
	}
}
