package evaluation

import (
    "context"
    "errors"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/service"
    localservice "trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Config represents the configuration for the evaluation system.
type Config struct {
    MaxConcurrency  int  `json:"max_concurrency" yaml:"max_concurrency"`
    TimeoutSeconds  int  `json:"timeout_seconds" yaml:"timeout_seconds"`
    EnableMetrics   bool `json:"enable_metrics" yaml:"enable_metrics"`
}

// AgentEvaluator provides a simplified interface for evaluating agents.
type AgentEvaluator struct {
    service  service.EvaluationService
    registry *evaluator.Registry
    cfg      AgentEvaluatorConfig
}

// AgentEvaluatorConfig contains configuration for AgentEvaluator.
type AgentEvaluatorConfig struct {
    NumRuns              int                        `json:"num_runs"`
    PrintDetailedResults bool                       `json:"print_detailed_results"`
    DefaultCriteria      map[string]float64         `json:"default_criteria"`

    AppName         string                     `json:"app_name"`
    EvalSetID       string                     `json:"eval_set_id"`
    EvalCaseIDs     []string                   `json:"eval_case_ids"`
    Metrics         []metric.EvalMetric        `json:"metrics"`
    InferenceConfig service.InferenceConfig    `json:"inference_config"`
    ConcurrencyConfig service.ConcurrencyConfig `json:"concurrency_config"`
}

// MetricSummary contains summary information for a metric.
type MetricSummary struct {
    MetricName   string                `json:"metric_name"`
    OverallScore *float64              `json:"overall_score,omitempty"`
    Threshold    float64               `json:"threshold"`
    Status       evalresult.EvalStatus `json:"status"`
    NumSamples   int                   `json:"num_samples"`
}

// EvaluationResult represents the final evaluation result.
type EvaluationResult struct {
    OverallStatus evalresult.EvalStatus `json:"overall_status"`
    MetricResults map[string]MetricSummary `json:"metric_results"`
    TotalCases    int                   `json:"total_cases"`
    ExecutionTime time.Duration         `json:"execution_time"`
}

// Option configures an AgentEvaluator instance.
type Option func(*AgentEvaluator)

// WithEvaluationService overrides the evaluation service implementation.
func WithEvaluationService(s service.EvaluationService) Option {
    return func(ae *AgentEvaluator) { ae.service = s }
}

// WithRegistry overrides the evaluator registry used by AgentEvaluator.
func WithRegistry(r *evaluator.Registry) Option {
    return func(ae *AgentEvaluator) {
        ae.registry = r
        if ls, ok := ae.service.(*localservice.Service); ok && r != nil {
            ls.SetRegistry(r)
        }
    }
}

// WithAgentEvaluatorConfig configures core evaluation parameters.
func WithAgentEvaluatorConfig(cfg AgentEvaluatorConfig) Option {
    return func(ae *AgentEvaluator) { ae.cfg = cfg }
}

// NewAgentEvaluator creates a new AgentEvaluator applying the provided options.
func NewAgentEvaluator(opts ...Option) *AgentEvaluator {
    ae := &AgentEvaluator{
        cfg: AgentEvaluatorConfig{
            NumRuns:              1,
            PrintDetailedResults: true,
            DefaultCriteria:      map[string]float64{},
        },
    }
    for _, opt := range opts {
        opt(ae)
    }

    if ae.registry == nil {
        ae.registry = DefaultRegistry()
    }

    if ae.service == nil {
        ae.service = localservice.New(localservice.WithEvaluatorRegistry(ae.registry))
    } else if ls, ok := ae.service.(*localservice.Service); ok {
        ls.SetRegistry(ae.registry)
    }

    return ae
}

// WithService replaces the evaluation service.
func (ae *AgentEvaluator) WithService(s service.EvaluationService) *AgentEvaluator {
    ae.service = s
    if ls, ok := s.(*localservice.Service); ok && ae.registry != nil {
        ls.SetRegistry(ae.registry)
    }
    return ae
}

// WithRegistry sets a new registry and updates the underlying service if possible.
func (ae *AgentEvaluator) WithRegistry(r *evaluator.Registry) *AgentEvaluator {
    ae.registry = r
    if ls, ok := ae.service.(*localservice.Service); ok && r != nil {
        ls.SetRegistry(r)
    }
    return ae
}

// Evaluate evaluates an agent using the provided runner.
func (ae *AgentEvaluator) Evaluate(ctx context.Context, run runner.Runner) (*EvaluationResult, error) {
    start := time.Now()

    if ae.service == nil {
        return nil, errors.New("evaluation service not configured")
    }
    if ae.registry == nil {
        ae.registry = DefaultRegistry()
        if ls, ok := ae.service.(*localservice.Service); ok {
            ls.SetRegistry(ae.registry)
        }
    }
    if run == nil {
        return nil, errors.New("runner is nil")
    }
    if ae.cfg.EvalSetID == "" {
        return nil, errors.New("eval set id is required")
    }

    numRuns := ae.cfg.NumRuns
    if numRuns <= 0 {
        numRuns = 1
    }

    metrics := ae.prepareMetrics()

    inferenceResults := make([]service.InferenceResult, 0)
    for i := 0; i < numRuns; i++ {
        req := &service.InferenceRequest{
            AppName:         ae.cfg.AppName,
            EvalSetID:       ae.cfg.EvalSetID,
            EvalCaseIDs:     ae.cfg.EvalCaseIDs,
            InferenceConfig: ae.cfg.InferenceConfig,
            Runner:          run,
        }
        ch, err := ae.service.PerformInference(ctx, req)
        if err != nil {
            return nil, err
        }
        for res := range ch {
            if res == nil {
                continue
            }
            inferenceResults = append(inferenceResults, *res)
        }
    }

    if len(inferenceResults) == 0 {
        return &EvaluationResult{
            OverallStatus: evalresult.EvalStatusNotEvaluated,
            MetricResults: make(map[string]MetricSummary),
            TotalCases:    0,
            ExecutionTime: time.Since(start),
        }, nil
    }

    evalReq := &service.EvaluateRequest{
        InferenceResults: inferenceResults,
        EvaluateConfig: service.EvaluateConfig{
            Metrics:            metrics,
            InferenceConfig:    ae.cfg.InferenceConfig,
            ConcurrencyConfig:  ae.cfg.ConcurrencyConfig,
        },
    }

    evalCh, err := ae.service.Evaluate(ctx, evalReq)
    if err != nil {
        return nil, err
    }

    agg := newMetricAggregator(metrics)
    overallStatus := evalresult.EvalStatusPassed
    totalCases := 0

    for res := range evalCh {
        if res == nil {
            continue
        }
        totalCases++
        agg.AddCaseResults(res.OverallEvalMetricResults)
        overallStatus = combineStatus(overallStatus, res.FinalEvalStatus)
    }

    if totalCases == 0 {
        overallStatus = evalresult.EvalStatusNotEvaluated
    }

    result := &EvaluationResult{
        OverallStatus: overallStatus,
        MetricResults: agg.Summary(),
        TotalCases:    totalCases,
        ExecutionTime: time.Since(start),
    }
    return result, nil
}

func (ae *AgentEvaluator) prepareMetrics() []metric.EvalMetric {
    if len(ae.cfg.Metrics) > 0 {
        return ae.cfg.Metrics
    }
    metrics := make([]metric.EvalMetric, 0, len(ae.cfg.DefaultCriteria))
    for name, threshold := range ae.cfg.DefaultCriteria {
        metrics = append(metrics, metric.EvalMetric{MetricName: name, Threshold: threshold})
    }
    if len(metrics) == 0 {
        metrics = append(metrics, metric.EvalMetric{
            MetricName: metric.MetricResponseMatchScore,
            Threshold:  1.0,
        })
    }
    return metrics
}

// metricAggregator accumulates metric statistics across cases.
type metricAggregator struct {
    entries map[string]*metricAggregate
}

type metricAggregate struct {
    sum       float64
    count     int
    threshold float64
    status    evalresult.EvalStatus
}

func newMetricAggregator(metrics []metric.EvalMetric) *metricAggregator {
    ma := &metricAggregator{entries: make(map[string]*metricAggregate, len(metrics))}
    for _, m := range metrics {
        ma.entries[m.MetricName] = &metricAggregate{
            threshold: m.Threshold,
            status:    evalresult.EvalStatusPassed,
        }
    }
    return ma
}

func (m *metricAggregator) AddCaseResults(results []evalresult.EvalMetricResult) {
    for _, res := range results {
        entry := m.entries[res.MetricName]
        if entry == nil {
            // Ignore metrics that were not part of the request configuration.
            continue
        }
        if res.Score != nil {
            entry.sum += *res.Score
            entry.count++
        }
        entry.status = combineStatus(entry.status, res.Status)
        if entry.threshold == 0 {
            entry.threshold = res.Threshold
        }
    }
}

func (m *metricAggregator) Summary() map[string]MetricSummary {
    summaries := make(map[string]MetricSummary, len(m.entries))
    for name, entry := range m.entries {
        var scorePtr *float64
        if entry.count > 0 {
            avg := entry.sum / float64(entry.count)
            scorePtr = &avg
        }
        status := entry.status
        if entry.count == 0 && status == evalresult.EvalStatusPassed {
            status = evalresult.EvalStatusNotEvaluated
        }
        summaries[name] = MetricSummary{
            MetricName:   name,
            OverallScore: scorePtr,
            Threshold:    entry.threshold,
            Status:       status,
            NumSamples:   entry.count,
        }
    }
    return summaries
}

func combineStatus(current, incoming evalresult.EvalStatus) evalresult.EvalStatus {
    if incoming == evalresult.EvalStatusFailed {
        return evalresult.EvalStatusFailed
    }
    if current == evalresult.EvalStatusFailed {
        return evalresult.EvalStatusFailed
    }
    if incoming == evalresult.EvalStatusUnknown && current == evalresult.EvalStatusPassed {
        return evalresult.EvalStatusNotEvaluated
    }
    if incoming == evalresult.EvalStatusNotEvaluated && current == evalresult.EvalStatusPassed {
        return evalresult.EvalStatusNotEvaluated
    }
    return current
}
