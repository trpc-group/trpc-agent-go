//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// RunEvaluationRequest represents the request payload for creating an evaluation run.
type RunEvaluationRequest struct {
	SetID   string `json:"setId,omitempty"`
	NumRuns *int   `json:"numRuns,omitempty"`
}

// ListSetsResponse represents the response payload for listing sets.
type ListSetsResponse struct {
	Sets []*evalset.EvalSet `json:"sets,omitempty"`
}

// GetSetResponse represents the response payload for getting a set.
type GetSetResponse struct {
	Set *evalset.EvalSet `json:"set,omitempty"`
}

// CreateRunResponse represents the response payload for creating a run.
type CreateRunResponse struct {
	EvaluationResult *coreevaluation.EvaluationResult `json:"evaluationResult,omitempty"`
}

// ListResultsResponse represents the response payload for listing results.
type ListResultsResponse struct {
	Results []*evalresult.EvalSetResult `json:"results,omitempty"`
}

// GetResultResponse represents the response payload for getting a result.
type GetResultResponse struct {
	Result *evalresult.EvalSetResult `json:"result,omitempty"`
}
