//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

// MetricScore is one normalized metric measurement for a single eval case.
type MetricScore struct {
	MetricName string
	Score      float64
	Passed     bool
	Status     string
	Reason     string
}

// CaseScore is one normalized per-case evaluation result.
type CaseScore struct {
	EvalSetID  string
	EvalCaseID string
	Passed     bool
	Score      float64
	Metrics    []MetricScore
}

// EvalResult is the normalized evaluation result used by the pipeline stages.
type EvalResult struct {
	OverallScore float64
	Cases        []CaseScore
}

// caseByID returns the case score for the given eval case id, or nil.
func (r *EvalResult) caseByID(evalCaseID string) *CaseScore {
	if r == nil {
		return nil
	}
	for i := range r.Cases {
		if r.Cases[i].EvalCaseID == evalCaseID {
			return &r.Cases[i]
		}
	}
	return nil
}
