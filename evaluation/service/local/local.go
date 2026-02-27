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
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/callback"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	evalstatus "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const reasonSeparator = ";"

// local is a local implementation of service.Service.
type local struct {
	runner                            runner.Runner
	evalSetManager                    evalset.Manager
	evalResultManager                 evalresult.Manager
	registry                          registry.Registry
	sessionIDSupplier                 func(ctx context.Context) string
	callbacks                         *service.Callbacks
	runOptions                        []agent.RunOption
	evalCaseParallelism               int
	evalCaseParallelInferenceEnabled  bool
	evalCaseParallelEvaluationEnabled bool
	evalCaseInferencePool             *ants.PoolWithFunc
	evalCaseInferencePoolOnce         sync.Once
	evalCaseInferencePoolErr          error
	evalCaseEvaluationPool            *ants.PoolWithFunc
	evalCaseEvaluationPoolOnce        sync.Once
	evalCaseEvaluationPoolErr         error
}

// New returns a new local evaluation service.
// If no service.Option is provided, the service will use the default options.
func New(runner runner.Runner, opt ...service.Option) (service.Service, error) {
	if runner == nil {
		return nil, errors.New("runner is nil")
	}
	opts := service.NewOptions(opt...)
	if (opts.EvalCaseParallelInferenceEnabled || opts.EvalCaseParallelEvaluationEnabled) && opts.EvalCaseParallelism <= 0 {
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
		runner:                            runner,
		evalSetManager:                    opts.EvalSetManager,
		evalResultManager:                 opts.EvalResultManager,
		registry:                          opts.Registry,
		sessionIDSupplier:                 opts.SessionIDSupplier,
		callbacks:                         opts.Callbacks,
		runOptions:                        append([]agent.RunOption(nil), opts.RunOptions...),
		evalCaseParallelism:               opts.EvalCaseParallelism,
		evalCaseParallelInferenceEnabled:  opts.EvalCaseParallelInferenceEnabled,
		evalCaseParallelEvaluationEnabled: opts.EvalCaseParallelEvaluationEnabled,
	}
	if service.evalCaseParallelInferenceEnabled {
		pool, err := createEvalCaseInferencePool(service.evalCaseParallelism)
		if err != nil {
			return nil, fmt.Errorf("create eval case inference pool: %w", err)
		}
		service.evalCaseInferencePool = pool
	}
	if service.evalCaseParallelEvaluationEnabled {
		pool, err := createEvalCaseEvaluationPool(service.evalCaseParallelism)
		if err != nil {
			return nil, fmt.Errorf("create eval case evaluation pool: %w", err)
		}
		service.evalCaseEvaluationPool = pool
	}
	return service, nil
}

// Close closes the eval service and releases owned resources.
func (s *local) Close() error {
	if s.evalCaseInferencePool != nil {
		s.evalCaseInferencePool.Release()
	}
	if s.evalCaseEvaluationPool != nil {
		s.evalCaseEvaluationPool.Release()
	}
	return nil
}

