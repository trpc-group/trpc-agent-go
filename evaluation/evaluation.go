package evaluation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
)

type AgentEvaluator interface {
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error)
}

func NewAgentEvaluator(agent agent.Agent, opt ...Option) (AgentEvaluator, error) {
	if agent == nil {
		return nil, errors.New("agent is nil")
	}
	opts := newOptions(opt...)
	a := &agentEvaluator{
		agent:             agent,
		evalService:       opts.evalService,
		evalSetManager:    opts.evalSetManager,
		evalResultManager: opts.evalResultManager,
		registry:          opts.registry,
		numRuns:           opts.numRuns,
		evalMetrics:       opts.evalMetrics,
	}
	if a.numRuns <= 0 {
		return nil, errors.New("num runs must be greater than 0")
	}
	if a.evalService == nil {
		a.evalService = local.New(a.agent,
			service.WithEvalSetManager(a.evalSetManager),
			service.WithEvalResultManager(a.evalResultManager),
			service.WithEvaluatorRegistry(a.registry),
		)
	}
	return a, nil
}

type agentEvaluator struct {
	agent             agent.Agent
	evalService       service.Service
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	registry          evaluator.Registry
	numRuns           int
	evalMetrics       []*evalset.EvalMetric
}

type EvaluationResult struct {
	AppName       string
	EvalSetID     string
	OverallStatus evalresult.EvalStatus
	ExecutionTime time.Duration
	EvalCases     []*EvaluationCaseResult
}

type EvaluationCaseResult struct {
	EvalCaseID      string
	OverallStatus   evalresult.EvalStatus
	EvalCaseResults []*evalresult.EvalCaseResult
	Metrics         []*evalresult.EvalMetricResult
}

func (a *agentEvaluator) Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error) {
	if evalSetID == "" {
		return nil, fmt.Errorf("eval set id is not configured")
	}

	start := time.Now()
	evalCases, err := a.executeRuns(ctx, evalSetID)
	if err != nil {
		return nil, err
	}

	status := summarizeOverallStatus(evalCases)

	return &EvaluationResult{
		AppName:       a.agent.Info().Name,
		EvalSetID:     evalSetID,
		OverallStatus: status,
		ExecutionTime: time.Since(start),
		EvalCases:     evalCases,
	}, nil
}

func (a *agentEvaluator) executeRuns(
	ctx context.Context,
	evalSetID string,
) ([]*EvaluationCaseResult, error) {
	caseResults := make(map[string][]*evalresult.EvalCaseResult)
	for i := 0; i < a.numRuns; i++ {
		results, err := a.runEvaluationOnce(ctx, evalSetID)
		if err != nil {
			return nil, err
		}
		for _, res := range results {
			caseResults[res.EvalID] = append(caseResults[res.EvalID], res)
		}
	}
	results := make([]*EvaluationCaseResult, 0, len(caseResults))
	for caseID, runs := range caseResults {
		result, err := assembleCaseResults(caseID, runs)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func assembleCaseResults(caseID string, runs []*evalresult.EvalCaseResult) (*EvaluationCaseResult, error) {
	type data struct {
		count     int
		score     float64
		threshold float64
	}
	metricsMap := make(map[string]*data)
	for _, result := range runs {
		for _, metric := range result.OverallEvalMetricResults {
			if metric.Status != evalresult.EvalStatusNotEvaluated {
				if _, ok := metricsMap[metric.MetricName]; !ok {
					metricsMap[metric.MetricName] = &data{threshold: metric.Threshold}
				}
				metricsMap[metric.MetricName].count++
				metricsMap[metric.MetricName].score += metric.Score
			}
		}
	}
	metrics := make([]*evalresult.EvalMetricResult, 0, len(metricsMap))
	for name, agg := range metricsMap {
		average := agg.score / float64(agg.count)
		status := evalresult.EvalStatusNotEvaluated
		if average >= agg.threshold {
			status = evalresult.EvalStatusPassed
		} else {
			status = evalresult.EvalStatusFailed
		}
		metrics = append(metrics, &evalresult.EvalMetricResult{
			MetricName: name,
			Score:      average,
			Status:     status,
			Threshold:  agg.threshold,
		})
	}
	caseStatus := summarizeMetricsStatus(metrics)
	if caseStatus == evalresult.EvalStatusNotEvaluated {
		caseStatus = summarizeRunStatus(runs)
	}
	return &EvaluationCaseResult{
		EvalCaseID:      caseID,
		OverallStatus:   caseStatus,
		EvalCaseResults: runs,
		Metrics:         metrics,
	}, nil
}

func (a *agentEvaluator) runEvaluationOnce(ctx context.Context, evalSetID string) ([]*evalresult.EvalCaseResult, error) {
	inferenceResults, err := a.evalService.Inference(ctx, &service.InferenceRequest{
		AppName:   a.agent.Info().Name,
		EvalSetID: evalSetID,
	})
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}

	caseResults, err := a.evalService.Evaluate(ctx, &service.EvaluateRequest{
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMertrics: a.evalMetrics,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate: %w", err)
	}

	return caseResults, nil
}

func summarizeRunStatus(results []*evalresult.EvalCaseResult) evalresult.EvalStatus {
	statuses := make([]evalresult.EvalStatus, 0, len(results))
	for _, res := range results {
		if res != nil {
			statuses = append(statuses, res.FinalEvalStatus)
		}
	}
	return reduceStatuses(statuses)
}

func summarizeMetricsStatus(metrics []*evalresult.EvalMetricResult) evalresult.EvalStatus {
	statuses := make([]evalresult.EvalStatus, 0, len(metrics))
	for _, metric := range metrics {
		if metric != nil {
			statuses = append(statuses, metric.Status)
		}
	}
	return reduceStatuses(statuses)
}

func summarizeOverallStatus(cases []*EvaluationCaseResult) evalresult.EvalStatus {
	statuses := make([]evalresult.EvalStatus, 0, len(cases))
	for _, c := range cases {
		if c != nil {
			statuses = append(statuses, c.OverallStatus)
		}
	}
	return reduceStatuses(statuses)
}

func reduceStatuses(statuses []evalresult.EvalStatus) evalresult.EvalStatus {
	status := evalresult.EvalStatusNotEvaluated
	for _, s := range statuses {
		switch s {
		case evalresult.EvalStatusFailed:
			return evalresult.EvalStatusFailed
		case evalresult.EvalStatusPassed:
			if status != evalresult.EvalStatusFailed {
				status = evalresult.EvalStatusPassed
			}
		}
	}
	return status
}
