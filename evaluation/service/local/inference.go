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
	"time"

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
	startTime := time.Now()
	defer func() {
		afterErr := s.runAfterInferenceSetCallbacks(ctx, req, results, err, startTime)
		if afterErr != nil {
			results = nil
			err = afterErr
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
	startTime time.Time,
) error {
	_, err = callback.RunAfterInferenceSet(ctx, s.callbacks, &service.AfterInferenceSetArgs{
		Request:   req,
		Results:   results,
		Error:     err,
		StartTime: startTime,
	})
	if err != nil {
		return fmt.Errorf("run after inference set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	return nil
}

func (s *local) runBeforeInferenceCaseCallbacks(
	ctx context.Context,
	req *service.InferenceRequest,
	evalCaseID string,
	sessionID string,
) (context.Context, error) {
	result, err := callback.RunBeforeInferenceCase(ctx, s.callbacks, &service.BeforeInferenceCaseArgs{
		Request:    req,
		EvalCaseID: evalCaseID,
		SessionID:  sessionID,
	})
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if err != nil {
		return ctx, fmt.Errorf("run before inference case callbacks (app=%s, evalSetID=%s, evalCaseID=%s, sessionID=%s): %w",
			req.AppName, req.EvalSetID, evalCaseID, sessionID, err)
	}
	return ctx, nil
}

func (s *local) runAfterInferenceCaseCallbacks(
	ctx context.Context,
	req *service.InferenceRequest,
	evalCaseID string,
	result *service.InferenceResult,
	err error,
	startTime time.Time,
) error {
	_, afterErr := callback.RunAfterInferenceCase(ctx, s.callbacks, &service.AfterInferenceCaseArgs{
		Request:   req,
		Result:    result,
		Error:     err,
		StartTime: startTime,
	})
	if afterErr != nil {
		return fmt.Errorf("run after inference case callbacks (app=%s, evalSetID=%s, evalCaseID=%s): %w",
			req.AppName, req.EvalSetID, evalCaseID, afterErr)
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
		result := s.inferenceEvalCase(ctx, req, evalCase)
		results = append(results, result)
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
			results[idx] = newFailedInferenceResult(
				newInferenceResult(req.AppName, req.EvalSetID, sessionID, evalCase),
				fmt.Errorf("submit inference task for eval case %s: %w", evalCase.EvalID, err),
			)
			param.reset()
			evalCaseInferenceParamPool.Put(param)
		}
	}
	wg.Wait()
	return results, nil
}

func (s *local) inferenceEvalCase(ctx context.Context, req *service.InferenceRequest, evalCase *evalset.EvalCase) (result *service.InferenceResult) {
	sessionID := s.sessionIDSupplier(ctx)
	if evalCase == nil {
		return newFailedInferenceResult(&service.InferenceResult{
			AppName:    req.AppName,
			EvalSetID:  req.EvalSetID,
			SessionID:  sessionID,
			EvalCaseID: "",
			EvalMode:   evalset.EvalModeDefault,
			UserID:     "",
		}, errors.New("eval case is nil"))
	}
	ctx, err := s.runBeforeInferenceCaseCallbacks(ctx, req, evalCase.EvalID, sessionID)
	if err != nil {
		return newFailedInferenceResult(&service.InferenceResult{
			AppName:    req.AppName,
			EvalSetID:  req.EvalSetID,
			SessionID:  sessionID,
			EvalCaseID: evalCase.EvalID,
			EvalMode:   evalset.EvalModeDefault,
			UserID:     "",
		}, err)
	}
	caseStartTime := time.Now()
	defer func() {
		afterErr := s.runAfterInferenceCaseCallbacks(ctx, req, evalCase.EvalID, result, nil, caseStartTime)
		if afterErr != nil {
			result = newFailedInferenceResult(result, errors.Join(err, afterErr))
		}
	}()
	result = newInferenceResult(req.AppName, req.EvalSetID, sessionID, evalCase)
	if evalCase.SessionInput == nil {
		err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): session input is nil", evalCase.EvalID, sessionID)
		return newFailedInferenceResult(result, err)
	}
	if len(evalCase.ActualConversation) != 0 && evalCase.EvalMode != evalset.EvalModeTrace {
		err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): actualConversation is only supported in trace mode",
			evalCase.EvalID, sessionID)
		return newFailedInferenceResult(result, err)
	}
	if evalCase.EvalMode == evalset.EvalModeTrace {
		if len(evalCase.ActualConversation) != 0 {
			if len(evalCase.Conversation) != 0 && len(evalCase.ActualConversation) != len(evalCase.Conversation) {
				err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): actual conversation length %d does not match conversation length %d",
					evalCase.EvalID, sessionID, len(evalCase.ActualConversation), len(evalCase.Conversation))
				return newFailedInferenceResult(result, err)
			}
			for i, invocation := range evalCase.ActualConversation {
				if invocation == nil {
					err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): actual invocation is nil at index %d",
						evalCase.EvalID, sessionID, i)
					return newFailedInferenceResult(result, err)
				}
				if invocation.UserContent == nil {
					err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): actual invocation user content is nil at index %d",
						evalCase.EvalID, sessionID, i)
					return newFailedInferenceResult(result, err)
				}
			}
			result.Inferences = evalCase.ActualConversation
			result.Status = status.EvalStatusPassed
			return result
		}
		if len(evalCase.Conversation) == 0 {
			err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): invocations are empty", evalCase.EvalID, sessionID)
			return newFailedInferenceResult(result, err)
		}
		result.Inferences = evalCase.Conversation
		result.Status = status.EvalStatusPassed
		return result
	}
	if len(evalCase.Conversation) == 0 {
		err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): invocations are empty", evalCase.EvalID, sessionID)
		return newFailedInferenceResult(result, err)
	}
	inferences, err := inference.Inference(
		ctx,
		s.runner,
		evalCase.Conversation,
		evalCase.SessionInput,
		sessionID,
		evalCase.ContextMessages,
	)
	if err != nil {
		err = fmt.Errorf("inference eval case (evalCaseID=%s, sessionID=%s): %w", evalCase.EvalID, sessionID, err)
		return newFailedInferenceResult(result, err)
	}
	result.Inferences = inferences
	result.Status = status.EvalStatusPassed
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
