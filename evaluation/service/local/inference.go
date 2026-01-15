//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/internal/inference"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Inference runs the agent for the requested eval cases and returns the inference results for each case.
func (s *local) Inference(ctx context.Context, req *service.InferenceRequest) ([]*service.InferenceResult, error) {
	if err := s.validateInferenceRequest(req); err != nil {
		return nil, fmt.Errorf("validate inference request: %w", err)
	}
	evalCases, err := s.loadInferenceEvalCases(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("load inference eval cases: %w", err)
	}
	if len(evalCases) == 0 {
		return []*service.InferenceResult{}, nil
	}
	return s.inferEvalCases(ctx, req.EvalSetID, evalCases)
}

func (s *local) validateInferenceRequest(req *service.InferenceRequest) error {
	if req == nil {
		return errors.New("inference request is nil")
	}
	if req.AppName == "" {
		return errors.New("app name is empty")
	}
	if req.EvalSetID == "" {
		return errors.New("eval set id is empty")
	}
	return nil
}

func (s *local) loadInferenceEvalCases(ctx context.Context, req *service.InferenceRequest) ([]*evalset.EvalCase, error) {
	evalSet, err := s.evalSetManager.Get(ctx, req.AppName, req.EvalSetID)
	if err != nil {
		return nil, fmt.Errorf("get eval set: %w", err)
	}
	if len(req.EvalCaseIDs) == 0 {
		filtered := make([]*evalset.EvalCase, 0, len(evalSet.EvalCases))
		for _, evalCase := range evalSet.EvalCases {
			if evalCase == nil {
				continue
			}
			filtered = append(filtered, evalCase)
		}
		return filtered, nil
	}
	wanted := make(map[string]struct{}, len(req.EvalCaseIDs))
	for _, id := range req.EvalCaseIDs {
		wanted[id] = struct{}{}
	}
	filtered := make([]*evalset.EvalCase, 0, len(evalSet.EvalCases))
	for _, evalCase := range evalSet.EvalCases {
		if evalCase == nil {
			continue
		}
		if _, ok := wanted[evalCase.EvalID]; ok {
			filtered = append(filtered, evalCase)
		}
	}
	return filtered, nil
}

func (s *local) inferEvalCases(ctx context.Context, evalSetID string, evalCases []*evalset.EvalCase) ([]*service.InferenceResult, error) {
	if s.evalCaseParallelInferenceEnabled && s.evalCaseInferencePool != nil {
		return s.inferEvalCasesParallel(ctx, evalSetID, evalCases)
	}
	return s.inferEvalCasesSerial(ctx, evalSetID, evalCases)
}

func (s *local) inferEvalCasesSerial(ctx context.Context, evalSetID string, evalCases []*evalset.EvalCase) ([]*service.InferenceResult, error) {
	results := make([]*service.InferenceResult, 0, len(evalCases))
	for _, evalCase := range evalCases {
		inferenceResult, err := s.inferenceEvalCase(ctx, evalSetID, evalCase)
		if err != nil {
			return nil, fmt.Errorf("run inference for eval case %s: %w", evalCase.EvalID, err)
		}
		results = append(results, inferenceResult)
	}
	return results, nil
}

func (s *local) inferEvalCasesParallel(ctx context.Context, evalSetID string, evalCases []*evalset.EvalCase) ([]*service.InferenceResult, error) {
	results := make([]*service.InferenceResult, len(evalCases))
	errs := make([]error, len(evalCases))
	var wg sync.WaitGroup
	for idx, evalCase := range evalCases {
		wg.Add(1)
		param := evalCaseInferenceParamPool.Get().(*evalCaseInferenceParam)
		param.idx = idx
		param.ctx = ctx
		param.evalSetID = evalSetID
		param.evalCase = evalCase
		param.svc = s
		param.results = results
		param.errs = errs
		param.wg = &wg
		if err := s.evalCaseInferencePool.Invoke(param); err != nil {
			wg.Done()
			errs[idx] = fmt.Errorf("submit inference task for eval case %s: %w", evalCase.EvalID, err)
			param.reset()
			evalCaseInferenceParamPool.Put(param)
		}
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err != nil {
		return nil, fmt.Errorf("inference eval cases parallel: %w", err)
	}
	return results, nil
}

func (s *local) inferenceEvalCase(ctx context.Context, evalSetID string, evalCase *evalset.EvalCase) (*service.InferenceResult, error) {
	if evalCase.SessionInput == nil {
		return nil, errors.New("session input is nil")
	}
	if len(evalCase.Conversation) == 0 {
		return nil, errors.New("invocations are empty")
	}
	if evalCase.EvalMode == evalset.EvalModeTrace {
		return &service.InferenceResult{
			AppName:    evalCase.SessionInput.AppName,
			EvalSetID:  evalSetID,
			EvalCaseID: evalCase.EvalID,
			EvalMode:   evalset.EvalModeTrace,
			Inferences: evalCase.Conversation,
			SessionID:  s.sessionIDSupplier(ctx),
			Status:     status.EvalStatusPassed,
		}, nil
	}
	sessionID := s.sessionIDSupplier(ctx)
	inferences, err := inference.Inference(
		ctx,
		s.runner,
		evalCase.Conversation,
		evalCase.SessionInput,
		sessionID,
		evalCase.ContextMessages,
	)
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}
	return &service.InferenceResult{
		AppName:    evalCase.SessionInput.AppName,
		EvalSetID:  evalSetID,
		EvalCaseID: evalCase.EvalID,
		EvalMode:   evalset.EvalModeDefault,
		SessionID:  sessionID,
		Status:     status.EvalStatusPassed,
		Inferences: inferences,
	}, nil
}
