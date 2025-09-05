//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local evaluation service.
package local

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// Service implements the EvaluationService interface for local evaluation
type Service struct {
}

// NewService creates a new local evaluation service
func NewService() *Service {
	return &Service{}
}

// PerformInference performs inference for eval cases and returns results as they become available
func (s *Service) PerformInference(ctx context.Context, request *service.InferenceRequest) (<-chan *service.InferenceResult, error) {
	return nil, nil
}

// Evaluate evaluates inference results and returns eval case results as they become available
func (s *Service) Evaluate(ctx context.Context, request *service.EvaluateRequest) (<-chan *evalresult.EvalCaseResult, error) {
	return nil, nil
}
