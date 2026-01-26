//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local implementation of service.Service.
package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/panjf2000/ants/v2"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/callback"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const reasonSeparator = ";"

// local is a local implementation of service.Service.
type local struct {
	runner                           runner.Runner
	evalSetManager                   evalset.Manager
	evalResultManager                evalresult.Manager
	registry                         registry.Registry
	sessionIDSupplier                func(ctx context.Context) string
	callbacks                        *service.Callbacks
	evalCaseParallelism              int
	evalCaseParallelInferenceEnabled bool
	evalCaseInferencePool            *ants.PoolWithFunc
}

// New returns a new local evaluation service.
// If no service.Option is provided, the service will use the default options.
func New(runner runner.Runner, opt ...service.Option) (service.Service, error) {
	if runner == nil {
		return nil, errors.New("runner is nil")
	}
	opts := service.NewOptions(opt...)
	if opts.EvalCaseParallelInferenceEnabled && opts.EvalCaseParallelism <= 0 {
		return nil, errors.New("eval case parallelism must be greater than 0")
	}
	if opts.EvalSetManager == nil {
		return nil, errors.New("eval set manager is nil")
	}
	if opts.EvalResultManager == nil {
		return nil, errors.New("eval result manager is nil")
	}
	if opts.Registry == nil {
		return nil, errors.New("registry is nil")
	}
	if opts.SessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	service := &local{
		runner:                           runner,
		evalSetManager:                   opts.EvalSetManager,
		evalResultManager:                opts.EvalResultManager,
		registry:                         opts.Registry,
		sessionIDSupplier:                opts.SessionIDSupplier,
		callbacks:                        opts.Callbacks,
		evalCaseParallelism:              opts.EvalCaseParallelism,
		evalCaseParallelInferenceEnabled: opts.EvalCaseParallelInferenceEnabled,
	}
	if service.evalCaseParallelInferenceEnabled {
		pool, err := createEvalCaseInferencePool(service.evalCaseParallelism)
		if err != nil {
			return nil, fmt.Errorf("create eval case inference pool: %w", err)
		}
		service.evalCaseInferencePool = pool
	}
	return service, nil
}

// Close closes the eval service and releases owned resources.
func (s *local) Close() error {
	if s.evalCaseInferencePool != nil {
		s.evalCaseInferencePool.Release()
	}
	return nil
}

