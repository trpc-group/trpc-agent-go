package evaluation

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	localservice "trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// 注意：EvalStatus 已在 evalresult 包中定义，避免重复定义。

// 注意：通用的指标名应定义在 metric 包中（见 metric/names.go）。

// 默认配置值（如需适配，请下沉至具体子模块）。
const (
	DefaultMaxConcurrency = 10
	DefaultTimeoutSeconds = 300
)

// Error messages
const (
	ErrEvalSetNotFound   = "eval set not found"
	ErrEvalCaseNotFound  = "eval case not found"
	ErrEvaluatorNotFound = "evaluator not found"
	ErrInvalidMetric     = "invalid metric configuration"
	ErrInferenceTimeout  = "inference timeout"
	ErrEvaluationTimeout = "evaluation timeout"
)

// This file serves as the main entry point for the evaluation package
// It re-exports key types and interfaces for easier access

// 评估流、评估器与指标类型请统一复用 service/evaluator/metric 包，避免在入口包重复定义。

// Config represents the configuration for the evaluation system
type Config struct {
	// MaxConcurrency controls the maximum number of concurrent evaluations
	MaxConcurrency int `json:"max_concurrency" yaml:"max_concurrency"`

	// Timeout for individual evaluation operations
	TimeoutSeconds int `json:"timeout_seconds" yaml:"timeout_seconds"`

	// EnableMetrics controls whether to collect evaluation metrics
	EnableMetrics bool `json:"enable_metrics" yaml:"enable_metrics"`
}

// EvalMetric represents a metric used in evaluation
// 指标定义请使用 metric 包中的类型。

// AgentEvaluator provides a simplified interface for evaluating agents
// This is the main entry point for users, similar to Python ADK's AgentEvaluator
type AgentEvaluator struct {
	// service coordinates inference and evaluation
	service service.EvaluationService
	// registry provides mapping from metric name to evaluator
	registry *evaluator.Registry
	// cfg controls high-level behavior
	cfg AgentEvaluatorConfig
}

// AgentEvaluatorConfig contains configuration for AgentEvaluator
type AgentEvaluatorConfig struct {
	// NumRuns is the number of times to run each eval case
	NumRuns int `json:"num_runs"`

	// PrintDetailedResults controls whether to print detailed results
	PrintDetailedResults bool `json:"print_detailed_results"`

	// DefaultCriteria contains default evaluation criteria
	DefaultCriteria map[string]float64 `json:"default_criteria"`
}

// Note: We use runner.Runner interface from the runner package for agent execution

// AgentInfo contains information about an agent
type AgentInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// EvaluationResult represents the final evaluation result
type EvaluationResult struct {
	// OverallStatus indicates if the evaluation passed or failed
	OverallStatus evalresult.EvalStatus `json:"overall_status"`

	// MetricResults contains results for each metric
	MetricResults map[string]MetricSummary `json:"metric_results"`

	// TotalCases is the number of eval cases processed
	TotalCases int `json:"total_cases"`

	// ExecutionTime is the total time taken
	ExecutionTime time.Duration `json:"execution_time"`
}

// MetricSummary contains summary information for a metric
type MetricSummary struct {
	MetricName   string                `json:"metric_name"`
	OverallScore *float64              `json:"overall_score,omitempty"`
	Threshold    float64               `json:"threshold"`
	Status       evalresult.EvalStatus `json:"status"`
	NumSamples   int                   `json:"num_samples"`
}

// Option 为 AgentEvaluator 的可选配置。
type Option func(*AgentEvaluator)

// WithEvaluationService 指定评估服务实现。
func WithEvaluationService(s service.EvaluationService) Option {
	return func(ae *AgentEvaluator) { ae.service = s }
}

// WithRegistry 指定评估器注册表。
func WithRegistry(r *evaluator.Registry) Option { return func(ae *AgentEvaluator) { ae.registry = r } }

// WithAgentEvaluatorConfig 指定 AgentEvaluator 的基础配置（如 NumRuns、打印细节、默认阈值等）。
func WithAgentEvaluatorConfig(config AgentEvaluatorConfig) Option {
	return func(ae *AgentEvaluator) { ae.cfg = config }
}

// NewAgentEvaluator 使用 Option 模式创建 AgentEvaluator。
// 如未提供，将注入本地服务与默认注册表，并设置合理默认配置。
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
	if ae.service == nil {
		ae.service = localservice.New()
	}
	if ae.registry == nil {
		ae.registry = evaluator.NewRegistry()
	}
	return ae
}

// WithService sets the EvaluationService on the AgentEvaluator.
func (ae *AgentEvaluator) WithService(s service.EvaluationService) *AgentEvaluator {
	ae.service = s
	return ae
}

// WithRegistry sets the Registry on the AgentEvaluator.
func (ae *AgentEvaluator) WithRegistry(r *evaluator.Registry) *AgentEvaluator {
	ae.registry = r
	return ae
}

// Evaluate evaluates an agent using the runner
func (ae *AgentEvaluator) Evaluate(ctx context.Context, runner runner.Runner) (*EvaluationResult, error) {
	start := time.Now()
	if ae.service == nil {
		return nil, errors.New("evaluation service not configured")
	}

	// Placeholder minimal implementation to keep API stable.
	// A full implementation should:
	// 1) Discover or load eval set (and criteria)
	// 2) Build N inference requests (NumRuns times)
	// 3) Collect inference results
	// 4) Build EvaluateRequest and stream results
	// 5) Aggregate per-metric scores and set OverallStatus
	res := &EvaluationResult{
		OverallStatus: evalresult.EvalStatusNotEvaluated,
		MetricResults: make(map[string]MetricSummary),
		TotalCases:    0,
		ExecutionTime: time.Since(start),
	}
	return res, nil
}
