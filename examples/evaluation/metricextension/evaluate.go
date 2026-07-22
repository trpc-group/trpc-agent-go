//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type responsePolicyEvaluator struct{}

func (responsePolicyEvaluator) Name() string {
	return responsePolicyEvaluatorName
}

func (responsePolicyEvaluator) Description() string {
	return "Evaluates final responses using response policy settings from metric extension."
}

func (responsePolicyEvaluator) Evaluate(_ context.Context, actuals, _ []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	extension, ok := evalMetric.Extension.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("extension must be an object")
	}
	requiredPhrase, ok := extension["requiredPhrase"].(string)
	if !ok || requiredPhrase == "" {
		return nil, fmt.Errorf("extension.requiredPhrase must be a non-empty string")
	}
	requiredPhrase = strings.ToLower(requiredPhrase)
	results := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	overallScore := 0.0
	for _, actual := range actuals {
		content := ""
		if actual != nil && actual.FinalResponse != nil {
			content = actual.FinalResponse.Content
		}
		score := 0.0
		if strings.Contains(strings.ToLower(content), requiredPhrase) {
			score = 1
		}
		invocationStatus := status.EvalStatusFailed
		if score >= evalMetric.Threshold {
			invocationStatus = status.EvalStatusPassed
		}
		results = append(results, &evaluator.PerInvocationResult{
			Score:  score,
			Status: invocationStatus,
		})
		overallScore += score
	}
	if len(results) > 0 {
		overallScore = overallScore / float64(len(results))
	}
	overallStatus := status.EvalStatusFailed
	if overallScore >= evalMetric.Threshold {
		overallStatus = status.EvalStatusPassed
	}
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        overallStatus,
		PerInvocationResults: results,
	}, nil
}
