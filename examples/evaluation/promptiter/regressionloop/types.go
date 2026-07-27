//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type metricResult struct {
	Name      string            `json:"name"`
	Score     float64           `json:"score"`
	Threshold float64           `json:"threshold"`
	Status    status.EvalStatus `json:"status"`
	Reason    string            `json:"reason,omitempty"`
}

type caseResult struct {
	EvalSetID      string                                      `json:"evalSetId"`
	EvalCaseID     string                                      `json:"evalCaseId"`
	Status         status.EvalStatus                           `json:"status"`
	Score          float64                                     `json:"score"`
	ExecutionError string                                      `json:"executionError,omitempty"`
	Metrics        []metricResult                              `json:"metrics"`
	MetricEvidence []*evalresult.EvalMetricResultPerInvocation `json:"-"`
	RunDetails     []*evaluation.EvaluationCaseRunDetails      `json:"-"`
}

type evaluationSnapshot struct {
	EvalSetID string            `json:"evalSetId"`
	Status    status.EvalStatus `json:"status"`
	Score     float64           `json:"score"`
	Cases     []caseResult      `json:"cases"`
	Duration  time.Duration     `json:"duration"`
}

func (s evaluationSnapshot) index() map[resultKey]metricResult {
	result := make(map[resultKey]metricResult)
	for _, evalCase := range s.Cases {
		for _, metric := range evalCase.Metrics {
			result[resultKey{
				EvalSetID:  evalCase.EvalSetID,
				EvalCaseID: evalCase.EvalCaseID,
				MetricName: metric.Name,
			}] = metric
		}
	}
	return result
}