func (s *local) runBeforeEvaluateSetCallbacks(ctx context.Context, req *service.EvaluateRequest) (context.Context, error) {
	callbackCtx := ctx
	beforeResult, callbackErr := callback.RunBeforeEvaluateSet(callbackCtx, s.callbacks, &service.BeforeEvaluateSetArgs{Request: req})
	if beforeResult != nil && beforeResult.Context != nil {
		callbackCtx = beforeResult.Context
	}
	if callbackErr != nil {
		return callbackCtx, fmt.Errorf("run before evaluate set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, callbackErr)
	}
	return callbackCtx, nil
}

func (s *local) runAfterEvaluateSetCallbacks(ctx context.Context, req *service.EvaluateRequest, result *evalresult.EvalSetResult, err error) error {
	_, afterErr := callback.RunAfterEvaluateSet(ctx, s.callbacks, &service.AfterEvaluateSetArgs{
		Request: req,
		Result:  result,
		Error:   err,
	})
	if afterErr != nil {
		return fmt.Errorf("run after evaluate set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, afterErr)
	}
	return nil
}

func (s *local) runBeforeEvaluateCaseCallbacks(ctx context.Context, req *service.EvaluateRequest, evalCaseID string) (context.Context, error) {
	caseCtx := ctx
	beforeResult, callbackErr := callback.RunBeforeEvaluateCase(caseCtx, s.callbacks, &service.BeforeEvaluateCaseArgs{
		Request:    req,
		EvalCaseID: evalCaseID,
	})
	if beforeResult != nil && beforeResult.Context != nil {
		caseCtx = beforeResult.Context
	}
	if callbackErr != nil {
		return caseCtx, fmt.Errorf("run before evaluate case callbacks (evalCaseID=%s): %w", evalCaseID, callbackErr)
	}
	return caseCtx, nil
}

func (s *local) runAfterEvaluateCaseCallbacks(
	ctx context.Context,
	req *service.EvaluateRequest,
	inferenceResult *service.InferenceResult,
	result *evalresult.EvalCaseResult,
	err error,
) error {
	_, afterErr := callback.RunAfterEvaluateCase(ctx, s.callbacks, &service.AfterEvaluateCaseArgs{
		Request:         req,
		InferenceResult: inferenceResult,
		Result:          result,
		Error:           err,
	})
	if afterErr != nil {
		evalCaseID := ""
		if inferenceResult != nil {
			evalCaseID = inferenceResult.EvalCaseID
		}
		return fmt.Errorf("run after evaluate case callbacks (evalCaseID=%s): %w", evalCaseID, afterErr)
	}
	return nil
}

// Evaluate runs the evaluation on the inference results and returns the persisted eval set result.
func (s *local) Evaluate(ctx context.Context, req *service.EvaluateRequest) (*evalresult.EvalSetResult, error) {
	if req == nil {
		return nil, errors.New("evaluate request is nil")
	}
	if req.AppName == "" {
		return nil, errors.New("app name is empty")
	}
	if req.EvalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	if req.EvaluateConfig == nil {
		return nil, errors.New("evaluate config is nil")
	}

	callbackCtx, callbackErr := s.runBeforeEvaluateSetCallbacks(ctx, req)
	if callbackErr != nil {
		return nil, callbackErr
	}

	evalCaseResults := make([]*evalresult.EvalCaseResult, 0, len(req.InferenceResults))
	for _, inferenceResult := range req.InferenceResults {
		if inferenceResult == nil {
			err := errors.New("inference result is nil")
			if afterErr := s.runAfterEvaluateSetCallbacks(callbackCtx, req, nil, err); afterErr != nil {
				err = errors.Join(err, afterErr)
			}
			return nil, fmt.Errorf("evaluate (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
		}

		caseCtx, err := s.runBeforeEvaluateCaseCallbacks(callbackCtx, req, inferenceResult.EvalCaseID)
		if err != nil {
			if afterErr := s.runAfterEvaluateSetCallbacks(callbackCtx, req, nil, err); afterErr != nil {
				err = errors.Join(err, afterErr)
			}
			return nil, fmt.Errorf("evaluate (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
		}

		var (
			result  *evalresult.EvalCaseResult
			caseErr error
		)
		if inferenceResult.Status != evalstatus.EvalStatusPassed {
			caseErr = errors.New(inferenceResult.ErrorMessage)
			evalCaseResults = append(evalCaseResults, s.failedEvalCaseResult(req.EvalSetID, inferenceResult, inferenceResult.ErrorMessage))
			result = evalCaseResults[len(evalCaseResults)-1]
		} else {
			caseResult, evalErr := s.evaluatePerCase(caseCtx, inferenceResult, req.EvaluateConfig)
			if evalErr != nil {
				caseErr = evalErr
				result = s.failedEvalCaseResult(req.EvalSetID, inferenceResult, evalErr.Error())
			} else {
				result = caseResult
			}
			evalCaseResults = append(evalCaseResults, result)
		}

		if err := s.runAfterEvaluateCaseCallbacks(caseCtx, req, inferenceResult, result, caseErr); err != nil {
			if afterErr := s.runAfterEvaluateSetCallbacks(callbackCtx, req, nil, err); afterErr != nil {
				err = errors.Join(err, afterErr)
			}
			return nil, fmt.Errorf("evaluate (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
		}
	}
	evalSetResult := &evalresult.EvalSetResult{
		EvalSetID:         req.EvalSetID,
		EvalCaseResults:   evalCaseResults,
		CreationTimestamp: &epochtime.EpochTime{Time: time.Now()},
	}

	evalSetResultID, err := s.evalResultManager.Save(callbackCtx, req.AppName, evalSetResult)
	if err != nil {
		err = fmt.Errorf("save eval set result: %w", err)
		if afterErr := s.runAfterEvaluateSetCallbacks(callbackCtx, req, nil, err); afterErr != nil {
			err = errors.Join(err, afterErr)
		}
		return nil, fmt.Errorf("evaluate (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	evalSetResult.EvalSetResultID = evalSetResultID
	evalSetResult.EvalSetResultName = evalSetResultID

	if afterErr := s.runAfterEvaluateSetCallbacks(callbackCtx, req, evalSetResult, nil); afterErr != nil {
		return evalSetResult, afterErr
	}
	return evalSetResult, nil
}

func (s *local) failedEvalCaseResult(evalSetID string, inferenceResult *service.InferenceResult, errorMessage string) *evalresult.EvalCaseResult {
	return &evalresult.EvalCaseResult{
		EvalSetID:       evalSetID,
		EvalID:          inferenceResult.EvalCaseID,
		FinalEvalStatus: evalstatus.EvalStatusFailed,
		ErrorMessage:    errorMessage,
		SessionID:       inferenceResult.SessionID,
		UserID:          inferenceResult.UserID,
	}
}

// evaluatePerCase runs the evaluation on the inference result and returns the case evaluation result.
func (s *local) evaluatePerCase(ctx context.Context, inferenceResult *service.InferenceResult,
	evaluateConfig *service.EvaluateConfig) (*evalresult.EvalCaseResult, error) {
	if inferenceResult == nil {
		return nil, fmt.Errorf("evaluate per case: inference result is nil")
	}
	if evaluateConfig == nil {
		return nil, fmt.Errorf("evaluate per case (evalCaseID=%s): evaluate config is nil", inferenceResult.EvalCaseID)
	}
	evalCase, err := s.evalSetManager.GetCase(ctx,
		inferenceResult.AppName,
		inferenceResult.EvalSetID,
		inferenceResult.EvalCaseID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"get eval case (app=%s, evalSetID=%s, evalCaseID=%s): %w",
			inferenceResult.AppName,
			inferenceResult.EvalSetID,
			inferenceResult.EvalCaseID,
			err,
		)
	}
	inputs, err := prepareCaseEvaluationInputs(inferenceResult, evalCase)
	if err != nil {
		return nil, fmt.Errorf("prepare case evaluation inputs (evalCaseID=%s): %w", inferenceResult.EvalCaseID, err)
	}
	// overallMetricResults collects the metric results for the entire eval case.
	overallMetricResults := make([]*evalresult.EvalMetricResult, 0, len(evaluateConfig.EvalMetrics))
	perInvocation := make([]*evalresult.EvalMetricResultPerInvocation, len(inputs.actuals))
	for i, actual := range inputs.actuals {
		perInvocation[i] = &evalresult.EvalMetricResultPerInvocation{
			ActualInvocation:   actual,
			ExpectedInvocation: inputs.expecteds[i],
			EvalMetricResults:  make([]*evalresult.EvalMetricResult, 0, len(evaluateConfig.EvalMetrics)),
		}
	}
	// Iterate through every configured metric and run the evaluation.
	for _, evalMetric := range evaluateConfig.EvalMetrics {
		result, err := s.evaluateMetric(ctx, evalMetric, inputs.actuals, inputs.expecteds)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Skip metrics whose evaluator or artifacts are intentionally absent.
				continue
			}
			return nil, fmt.Errorf("run evaluation for metric %s: %w", evalMetric.MetricName, err)
		}
		if len(result.PerInvocationResults) != len(perInvocation) {
			return nil, fmt.Errorf("metric %s returned %d per-invocation results, expected %d", evalMetric.MetricName,
				len(result.PerInvocationResults), len(perInvocation))
		}
		reasons := make([]string, 0, len(result.PerInvocationResults))
		rubricScores := make([]*evalresult.RubricScore, 0, len(result.PerInvocationResults))
		for i, invocationResult := range result.PerInvocationResults {
			// Record the metric outcome for the corresponding invocation.
			evalMetricResult := &evalresult.EvalMetricResult{
				MetricName: evalMetric.MetricName,
				Threshold:  evalMetric.Threshold,
				Criterion:  evalMetric.Criterion,
				Score:      invocationResult.Score,
				EvalStatus: invocationResult.Status,
			}
			if invocationResult.Details != nil {
				evalMetricResult.Details = &evalresult.EvalMetricResultDetails{
					Reason:       invocationResult.Details.Reason,
					Score:        invocationResult.Details.Score,
					RubricScores: invocationResult.Details.RubricScores,
				}
				reasons = append(reasons, invocationResult.Details.Reason)
				rubricScores = append(rubricScores, invocationResult.Details.RubricScores...)
			}
			perInvocation[i].EvalMetricResults = append(perInvocation[i].EvalMetricResults, evalMetricResult)
		}
		overallMetricResults = append(overallMetricResults, &evalresult.EvalMetricResult{
			MetricName: evalMetric.MetricName,
			Threshold:  evalMetric.Threshold,
			Criterion:  evalMetric.Criterion,
			Score:      result.OverallScore,
			EvalStatus: result.OverallStatus,
			Details: &evalresult.EvalMetricResultDetails{
				Reason:       strings.Join(reasons, reasonSeparator),
				Score:        result.OverallScore,
				RubricScores: rubricScores,
			},
		})
	}
	// Summarize the overall metric results and return the final eval status.
	finalStatus, err := istatus.SummarizeMetricsStatus(overallMetricResults)
	if err != nil {
		return nil, fmt.Errorf("summarize overall metric results: %w", err)
	}
	return &evalresult.EvalCaseResult{
		EvalSetID:                     inferenceResult.EvalSetID,
		EvalID:                        inferenceResult.EvalCaseID,
		FinalEvalStatus:               finalStatus,
		OverallEvalMetricResults:      overallMetricResults,
		EvalMetricResultPerInvocation: perInvocation,
		SessionID:                     inferenceResult.SessionID,
		UserID:                        inputs.userID,
	}, nil
}

// evaluateMetric locates the evaluator registered for the metric and runs the evaluation.
func (s *local) evaluateMetric(ctx context.Context, evalMetric *metric.EvalMetric,
	actuals, expecteds []*evalset.Invocation) (*evaluator.EvaluateResult, error) {
	metricEvaluator, err := s.registry.Get(evalMetric.MetricName)
	if err != nil {
		return nil, fmt.Errorf("get evaluator for metric %s: %w", evalMetric.MetricName, err)
	}
	// Run the evaluation on the actual and expected invocations and return the evaluation result.
	return metricEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

type caseEvaluationInputs struct {
	actuals   []*evalset.Invocation
	expecteds []*evalset.Invocation
	userID    string
}

func prepareCaseEvaluationInputs(inferenceResult *service.InferenceResult, evalCase *evalset.EvalCase) (*caseEvaluationInputs, error) {
	if len(evalCase.Conversation) == 0 {
		return nil, errors.New("invalid eval case")
	}
	if evalCase.SessionInput == nil {
		return nil, errors.New("session input is nil")
	}
	evalMode := evalCase.EvalMode
	actuals := inferenceResult.Inferences
	expecteds := evalCase.Conversation
	if evalMode == evalset.EvalModeTrace {
		expecteds = traceExpectedsForEval(evalCase.Conversation)
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("inference count %d does not match expected conversation length %d",
			len(actuals), len(expecteds))
	}
	return &caseEvaluationInputs{
		actuals:   actuals,
		expecteds: expecteds,
		userID:    evalCase.SessionInput.UserID,
	}, nil
}

// traceExpectedsForEval builds placeholder expected invocations that only preserve user inputs.
// This whitelist prevents trace outputs from being treated as reference answers and stays correct when Invocation gains new fields.
func traceExpectedsForEval(conversation []*evalset.Invocation) []*evalset.Invocation {
	expecteds := make([]*evalset.Invocation, len(conversation))
	for i, invocation := range conversation {
		expecteds[i] = &evalset.Invocation{
			InvocationID: invocation.InvocationID,
			UserContent:  invocation.UserContent,
		}
	}
	return expecteds
}
