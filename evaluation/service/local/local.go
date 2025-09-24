package local

import (
	"context"
	"fmt"
	"slices"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/internal/inference"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// localService is a local implementation of service.Service.
// It runs the agent in-process and applies the registered evaluators
// against the recorded conversations for each eval case.
type localService struct {
	agent             agent.Agent
	sessionService    session.Service
	artifactService   artifact.Service
	memoryService     memory.Service
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	registry          evaluator.Registry
	sessionIDSupplier func(ctx context.Context) string
}

// Ensure interface compliance
var _ service.Service = (*localService)(nil)

// New returns a new local evaluation service. The service ships with sensible
// defaults: an in-memory eval set manager, an in-memory result manager and a
// no-op evaluator registry. Callers can override these via options.
func New(agent agent.Agent, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	service := &localService{
		agent:             agent,
		sessionService:    opts.SessionService,
		artifactService:   opts.ArtifactService,
		memoryService:     opts.MemoryService,
		evalSetManager:    opts.EvalSetManager,
		evalResultManager: opts.EvalResultManager,
		registry:          opts.Registry,
		sessionIDSupplier: opts.SessionIDSupplier,
	}
	return service
}

// Inference executes agent runs for the requested eval cases and returns the
// recorded invocations produced during those runs.
func (s *localService) Inference(ctx context.Context, req *service.InferenceRequest) ([]*service.InferenceResult, error) {
	// Guard rail for callers that forgot to pass a request payload.
	if req == nil {
		return nil, fmt.Errorf("inference request is nil")
	}
	if s.evalSetManager == nil {
		return nil, fmt.Errorf("eval set manager is not configured")
	}
	if s.agent == nil {
		return nil, fmt.Errorf("agent is not configured")
	}
	evalSet, err := s.evalSetManager.Get(ctx, req.AppName, req.EvalSetID)
	if err != nil {
		return nil, err
	}
	evalCases := evalSet.EvalCases
	if len(req.EvalCaseIDs) > 0 {
		// Filter to the requested subset so we only launch runs we care about.
		filteredEvalCases := evalCases[:0]
		for _, evalCase := range evalCases {
			if slices.Contains(req.EvalCaseIDs, evalCase.EvalID) {
				filteredEvalCases = append(filteredEvalCases, evalCase)
			}
		}
		evalCases = filteredEvalCases
	}
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

func (s *localService) inferenceEvalCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) (*service.InferenceResult, error) {
	sessionID := s.sessionIDSupplier(ctx)
	inferenceResult := &service.InferenceResult{
		AppName:    appName,
		EvalSetID:  evalSetID,
		EvalCaseID: evalCase.EvalID,
		SessionID:  sessionID,
	}
	inferences, err := inference.Inference(
		ctx,
		evalCase.Conversation,
		s.agent,
		evalCase.SessionInput,
		sessionID,
		s.sessionService,
		s.artifactService,
		s.memoryService,
	)
	if err != nil {
		inferenceResult.Status = evalresult.EvalStatusFailed
		inferenceResult.ErrorMessage = err.Error()
		return inferenceResult, err
	}
	inferenceResult.Status = evalresult.EvalStatusPassed
	inferenceResult.Inferences = inferences
	return inferenceResult, nil
}

// Evaluate applies the configured metrics to inference results and returns the
// aggregated outcomes for each eval case.
func (s *localService) Evaluate(ctx context.Context, req *service.EvaluateRequest) ([]*evalresult.EvalCaseResult, error) {
	if req == nil {
		return nil, fmt.Errorf("evaluate request is nil")
	}
	evalCaseResults := make([]*evalresult.EvalCaseResult, 0, len(req.InferenceResults))
	for _, inferenceResult := range req.InferenceResults {
		// Each inference result may represent a single eval case across runs.
		result, err := s.evaluateSingleInferenceResult(ctx, inferenceResult, req.EvaluateConfig)
		if err != nil {
			return nil, fmt.Errorf("evaluate: %w", err)
		}
		evalCaseResults = append(evalCaseResults, result)
	}
	return evalCaseResults, nil
}

// evaluateSingleInferenceResult computes metric results for a single eval case
// and aggregates per-invocation scores into the summary structures expected by
// callers.
func (s *localService) evaluateSingleInferenceResult(ctx context.Context, inferenceResult *service.InferenceResult, evaluateConfig *service.EvaluateConfig) (*evalresult.EvalCaseResult, error) {
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
		return nil, err
	}
	if len(inferenceResult.Inferences) != len(evalCase.Conversation) {
		return nil, fmt.Errorf("inference count %d does not match expected conversation length %d", len(inferenceResult.Inferences), len(evalCase.Conversation))
	}
	overallMetricResults := make([]*evalresult.EvalMetricResult, len(evaluateConfig.EvalMertrics))
	perInvocation := make([]*evalresult.EvalMetricResultPerInvocation, len(inferenceResult.Inferences))
	for i := 0; i < len(inferenceResult.Inferences); i++ {
		perInvocation[i] = &evalresult.EvalMetricResultPerInvocation{
			ActualInvocation:   inferenceResult.Inferences[i],
			ExpectedInvocation: evalCase.Conversation[i],
			MetricResults:      make([]*evalresult.EvalMetricResult, len(evaluateConfig.EvalMertrics)),
		}
	}
	for i, evalMetric := range evaluateConfig.EvalMertrics {
		evaluationResult, metricErr := s.evaluateMetric(ctx, evalMetric, inferenceResult.Inferences, evalCase.Conversation)
		if metricErr != nil {
			return nil, metricErr
		}
		overallMetricResults[i] = &evalresult.EvalMetricResult{
			MetricName: evalMetric.MetricName,
			Threshold:  evalMetric.Threshold,
			Score:      evaluationResult.OverallScore,
			Status:     evaluationResult.OverallStatus,
		}
		if len(evaluationResult.PerInvocationResults) != len(perInvocation) {
			return nil, fmt.Errorf("metric %q returned %d invocation results, expected %d", evalMetric.MetricName, len(evaluationResult.PerInvocationResults), len(perInvocation))
		}
		for j := 0; j < len(evaluationResult.PerInvocationResults); j++ {
			perInvocation[j].MetricResults[i] = &evalresult.EvalMetricResult{
				MetricName: evalMetric.MetricName,
				Threshold:  evalMetric.Threshold,
				Score:      evaluationResult.PerInvocationResults[j].Score,
				Status:     evaluationResult.PerInvocationResults[j].Status,
			}
		}
	}
	finalStatus, err := s.generateFinalEvalStatus(overallMetricResults)
	if err != nil {
		return nil, err
	}
	var userID string
	if evalCase.SessionInput != nil {
		userID = evalCase.SessionInput.UserID
	}
	return &evalresult.EvalCaseResult{
		EvalSetID:                     inferenceResult.EvalSetID,
		EvalID:                        inferenceResult.EvalCaseID,
		FinalEvalStatus:               finalStatus,
		OverallEvalMetricResults:      overallMetricResults,
		EvalMetricResultPerInvocation: perInvocation,
		SessionID:                     inferenceResult.SessionID,
		UserID:                        userID,
	}, nil
}

