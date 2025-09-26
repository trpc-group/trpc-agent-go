// Package local provides a local implementation of service.Service.
package local

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/internal/inference"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// local is a local implementation of service.Service.
type local struct {
	agent             agent.Agent
	sessionService    session.Service
	artifactService   artifact.Service
	memoryService     memory.Service
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	evaluatorRegistry registry.Registry
	sessionIDSupplier func(ctx context.Context) string
}

// New returns a new local evaluation service.
// If no service.Option is provided, the service will use the default options.
func New(agent agent.Agent, opt ...service.Option) (service.Service, error) {
	opts := service.NewOptions(opt...)
	service := &local{
		agent:             agent,
		sessionService:    opts.SessionService,
		artifactService:   opts.ArtifactService,
		memoryService:     opts.MemoryService,
		evalSetManager:    opts.EvalSetManager,
		evalResultManager: opts.EvalResultManager,
		evaluatorRegistry: opts.EvaluatorRegistry,
		sessionIDSupplier: opts.SessionIDSupplier,
	}
	return service, nil
}

// Inference executes the agent for the requested eval cases and returns recorded invocations produced during this run.
func (s *local) Inference(ctx context.Context, req *service.InferenceRequest) ([]*service.InferenceResult, error) {
	if req == nil {
		return nil, fmt.Errorf("inference request is nil")
	}
	// Get eval set.
	evalSet, err := s.evalSetManager.Get(ctx, req.AppName, req.EvalSetID)
	if err != nil {
		return nil, err
	}
	// Filter eval cases.
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
	// Run inference.
	inferenceResults := make([]*service.InferenceResult, 0, len(evalCases))
	for _, evalCase := range evalCases {
		inference, err := s.inferenceEvalCase(ctx, req.AppName, req.EvalSetID, evalCase)
		if err != nil {
			return nil, fmt.Errorf("inference: %w", err)
		}
		inferenceResults = append(inferenceResults, inference)
	}
	return inferenceResults, nil
}

// inferenceEvalCase runs the agent for a single eval case.
func (s *local) inferenceEvalCase(ctx context.Context, appName, evalSetID string,
	evalCase *evalset.EvalCase) (*service.InferenceResult, error) {
	sessionID := s.sessionIDSupplier(ctx)
	inferenceResult := &service.InferenceResult{
		AppName:    appName,
		EvalSetID:  evalSetID,
		EvalCaseID: evalCase.EvalID,
		SessionID:  sessionID,
	}
	inferences, err := inference.Inference(
		ctx,
		s.agent,
		evalCase.Conversation,
		evalCase.SessionInput,
		sessionID,
		runner.WithSessionService(s.sessionService),
		runner.WithArtifactService(s.artifactService),
		runner.WithMemoryService(s.memoryService),
	)
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}
	inferenceResult.Status = status.EvalStatusPassed
	inferenceResult.Inferences = inferences
	return inferenceResult, nil
}

// Evaluate applies the configured metrics to inference results and returns aggregated outcomes for each eval case.
func (s *local) Evaluate(ctx context.Context, req *service.EvaluateRequest) ([]*evalresult.EvalCaseResult, error) {
	if req == nil {
		return nil, fmt.Errorf("evaluate request is nil")
	}
	evalCaseResults := make([]*evalresult.EvalCaseResult, 0, len(req.InferenceResults))
	for _, inferenceResult := range req.InferenceResults {
		// Evaluate per case.
		result, err := s.evaluatePerCase(ctx, inferenceResult, req.EvaluateConfig)
		if err != nil {
			return nil, fmt.Errorf("evaluate case %s: %w", inferenceResult.EvalCaseID, err)
		}
		evalCaseResults = append(evalCaseResults, result)
	}
	return evalCaseResults, nil
}

