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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/callback"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/internal/inference"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Inference runs the agent for the requested eval cases and returns the inference results for each case.
func (s *local) Inference(ctx context.Context, req *service.InferenceRequest) (results []*service.InferenceResult, err error) {
	if err := s.validateInferenceRequest(req); err != nil {
		return nil, fmt.Errorf("validate inference request: %w", err)
	}
	ctx, err = s.runBeforeInferenceSetCallbacks(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("run before inference set callbacks (app=%s, evalSetID=%s): %w",
			req.AppName, req.EvalSetID, err)
	}
	defer func() {
		err = s.runAfterInferenceSetCallbacks(ctx, req, results, err)
		if err != nil {
			results = nil
			err = fmt.Errorf("run after inference set callbacks (app=%s, evalSetID=%s): %w",
				req.AppName, req.EvalSetID, err)
		}
	}()
	evalCases, err := s.loadInferenceEvalCases(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("load inference eval cases: %w", err)
	}
	if len(evalCases) == 0 {
		return []*service.InferenceResult{}, nil
	}
	results, err = s.inferEvalCases(ctx, req, evalCases)
	if err != nil {
		return nil, fmt.Errorf("infer eval cases: %w", err)
	}
	return results, nil
}

func (s *local) runBeforeInferenceSetCallbacks(ctx context.Context, req *service.InferenceRequest) (context.Context, error) {
	result, err := callback.RunBeforeInferenceSet(ctx, s.callbacks, &service.BeforeInferenceSetArgs{Request: req})
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if err != nil {
		return ctx, fmt.Errorf("run before inference set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	return ctx, nil
}

func (s *local) runAfterInferenceSetCallbacks(
	ctx context.Context,
	req *service.InferenceRequest,
	results []*service.InferenceResult,
	err error,
) error {
	_, err = callback.RunAfterInferenceSet(ctx, s.callbacks, &service.AfterInferenceSetArgs{
		Request: req,
		Results: results,
		Error:   err,
	})
	if err != nil {
		return fmt.Errorf("run after inference set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	return nil
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

func (s *local) inferEvalCases(ctx context.Context, req *service.InferenceRequest, evalCases []*evalset.EvalCase) ([]*service.InferenceResult, error) {
	if s.evalCaseParallelInferenceEnabled && s.evalCaseInferencePool != nil {
		return s.inferEvalCasesParallel(ctx, req, evalCases)
	}
	return s.inferEvalCasesSerial(ctx, req, evalCases)
}

func (s *local) inferEvalCasesSerial(ctx context.Context, req *service.InferenceRequest, evalCases []*evalset.EvalCase) ([]*service.InferenceResult, error) {
	results := make([]*service.InferenceResult, 0, len(evalCases))
	for _, evalCase := range evalCases {
		results = append(results, s.inferenceEvalCase(ctx, req, evalCase))
	}
	return results, nil
}

func (s *local) inferEvalCasesParallel(ctx context.Context, req *service.InferenceRequest, evalCases []*evalset.EvalCase) ([]*service.InferenceResult, error) {
	results := make([]*service.InferenceResult, len(evalCases))
	var wg sync.WaitGroup
	for idx, evalCase := range evalCases {
		wg.Add(1)
		param := evalCaseInferenceParamPool.Get().(*evalCaseInferenceParam)
		param.idx = idx
		param.ctx = ctx
		param.req = req
		param.evalCase = evalCase
		param.svc = s
		param.results = results
		param.wg = &wg
		if err := s.evalCaseInferencePool.Invoke(param); err != nil {
			wg.Done()
			sessionID := s.sessionIDSupplier(ctx)
			evalCaseID := ""
			if evalCase != nil {
				evalCaseID = evalCase.EvalID
			}
			workerErr := fmt.Errorf("submit inference task for eval case %s: %w", evalCaseID, err)
			if evalCase != nil {
				results[idx] = newFailedInferenceResult(
					newInferenceResult(req.AppName, req.EvalSetID, sessionID, evalCase),
					workerErr,
				)
			} else {
				results[idx] = &service.InferenceResult{
					AppName:      req.AppName,
					EvalSetID:    req.EvalSetID,
					SessionID:    sessionID,
					Status:       status.EvalStatusFailed,
					ErrorMessage: workerErr.Error(),
					Inferences:   nil,
					EvalCaseID:   "",
					EvalMode:     evalset.EvalModeDefault,
					UserID:       "",
				}
			}
			param.reset()
			evalCaseInferenceParamPool.Put(param)
		}
	}
	wg.Wait()
	return results, nil
}

func (s *local) inferenceEvalCase(ctx context.Context, req *service.InferenceRequest, evalCase *evalset.EvalCase) *service.InferenceResult {
	if req == nil {
		return &service.InferenceResult{
			Status:       status.EvalStatusFailed,
			ErrorMessage: "inference eval case: inference request is nil",
		}
	}
	sessionID := s.sessionIDSupplier(ctx)
	if evalCase == nil {
		runErr := fmt.Errorf("inference eval case (sessionID=%s): eval case is nil", sessionID)
		return newFailedInferenceResult(&service.InferenceResult{
			AppName:    req.AppName,
			EvalSetID:  req.EvalSetID,
			SessionID:  sessionID,
			EvalCaseID: "",
			EvalMode:   evalset.EvalModeDefault,
			UserID:     "",
		}, runErr)
	}

	caseCtx := ctx
	beforeResult, callbackErr := callback.RunBeforeInferenceCase(caseCtx, s.callbacks, &service.BeforeInferenceCaseArgs{
		Request:    req,
		EvalCaseID: evalCase.EvalID,
		SessionID:  sessionID,
	})
	if beforeResult != nil && beforeResult.Context != nil {
		caseCtx = beforeResult.Context
	}

	result := newInferenceResult(req.AppName, req.EvalSetID, sessionID, evalCase)
	var runErr error
	if callbackErr != nil {
		runErr = fmt.Errorf("run before inference case callbacks (evalCaseID=%s, sessionID=%s): %w", evalCase.EvalID, sessionID, callbackErr)
		newFailedInferenceResult(result, runErr)
	} else if evalCase.SessionInput == nil {
		runErr = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): session input is nil", evalCase.EvalID, sessionID)
		newFailedInferenceResult(result, runErr)
	} else if len(evalCase.Conversation) == 0 {
		runErr = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): invocations are empty", evalCase.EvalID, sessionID)
		newFailedInferenceResult(result, runErr)
	} else if evalCase.EvalMode == evalset.EvalModeTrace {
		result.Inferences = evalCase.Conversation
		result.Status = status.EvalStatusPassed
	} else {
		inferences, err := inference.Inference(
			caseCtx,
			s.runner,
			evalCase.Conversation,
			evalCase.SessionInput,
			sessionID,
			evalCase.ContextMessages,
		)
		if err != nil {
			runErr = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): %w", evalCase.EvalID, sessionID, err)
			newFailedInferenceResult(result, runErr)
		} else {
			result.Inferences = inferences
			result.Status = status.EvalStatusPassed
		}
	}

	_, afterErr := callback.RunAfterInferenceCase(caseCtx, s.callbacks, &service.AfterInferenceCaseArgs{
		Request: req,
		Result:  result,
		Error:   runErr,
	})
	if afterErr != nil {
		afterErr = fmt.Errorf("run after inference case callbacks (evalCaseID=%s): %w", evalCase.EvalID, afterErr)
		newFailedInferenceResult(result, errors.Join(runErr, afterErr))
	}
	return result
}

func newInferenceResult(appName, evalSetID, sessionID string, evalCase *evalset.EvalCase) *service.InferenceResult {
	userID := ""
	if evalCase.SessionInput != nil {
		userID = evalCase.SessionInput.UserID
	}
	return &service.InferenceResult{
		AppName:    appName,
		EvalSetID:  evalSetID,
		EvalCaseID: evalCase.EvalID,
		EvalMode:   evalCase.EvalMode,
		SessionID:  sessionID,
		UserID:     userID,
	}
}

func newFailedInferenceResult(result *service.InferenceResult, err error) *service.InferenceResult {
	result.Status = status.EvalStatusFailed
	result.ErrorMessage = err.Error()
	result.Inferences = nil
	return result
}