// generateFinalEvalStatus reduces overall metric statuses to a single
// pass/fail/not-evaluated flag for the eval case summary.
func (s *localService) generateFinalEvalStatus(overallMetricResults []*evalresult.EvalMetricResult) (evalresult.EvalStatus, error) {
	finalStatus := evalresult.EvalStatusNotEvaluated
	for _, overall := range overallMetricResults {
		switch overall.Status {
		case evalresult.EvalStatusPassed:
			finalStatus = evalresult.EvalStatusPassed
		case evalresult.EvalStatusNotEvaluated:
			continue
		case evalresult.EvalStatusFailed:
			return evalresult.EvalStatusFailed, nil
		default:
			return evalresult.EvalStatusFailed, fmt.Errorf("unexpected eval status %v", overall.Status)
		}
	}
	return finalStatus, nil
}

func (s *localService) evaluateMetric(ctx context.Context, evalMetric *evalset.EvalMetric, actualInvocations []*evalset.Invocation, expectedInvocations []*evalset.Invocation) (*evaluator.EvaluationResult, error) {
	metricEvaluator, err := s.registry.Get(evalMetric.MetricName)
	if err != nil {
		return nil, err
	}
	return metricEvaluator.Evaluate(ctx, actualInvocations, expectedInvocations)
}
