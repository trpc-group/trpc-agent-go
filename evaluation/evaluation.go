package evaluation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// AgentEvaluator evaluates an agent against configured evaluation sets.
type AgentEvaluator interface {
	// Evaluate runs evaluation against the specified eval set.
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error)
}

// NewAgentEvaluator creates an AgentEvaluator with the supplied agent and options.
func NewAgentEvaluator(agent agent.Agent, opt ...Option) (AgentEvaluator, error) {
	if agent == nil {
		return nil, errors.New("agent is nil")
	}
	opts := newOptions(opt...)
	a := &agentEvaluator{
		agent:             agent,
		evalSetManager:    opts.evalSetManager,
		evalResultManager: opts.evalResultManager,
		metricManager:     opts.metricManager,
		evaluatorRegistry: opts.evaluatorRegistry,
		evalService:       opts.evalService,
		numRuns:           opts.numRuns,
	}
	if a.numRuns <= 0 {
		return nil, errors.New("num runs must be greater than 0")
	}
	if a.evalService == nil {
		evalService, err := local.New(a.agent,
			service.WithEvalSetManager(a.evalSetManager),
			service.WithEvalResultManager(a.evalResultManager),
			service.WithEvaluatorRegistry(a.evaluatorRegistry),
		)
		if err != nil {
			return nil, fmt.Errorf("create eval service: %w", err)
		}
		a.evalService = evalService
	}
	return a, nil
}

// agentEvaluator is the default implementation of AgentEvaluator.
type agentEvaluator struct {
	agent             agent.Agent
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	metricManager     metric.Manager
	evaluatorRegistry registry.Registry
	evalService       service.Service
	numRuns           int
}

// EvaluationResult contains the aggregated outcome of running an evaluation across multiple runs.
type EvaluationResult struct {
	AppName       string
	EvalSetID     string
	OverallStatus status.EvalStatus
	ExecutionTime time.Duration
	EvalCases     []*EvaluationCaseResult
}

// EvaluationCaseResult aggregates the outcome of a single eval case across multiple runs.
type EvaluationCaseResult struct {
	EvalCaseID      string
	OverallStatus   status.EvalStatus
	EvalCaseResults []*evalresult.EvalCaseResult
	MetricResults   []*metric.EvalMetricResult
}

// Evaluate evaluates agent against the specified eval set across multiple runs.
func (a *agentEvaluator) Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error) {
	if evalSetID == "" {
		return nil, fmt.Errorf("eval set id is not configured")
	}
	start := time.Now()
	// Gather per-case results.
	evalCases, err := a.collectCaseResults(ctx, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("collect eval case results: %w", err)
	}
	// Reduce the case statuses to determine the overall evaluation outcome.
	status, err := summarizeOverallStatus(evalCases)
	if err != nil {
		return nil, fmt.Errorf("summarize overall status: %w", err)
	}
	return &EvaluationResult{
		AppName:       a.agent.Info().Name,
		EvalSetID:     evalSetID,
		OverallStatus: status,
		ExecutionTime: time.Since(start),
		EvalCases:     evalCases,
	}, nil
}

// collectCaseResults runs evaluation on the specified eval set across multiple runs and groups results by case ID.
func (a *agentEvaluator) collectCaseResults(ctx context.Context, evalSetID string) ([]*EvaluationCaseResult, error) {
	// Due to multiple runs, an evaluation case may be evaluated multiple times and generate multiple evaluation
	// case results. So EvalCaseResult need to be grouped by case ID.
	// caseResultsByID is a map from case ID to a list of eval case results.
	caseResultsByID := make(map[string][]*evalresult.EvalCaseResult)
	for i := 0; i < a.numRuns; i++ {
		// Run evaluation on the specified eval set.
		caseResults, err := a.runEvaluation(ctx, evalSetID)
		if err != nil {
			return nil, fmt.Errorf("run evaluation: %w", err)
		}
		// Group results by case ID.
		for _, caseResult := range caseResults {
			caseResultsByID[caseResult.EvalID] = append(caseResultsByID[caseResult.EvalID], caseResult)
		}
	}
	evalCaseResults := make([]*EvaluationCaseResult, 0, len(caseResultsByID))
	for caseID, runs := range caseResultsByID {
		// Aggregate multiple runs for a single case.
		evalCaseResult, err := aggregateCaseRuns(caseID, runs)
		if err != nil {
			return nil, fmt.Errorf("aggregate case runs: %w", err)
		}
		evalCaseResults = append(evalCaseResults, evalCaseResult)
	}
	return evalCaseResults, nil
}

