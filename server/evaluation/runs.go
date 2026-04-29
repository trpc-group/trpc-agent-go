//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"context"
	"errors"
	"net/http"
	"strings"

	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
)

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, http.MethodPost)
		s.respondJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	s.handleCreateRun(w, r)
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	req, err := s.decodeRunEvaluationRequest(w, r)
	if err != nil {
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	result, err := s.runEvaluation(ctx, req)
	if err != nil {
		s.respondStatusError(w, r, err)
		return
	}
	s.respondJSON(w, r, http.StatusCreated, &CreateRunResponse{
		EvaluationResult: result,
	})
}

func (s *Server) runEvaluation(ctx context.Context, req *RunEvaluationRequest) (*coreevaluation.EvaluationResult, error) {
	evalOpts := make([]coreevaluation.Option, 0, 1)
	if req.NumRuns != nil {
		evalOpts = append(evalOpts, coreevaluation.WithNumRuns(*req.NumRuns))
	}
	return s.agentEvaluator.Evaluate(ctx, req.SetID, evalOpts...)
}

func (s *Server) decodeRunEvaluationRequest(w http.ResponseWriter, r *http.Request) (*RunEvaluationRequest, error) {
	var req RunEvaluationRequest
	if err := s.decodeJSONRequestBody(w, r, &req); err != nil {
		return nil, err
	}
	if err := validateRunEvaluationRequest(&req); err != nil {
		s.respondJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, err
	}
	return &req, nil
}

func validateRunEvaluationRequest(req *RunEvaluationRequest) error {
	if req == nil {
		return errors.New("request must not be nil")
	}
	req.SetID = strings.TrimSpace(req.SetID)
	if req.SetID == "" {
		return errors.New("setId must not be empty")
	}
	if req.NumRuns != nil && *req.NumRuns <= 0 {
		return errors.New("numRuns must be greater than 0 when provided")
	}
	return nil
}