func (s *local) runBeforeEvaluateSetCallbacks(ctx context.Context, callbacks *service.Callbacks, req *service.EvaluateRequest) (context.Context, error) {
	result, err := callback.RunBeforeEvaluateSet(ctx, callbacks, &service.BeforeEvaluateSetArgs{Request: req})
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if err != nil {
		return ctx, fmt.Errorf("run before evaluate set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	return ctx, nil
}

func (s *local) runAfterEvaluateSetCallbacks(ctx context.Context, callbacks *service.Callbacks, req *service.EvaluateRequest, result *service.EvalSetRunResult, err error, startTime time.Time) error {
	_, err = callback.RunAfterEvaluateSet(ctx, callbacks, &service.AfterEvaluateSetArgs{
		Request:   req,
		Result:    result,
		Error:     err,
		StartTime: startTime,
	})
	if err != nil {
		return fmt.Errorf("run after evaluate set callbacks (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	return nil
}

func (s *local) runBeforeEvaluateCaseCallbacks(ctx context.Context, callbacks *service.Callbacks, req *service.EvaluateRequest, evalCaseID string) (context.Context, error) {
	result, err := callback.RunBeforeEvaluateCase(ctx, callbacks, &service.BeforeEvaluateCaseArgs{
		Request:    req,
		EvalCaseID: evalCaseID,
	})
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if err != nil {
		return ctx, fmt.Errorf("run before evaluate case callbacks (app=%s, evalSetID=%s, evalCaseID=%s): %w",
			req.AppName, req.EvalSetID, evalCaseID, err)
	}
	return ctx, nil
}

func (s *local) runAfterEvaluateCaseCallbacks(
	ctx context.Context,
	callbacks *service.Callbacks,
	req *service.EvaluateRequest,
	inferenceResult *service.InferenceResult,
	result *evalresult.EvalCaseResult,
	err error,
	startTime time.Time,
) error {
	_, err = callback.RunAfterEvaluateCase(ctx, callbacks, &service.AfterEvaluateCaseArgs{
		Request:         req,
		InferenceResult: inferenceResult,
		Result:          result,
		Error:           err,
		StartTime:       startTime,
	})
	if err != nil {
		evalCaseID := ""
		if inferenceResult != nil {
			evalCaseID = inferenceResult.EvalCaseID
		}
		return fmt.Errorf("run after evaluate case callbacks (app=%s, evalSetID=%s, evalCaseID=%s): %w",
			req.AppName, req.EvalSetID, evalCaseID, err)
	}
	return nil
}

// Evaluate runs the evaluation on the inference results and returns the eval set run result.
func (s *local) Evaluate(ctx context.Context, req *service.EvaluateRequest, opt ...service.Option) (runResult *service.EvalSetRunResult, err error) {
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
	callOpts, err := s.resolveEvaluateOptions(opt...)
	if err != nil {
		return nil, err
	}
	ctx, err = s.runBeforeEvaluateSetCallbacks(ctx, callOpts.Callbacks, req)
	if err != nil {
		return nil, fmt.Errorf("run before evaluate set callbacks (app=%s, evalSetID=%s): %w",
			req.AppName, req.EvalSetID, err)
	}
	setStartTime := time.Now()
	defer func() {
		afterErr := s.runAfterEvaluateSetCallbacks(ctx, callOpts.Callbacks, req, runResult, err, setStartTime)
		if afterErr != nil {
			runResult = nil
			err = afterErr
		}
	}()
	evalCaseResults, err := s.evaluateCaseResults(ctx, req, callOpts)
	if err != nil {
		return nil, fmt.Errorf("evaluate case results (app=%s, evalSetID=%s): %w", req.AppName, req.EvalSetID, err)
	}
	runResult = &service.EvalSetRunResult{
		AppName:         req.AppName,
		EvalSetID:       req.EvalSetID,
		EvalCaseResults: evalCaseResults,
	}
	return runResult, nil
}

func (s *local) evaluateCaseResults(ctx context.Context, req *service.EvaluateRequest, opts *service.Options) ([]*evalresult.EvalCaseResult, error) {
	if opts.EvalCaseParallelEvaluationEnabled {
		return s.evaluateCaseResultsParallel(ctx, req, opts)
	}
	return s.evaluateCaseResultsSerial(ctx, req, opts)
}

func (s *local) evaluateCaseResultsParallel(ctx context.Context, req *service.EvaluateRequest, opts *service.Options) ([]*evalresult.EvalCaseResult, error) {
	results := make([]*evalresult.EvalCaseResult, len(req.InferenceResults))
	evalErrors := make([]error, len(req.InferenceResults))
	var wg sync.WaitGroup
	for idx, inferenceResult := range req.InferenceResults {
		wg.Add(1)
		param := evalCaseEvaluationParamPool.Get().(*evalCaseEvaluationParam)
		param.idx = idx
		param.ctx = ctx
		param.req = req
		param.inferenceResult = inferenceResult
		param.opts = opts
		param.svc = s
		param.results = results
		param.errs = evalErrors
		param.wg = &wg
		if err := s.evalCaseEvaluationPool.Invoke(param); err != nil {
			wg.Done()
			evalCaseID := ""
			if inferenceResult != nil {
				evalCaseID = inferenceResult.EvalCaseID
			}
			evalErrors[idx] = fmt.Errorf("submit evaluation task for eval case %s: %w", evalCaseID, err)
			param.reset()
			evalCaseEvaluationParamPool.Put(param)
		}
	}
	wg.Wait()
	if err := errors.Join(evalErrors...); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *local) evaluateCaseResultsSerial(ctx context.Context, req *service.EvaluateRequest, opts *service.Options) ([]*evalresult.EvalCaseResult, error) {
	results := make([]*evalresult.EvalCaseResult, len(req.InferenceResults))
	for idx, inferenceResult := range req.InferenceResults {
		caseResult, err := s.evaluateCase(ctx, req, inferenceResult, opts)
		if err != nil {
			evalCaseID := ""
			if inferenceResult != nil {
				evalCaseID = inferenceResult.EvalCaseID
			}
			return nil, fmt.Errorf("evaluate case (app=%s, evalSetID=%s, evalCaseID=%s): %w",
				req.AppName, req.EvalSetID, evalCaseID, err)
		}
		results[idx] = caseResult
	}
	return results, nil
}

func (s *local) evaluateCase(ctx context.Context, req *service.EvaluateRequest, inferenceResult *service.InferenceResult, opts *service.Options) (result *evalresult.EvalCaseResult, err error) {
	if inferenceResult == nil {
		return nil, errors.New("inference result is nil")
	}
	ctx, err = s.runBeforeEvaluateCaseCallbacks(ctx, opts.Callbacks, req, inferenceResult.EvalCaseID)
	if err != nil {
		return nil, fmt.Errorf("run before evaluate case callbacks (app=%s, evalSetID=%s, evalCaseID=%s): %w",
			req.AppName, req.EvalSetID, inferenceResult.EvalCaseID, err)
	}
	caseStartTime := time.Now()
	defer func() {
		afterErr := s.runAfterEvaluateCaseCallbacks(ctx, opts.Callbacks, req, inferenceResult, result, err, caseStartTime)
		if afterErr != nil {
			result = nil
			err = afterErr
		}
	}()
	if inferenceResult.Status != evalstatus.EvalStatusPassed {
		result = s.failedEvalCaseResult(req.EvalSetID, inferenceResult, inferenceResult.ErrorMessage)
		return result, nil
	}
	caseResult, err := s.evaluatePerCase(ctx, inferenceResult, req.EvaluateConfig, opts)
	if err != nil {
		result = s.failedEvalCaseResult(req.EvalSetID, inferenceResult, err.Error())
		return result, nil
	}
	return caseResult, nil
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
	evaluateConfig *service.EvaluateConfig, opts *service.Options) (*evalresult.EvalCaseResult, error) {
	if inferenceResult == nil {
		return nil, errors.New("inference result is nil")
	}
	if evaluateConfig == nil {
		return nil, fmt.Errorf("evaluate per case (evalCaseID=%s): evaluate config is nil", inferenceResult.EvalCaseID)
	}
	if opts.EvalSetManager == nil {
		return nil, errors.New("eval set manager is nil")
	}
	if opts.Registry == nil {
		return nil, errors.New("registry is nil")
	}
	evalCase, err := opts.EvalSetManager.GetCase(ctx,
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
		result, err := s.evaluateMetric(ctx, opts.Registry, evalMetric, inputs.actuals, inputs.expecteds)
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
func (s *local) evaluateMetric(ctx context.Context, reg registry.Registry, evalMetric *metric.EvalMetric, actuals, expecteds []*evalset.Invocation) (*evaluator.EvaluateResult, error) {
	metricEvaluator, err := reg.Get(evalMetric.MetricName)
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
	if evalCase.SessionInput == nil {
		return nil, errors.New("session input is nil")
	}
	actuals := inferenceResult.Inferences
	expecteds, err := buildExpectedsForEval(evalCase)
	if err != nil {
		return nil, fmt.Errorf("build expecteds for eval (evalCaseID=%s): %w", evalCase.EvalID, err)
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("inference count %d does not match expected conversation length %d",
			len(actuals), len(expecteds))
	}
	attachContextMessages(actuals, evalCase.ContextMessages)
	attachContextMessages(expecteds, evalCase.ContextMessages)
	return &caseEvaluationInputs{
		actuals:   actuals,
		expecteds: expecteds,
		userID:    evalCase.SessionInput.UserID,
	}, nil
}

func attachContextMessages(invocations []*evalset.Invocation, contextMessages []*model.Message) {
	if len(invocations) == 0 || len(contextMessages) == 0 {
		return
	}
	for _, invocation := range invocations {
		if invocation == nil {
			continue
		}
		if len(invocation.ContextMessages) != 0 {
			continue
		}
		invocation.ContextMessages = contextMessages
	}
}

// In trace mode, Conversation can represent either expected outputs or recorded actual traces for backward compatibility.
// If ActualConversation is provided, Conversation is treated as expecteds aligned by turn.
// If ActualConversation is omitted, Conversation is treated as the actual trace and expecteds are reduced to user-input placeholders.
// If Conversation is omitted but ActualConversation is provided, expecteds are built from ActualConversation as user-input placeholders,
// which represents trace evaluation without expected outputs.
func buildExpectedsForEval(evalCase *evalset.EvalCase) ([]*evalset.Invocation, error) {
	if evalCase.EvalMode == evalset.EvalModeTrace {
		if len(evalCase.Conversation) != 0 {
			if len(evalCase.ActualConversation) == 0 {
				return traceExpectedsForEval(evalCase.Conversation), nil
			}
			return evalCase.Conversation, nil
		}
		if len(evalCase.ActualConversation) != 0 {
			return traceExpectedsForEval(evalCase.ActualConversation), nil
		}
		return nil, errors.New("invalid eval case")
	}
	if len(evalCase.Conversation) == 0 {
		return nil, errors.New("invalid eval case")
	}
	return evalCase.Conversation, nil
}

// traceExpectedsForEval builds placeholder expected invocations that only preserve user inputs.
// This whitelist prevents trace outputs from being treated as reference answers and stays correct when Invocation gains new fields.
func traceExpectedsForEval(conversation []*evalset.Invocation) []*evalset.Invocation {
	expecteds := make([]*evalset.Invocation, len(conversation))
	for i, invocation := range conversation {
		if invocation == nil {
			expecteds[i] = &evalset.Invocation{}
			continue
		}
		expecteds[i] = &evalset.Invocation{
			InvocationID: invocation.InvocationID,
			UserContent:  invocation.UserContent,
		}
	}
	return expecteds
}