// runEvaluation runs inference and evaluation on the specified eval set.
func (a *agentEvaluator) runEvaluation(ctx context.Context, evalSetID string) ([]*evalresult.EvalCaseResult, error) {
	inferenceRequest := &service.InferenceRequest{
		AppName:   a.agent.Info().Name,
		EvalSetID: evalSetID,
	}
	// Run inference on the specified eval set.
	inferenceResults, err := a.evalService.Inference(ctx, inferenceRequest)
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}
	// Fetch the metric configuration that will be applied to these runs.
	evalMertrics, err := a.metricManager.List(ctx, a.agent.Info().Name, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("list metrics: %w", err)
	}
	evaluateRequest := &service.EvaluateRequest{
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMertrics: evalMertrics,
		},
	}
	// Run evaluation on the specified eval set.
	caseResults, err := a.evalService.Evaluate(ctx, evaluateRequest)
	if err != nil {
		return nil, fmt.Errorf("evaluate: %w", err)
	}
	return caseResults, nil
}

// aggregateCaseRuns aggregates the metric results from multiple runs of a single case.
func aggregateCaseRuns(caseID string, runs []*evalresult.EvalCaseResult) (*EvaluationCaseResult, error) {
	type aggregatedMetric struct {
		count     int
		score     float64
		threshold float64
	}
	// Group metrics results by metric name.
	aggregatedMetrics := make(map[string]*aggregatedMetric)
	for _, run := range runs {
		for _, metric := range run.OverallEvalMetricResults {
			if metric.Status == status.EvalStatusNotEvaluated {
				continue
			}
			if _, ok := aggregatedMetrics[metric.MetricName]; !ok {
				aggregatedMetrics[metric.MetricName] = &aggregatedMetric{threshold: metric.Threshold}
			}
			aggregatedMetrics[metric.MetricName].count++
			aggregatedMetrics[metric.MetricName].score += metric.Score
		}
	}
	// Aggregate metrics results by metric name.
	metricsResults := make([]*metric.EvalMetricResult, 0, len(aggregatedMetrics))
	for name, aggregatedMetric := range aggregatedMetrics {
		average := aggregatedMetric.score / float64(aggregatedMetric.count)
		evalStatus := status.EvalStatusFailed
		if average >= aggregatedMetric.threshold {
			evalStatus = status.EvalStatusPassed
		}
		metricsResults = append(metricsResults, &metric.EvalMetricResult{
			MetricName: name,
			Score:      average,
			Status:     evalStatus,
			Threshold:  aggregatedMetric.threshold,
		})
	}
	status, err := istatus.SummarizeMetricsStatus(metricsResults)
	if err != nil {
		return nil, fmt.Errorf("summarize metrics status: %w", err)
	}
	return &EvaluationCaseResult{
		EvalCaseID:      caseID,
		OverallStatus:   status,
		EvalCaseResults: runs,
		MetricResults:   metricsResults,
	}, nil
}

// summarizeOverallStatus summarizes the aggregate status across all cases in the evaluation.
func summarizeOverallStatus(cases []*EvaluationCaseResult) (status.EvalStatus, error) {
	evalStatuses := make([]status.EvalStatus, 0, len(cases))
	for _, c := range cases {
		if c != nil {
			evalStatuses = append(evalStatuses, c.OverallStatus)
		}
	}
	return istatus.Summarize(evalStatuses)
}
