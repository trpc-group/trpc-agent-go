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
	"fmt"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("Evaluation completed with case aggregation")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	if result.EvalResult != nil && result.EvalResult.EvalSetResultID != "" {
		fmt.Printf("Saved Result: %s\n", filepath.Join(outDir, appName, result.EvalResult.EvalSetResultID+".evalset_result.json"))
	}
	for _, caseSummary := range result.EvalCases {
		for _, caseResult := range caseSummary.EvalCaseResults {
			fmt.Printf("Case %s run %d -> %s (case score %.2f)\n",
				caseResult.EvalID,
				caseResult.RunID,
				caseResult.FinalEvalStatus,
				caseResult.Score,
			)
			if response := actualFinalResponse(caseResult); response != "" {
				fmt.Printf("  Actual Response: %s\n", response)
			}
			for _, metricResult := range caseResult.OverallEvalMetricResults {
				fmt.Printf("  Metric %s: score %.2f (threshold %.2f) => %s\n",
					metricResult.MetricName,
					metricResult.Score,
					metricResult.Threshold,
					metricResult.EvalStatus,
				)
			}
			fmt.Println()
		}
	}
}

func actualFinalResponse(caseResult *evalresult.EvalCaseResult) string {
	if caseResult == nil || len(caseResult.EvalMetricResultPerInvocation) == 0 {
		return ""
	}
	invocationResult := caseResult.EvalMetricResultPerInvocation[0]
	if invocationResult == nil || invocationResult.ActualInvocation == nil ||
		invocationResult.ActualInvocation.FinalResponse == nil {
		return ""
	}
	return invocationResult.ActualInvocation.FinalResponse.Content
}
