//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package status provides functions to summarize the evaluation status.
package status

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// SummarizeMetricsStatus summarizes the metric statuses into a single value.
func SummarizeMetricsStatus(metrics []*evalresult.EvalMetricResult) (status.EvalStatus, error) {
	evalStatuses := make([]status.EvalStatus, 0, len(metrics))
	for _, evalMetricResult := range metrics {
		if evalMetricResult == nil {
			continue
		}
		evalStatuses = append(evalStatuses, evalMetricResult.EvalStatus)
	}
	return Summarize(evalStatuses)
}

// Summarize summarizes the evaluation status of a single case.
// The precedence rules are:
// 1. If there is a Failed, the overall status is Failed.
// 2. If there is a Passed, the overall status is Passed.
// 3. Otherwise, the overall status is NotEvaluated.
func Summarize(statuses []status.EvalStatus) (status.EvalStatus, error) {
	combined := status.EvalStatusNotEvaluated
	for _, s := range statuses {
		switch s {
		case status.EvalStatusFailed:
			return status.EvalStatusFailed, nil
		case status.EvalStatusPassed:
			combined = status.EvalStatusPassed
		case status.EvalStatusNotEvaluated:
			continue
		default:
			return status.EvalStatusFailed, fmt.Errorf("unexpected eval status %v", s)
		}
	}
	return combined, nil
}
