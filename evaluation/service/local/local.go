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
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/internal/inference"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const reasonSeparator = ";"

// local is a local implementation of service.Service.
type local struct {
	runner            runner.Runner
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	registry          registry.Registry
	sessionIDSupplier func(ctx context.Context) string
}

// New returns a new local evaluation service.
// If no service.Option is provided, the service will use the default options.
func New(runner runner.Runner, opt ...service.Option) (service.Service, error) {
	opts := service.NewOptions(opt...)
	service := &local{
		runner:            runner,
		evalSetManager:    opts.EvalSetManager,
		evalResultManager: opts.EvalResultManager,
		registry:          opts.Registry,
		sessionIDSupplier: opts.SessionIDSupplier,
	}
	return service, nil
}

// Inference runs the agent for the requested eval cases and returns the inference results for each case.
func (s *local) Inference(ctx context.Context, req *service.InferenceRequest) ([]*service.InferenceResult, error) {
	if req == nil {
		return nil, errors.New("inference request is nil")
	}
	if req.AppName == "" {
		return nil, errors.New("app name is empty")
	}
	if req.EvalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	// Get the eval set.
	evalSet, err := s.evalSetManager.Get(ctx, req.AppName, req.EvalSetID)
	if err != nil {
		return nil, fmt.Errorf("get eval set: %w", err)
	}
	// If eval case IDs are provided, filter the eval cases to only include the specified eval case IDs.
	// Otherwise, use all eval cases in the eval set.
	evalCases := evalSet.EvalCases
	if len(req.EvalCaseIDs) > 0 {
		filteredEvalCases := evalCases[:0]
		for _, evalCase := range evalCases {
			if slices.Contains(req.EvalCaseIDs, evalCase.EvalID) {
				filteredEvalCases = append(filteredEvalCases, evalCase)
			}
		}
		evalCases = filteredEvalCases
	}
	// Run the agent for the requested eval cases and return the inference results for each case.
	inferenceResults := make([]*service.InferenceResult, 0, len(evalCases))
	for _, evalCase := range evalCases {
		inference, err := s.inferenceEvalCaseByMode(ctx, req, evalCase)
		if err != nil {
			return nil, fmt.Errorf("run inference for eval case %s: %w", evalCase.EvalID, err)
		}
		inferenceResults = append(inferenceResults, inference)
	}
	return inferenceResults, nil
}

func (s *local) inferenceEvalCaseByMode(ctx context.Context, req *service.InferenceRequest,
	evalCase *evalset.EvalCase) (*service.InferenceResult, error) {
	if evalCase.EvalMode == evalset.EvalModeTrace {
		return &service.InferenceResult{
			AppName:    req.AppName,
			EvalSetID:  req.EvalSetID,
			EvalCaseID: evalCase.EvalID,
			Inferences: evalCase.Conversation,
			SessionID:  s.sessionIDSupplier(ctx),
			Status:     status.EvalStatusPassed,
			EvalMode:   evalset.EvalModeTrace,
		}, nil
	}
	return s.inferenceEvalCase(ctx, req.AppName, req.EvalSetID, evalCase)
}

// inferenceEvalCase runs the agent for a single eval case and returns the inference result.
func (s *local) inferenceEvalCase(ctx context.Context, appName, evalSetID string,
	evalCase *evalset.EvalCase) (*service.InferenceResult, error) {
	sessionID := s.sessionIDSupplier(ctx)
	inferenceResult := &service.InferenceResult{
		AppName:    appName,
		EvalSetID:  evalSetID,
		EvalCaseID: evalCase.EvalID,
		SessionID:  sessionID,
		EvalMode:   evalset.EvalModeDefault,
	}
	inferences, err := inference.Inference(
		ctx,
		s.runner,
		evalCase.Conversation,
		evalCase.SessionInput,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}
	inferenceResult.Status = status.EvalStatusPassed
	inferenceResult.Inferences = inferences
	return inferenceResult, nil
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
	evalCaseResults := make([]*evalresult.EvalCaseResult, 0, len(req.InferenceResults))
	for _, inferenceResult := range req.InferenceResults {
		// Run the evaluation on the inference result and return the case evaluation result.
		result, err := s.evaluatePerCase(ctx, inferenceResult, req.EvaluateConfig)
		if err != nil {
			return nil, fmt.Errorf("evaluate inference result for eval case %s: %w", inferenceResult.EvalCaseID, err)
		}
		evalCaseResults = append(evalCaseResults, result)
	}
	evalSetResult := &evalresult.EvalSetResult{
		EvalSetID:         req.EvalSetID,
		EvalCaseResults:   evalCaseResults,
		CreationTimestamp: &epochtime.EpochTime{Time: time.Now()},
	}
	evalSetResultID, err := s.evalResultManager.Save(ctx, req.AppName, evalSetResult)
	if err != nil {
		return nil, fmt.Errorf("save eval set result: %w", err)
	}
	evalSetResult.EvalSetResultID = evalSetResultID
	evalSetResult.EvalSetResultName = evalSetResultID
	return evalSetResult, nil
}

// evaluatePerCase runs the evaluation on the inference result and returns the case evaluation result.
func (s *local) evaluatePerCase(ctx context.Context, inferenceResult *service.InferenceResult,
	evaluateConfig *service.EvaluateConfig) (*evalresult.EvalCaseResult, error) {
	if inferenceResult == nil {
		return nil, errors.New("inference result is nil")
	}
	if evaluateConfig == nil {
		return nil, errors.New("evaluate config is nil")
	}
	evalCase, err := s.evalSetManager.GetCase(ctx,
		inferenceResult.AppName,
		inferenceResult.EvalSetID,
		inferenceResult.EvalCaseID,
	)
	if err != nil {
		return nil, fmt.Errorf("get eval case: %w", err)
	}
	inputs, err := prepareCaseEvaluationInputs(inferenceResult, evalCase)
	if err != nil {
		return nil, err
	}
	// overallMetricResults collects the metric results for the entire eval case.
	overallMetricResults := make([]*evalresult.EvalMetricResult, 0, len(evaluateConfig.EvalMetrics))
	perInvocation := make([]*evalresult.EvalMetricResultPerInvocation, len(inputs.actuals))
	for i, actual := range inputs.actuals {
		perInvocation[i] = &evalresult.EvalMetricResultPerInvocation{
			ActualInvocation:   actual,
			ExpectedInvocation: inputs.expectedInvocationsForResult[i],
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
	actuals                      []*evalset.Invocation
	expecteds                    []*evalset.Invocation
	expectedInvocationsForResult []*evalset.Invocation
	userID                       string
}

func prepareCaseEvaluationInputs(inferenceResult *service.InferenceResult, evalCase *evalset.EvalCase) (*caseEvaluationInputs, error) {
	if len(evalCase.Conversation) == 0 {
		return nil, errors.New("invalid eval case")
	}
	evalMode := evalCase.EvalMode
	actuals := inferenceResult.Inferences
	expecteds := evalCase.Conversation
	expectedInvocationsForResult := evalCase.Conversation
	if evalMode == evalset.EvalModeTrace {
		expecteds = traceExpectedsForEval(evalCase.Conversation)
		expectedInvocationsForResult = make([]*evalset.Invocation, len(evalCase.Conversation))
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("inference count %d does not match expected conversation length %d",
			len(actuals), len(expecteds))
	}
	return &caseEvaluationInputs{
		actuals:                      actuals,
		expecteds:                    expecteds,
		expectedInvocationsForResult: expectedInvocationsForResult,
		userID:                       evalCase.SessionInput.UserID,
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