// evaluatePerCase computes metric results for a single eval case.
func (s *local) evaluatePerCase(ctx context.Context, inferenceResult *service.InferenceResult,
	evaluateConfig *service.EvaluateConfig) (*evalresult.EvalCaseResult, error) {
	if inferenceResult == nil {
		return nil, fmt.Errorf("inference result is nil")
	}
	if evaluateConfig == nil {
		return nil, fmt.Errorf("evaluate config is nil")
	}
	evalCase, err := s.evalSetManager.GetCase(ctx,
		inferenceResult.AppName,
		inferenceResult.EvalSetID,
		inferenceResult.EvalCaseID,
	)
	if err != nil {
		return nil, fmt.Errorf("get eval case: %w", err)
	}
	if evalCase == nil || len(evalCase.Conversation) == 0 || evalCase.SessionInput == nil {
		return nil, errors.New("invalid eval case")
	}
	if len(inferenceResult.Inferences) != len(evalCase.Conversation) {
		return nil, fmt.Errorf("inference count %d does not match expected conversation length %d",
			len(inferenceResult.Inferences), len(evalCase.Conversation))
	}
	// overallMetricResults collects the results for each metric for the entire eval case.
	overallMetricResults := make([]*metric.EvalMetricResult, 0, len(evaluateConfig.EvalMertrics))
	// perInvocation collects the results for each metric for each invocation.
	perInvocation := make([]*metric.EvalMetricResultPerInvocation, 0, len(inferenceResult.Inferences))
	// Prepare a per-invocation container to hold metric results for each step of the conversation.
	for i := 0; i < len(inferenceResult.Inferences); i++ {
		perInvocation = append(perInvocation, &metric.EvalMetricResultPerInvocation{
			ActualInvocation:   inferenceResult.Inferences[i],
			ExpectedInvocation: evalCase.Conversation[i],
			MetricResults:      make([]*metric.EvalMetricResult, 0, len(evaluateConfig.EvalMertrics)),
		})
	}
	// Iterate through every configured metric and evaluate it.
	for _, evalMetric := range evaluateConfig.EvalMertrics {
		result, err := s.evaluateMetric(ctx, evalMetric, inferenceResult.Inferences, evalCase.Conversation)
		if err != nil {
			return nil, fmt.Errorf("evaluate metric %s: %w", evalMetric.MetricName, err)
		}
		overallMetricResults = append(overallMetricResults, &metric.EvalMetricResult{
			MetricName: evalMetric.MetricName,
			Threshold:  evalMetric.Threshold,
			Score:      result.OverallScore,
			Status:     result.OverallStatus,
		})
		if len(result.PerInvocationResults) != len(perInvocation) {
			return nil, fmt.Errorf("metric %s returned %d invocation results, expected %d", evalMetric.MetricName,
				len(result.PerInvocationResults), len(perInvocation))
		}
		for i, invocationResult := range result.PerInvocationResults {
			// Record the metric outcome for the corresponding invocation.
			perInvocation[i].MetricResults = append(perInvocation[i].MetricResults, &metric.EvalMetricResult{
				MetricName: evalMetric.MetricName,
				Threshold:  evalMetric.Threshold,
				Score:      invocationResult.Score,
				Status:     invocationResult.Status,
			})
		}
	}
	// Determine the final eval status after evaluating all metrics.
	finalStatus, err := istatus.SummarizeMetricsStatus(overallMetricResults)
	if err != nil {
		return nil, fmt.Errorf("generate final eval status: %w", err)
	}
	return &evalresult.EvalCaseResult{
		EvalSetID:                     inferenceResult.EvalSetID,
		EvalID:                        inferenceResult.EvalCaseID,
		FinalEvalStatus:               finalStatus,
		OverallEvalMetricResults:      overallMetricResults,
		EvalMetricResultPerInvocation: perInvocation,
		SessionID:                     inferenceResult.SessionID,
		UserID:                        evalCase.SessionInput.UserID,
	}, nil
}

// evaluateMetric locates the evaluator registered for the metric and executes it.
func (s *local) evaluateMetric(ctx context.Context, evalMetric *metric.EvalMetric,
	actualInvocations, expectedInvocations []*evalset.Invocation) (*evaluator.EvaluationResult, error) {
	metricEvaluator, err := s.evaluatorRegistry.Get(evalMetric.MetricName)
	if err != nil {
		return nil, fmt.Errorf("get metric evaluator %s: %w", evalMetric.MetricName, err)
	}
	return metricEvaluator.Evaluate(ctx, actualInvocations, expectedInvocations)
}
