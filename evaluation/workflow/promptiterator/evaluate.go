//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiterator

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
)

func (w *promptIterator) evaluateRound(ctx context.Context, idx int, promptText string, evalSetIDs []string, callOpts *options) (*Round, error) {
	allPassed := true
	rawIssues := make([]issue.IssueRecord, 0)
	evalResults := make(map[string]*evaluation.EvaluationResult, len(evalSetIDs))
	// Build runner options for candidate inference in this round.
	runOpts := make([]agent.RunOption, 0, len(callOpts.runOptions)+1)
	runOpts = append(runOpts, callOpts.runOptions...)
	runOpts = append(runOpts, agent.WithInstruction(promptText))
	// Evaluate all requested eval sets and extract issues.
	firstCaseError := ""
	for _, evalSetID := range evalSetIDs {
		res, err := w.agentEvaluator.Evaluate(
			ctx,
			evalSetID,
			evaluation.WithEvalSetManager(callOpts.evalSetManager),
			evaluation.WithMetricManager(callOpts.metricManager),
			evaluation.WithRegistry(callOpts.registry),
			evaluation.WithExpectedRunner(callOpts.expectedRunner),
			evaluation.WithRunOptions(runOpts...),
		)
		if err != nil {
			return nil, fmt.Errorf("evaluate %s: %w", evalSetID, err)
		}
		if res == nil || res.EvalResult == nil {
			return nil, fmt.Errorf("evaluation result for %s is nil", evalSetID)
		}
		evalResults[evalSetID] = res
		if res.OverallStatus != status.EvalStatusPassed {
			allPassed = false
		}
		for _, cr := range res.EvalResult.EvalCaseResults {
			if cr == nil {
				continue
			}
			if firstCaseError == "" && cr.ErrorMessage != "" {
				firstCaseError = fmt.Sprintf("%s/%s: %s", evalSetID, cr.EvalID, cr.ErrorMessage)
			}
			rawIssues = append(rawIssues, callOpts.issueExtractor(evalSetID, cr)...)
		}
	}
	if firstCaseError != "" {
		return nil, fmt.Errorf("evaluation has case-level errors: %s", firstCaseError)
	}
	return &Round{
		Index:       idx,
		Prompt:      promptText,
		EvalResults: evalResults,
		Passed:      allPassed,
		Issues:      rawIssues,
	}, nil
}
