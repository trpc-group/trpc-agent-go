package evaluation

import (
	"context"
	"time"
)

// EvalStatus represents the status of an evaluation
type EvalStatus int

const (
	EvalStatusUnknown EvalStatus = iota
	EvalStatusPassed
	EvalStatusFailed
	EvalStatusNotEvaluated
)

func (s EvalStatus) String() string {
	switch s {
	case EvalStatusPassed:
		return "passed"
	case EvalStatusFailed:
		return "failed"
	case EvalStatusNotEvaluated:
		return "not_evaluated"
	default:
		return "unknown"
	}
}

// PrebuiltMetrics defines commonly used evaluation metrics
type PrebuiltMetrics string

const (
	MetricToolTrajectoryAvgScore  PrebuiltMetrics = "tool_trajectory_avg_score"
	MetricResponseEvaluationScore PrebuiltMetrics = "response_evaluation_score"
	MetricResponseMatchScore      PrebuiltMetrics = "response_match_score"
	MetricSafetyV1                PrebuiltMetrics = "safety_v1"
	MetricFinalResponseMatchV2    PrebuiltMetrics = "final_response_match_v2"
)

// Default configuration values
const (
	DefaultMaxConcurrency = 10
	DefaultTimeoutSeconds = 300
	DefaultMaxTokens      = 2048
	DefaultTemperature    = 0.7
	DefaultJudgeModel     = "gemini-2.5-flash"
	DefaultNumSamples     = 1
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

// Re-export key types from sub-packages
// Note: Actual re-exports would be added here when implementing

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
type EvalMetric struct {
	// MetricName is the name of the metric
	MetricName string `json:"metric_name"`

	// Threshold is the threshold value for this metric
	Threshold float64 `json:"threshold"`

	// JudgeModelOptions contains options for the judge model
	JudgeModelOptions *JudgeModelOptions `json:"judge_model_options,omitempty"`

	// Config contains metric-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`
}

// JudgeModelOptions contains options for a judge model
type JudgeModelOptions struct {
	// JudgeModel is the model to use for evaluation
	JudgeModel string `json:"judge_model"`

	// NumSamples is the number of times to sample the model
	NumSamples *int `json:"num_samples,omitempty"`
}

// AgentEvaluator provides a simplified interface for evaluating agents
// This is the main entry point for users, similar to Python ADK's AgentEvaluator
type AgentEvaluator struct {
	// Private fields to avoid circular imports
	// Implementation will be provided via methods
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

// AgentRunner defines the interface for running agents during evaluation
type AgentRunner interface {
	// RunAgent executes an agent with the given eval case and returns invocations
	RunAgent(ctx context.Context, evalCase interface{}) (interface{}, error)

	// GetAgentInfo returns information about the agent
	GetAgentInfo() *AgentInfo
}

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
	OverallStatus EvalStatus `json:"overall_status"`

	// MetricResults contains results for each metric
	MetricResults map[string]MetricSummary `json:"metric_results"`

	// TotalCases is the number of eval cases processed
	TotalCases int `json:"total_cases"`

	// ExecutionTime is the total time taken
	ExecutionTime time.Duration `json:"execution_time"`
}

// MetricSummary contains summary information for a metric
type MetricSummary struct {
	MetricName   string     `json:"metric_name"`
	OverallScore *float64   `json:"overall_score,omitempty"`
	Threshold    float64    `json:"threshold"`
	Status       EvalStatus `json:"status"`
	NumSamples   int        `json:"num_samples"`
}

// NewAgentEvaluator creates a new AgentEvaluator with default configuration
func NewAgentEvaluator() *AgentEvaluator {
	return &AgentEvaluator{}
}

// NewAgentEvaluatorWithConfig creates a new AgentEvaluator with custom configuration
func NewAgentEvaluatorWithConfig(config AgentEvaluatorConfig) *AgentEvaluator {
	return &AgentEvaluator{}
}

// Evaluate evaluates an agent using eval data from file or directory
func (ae *AgentEvaluator) Evaluate(ctx context.Context, agentRunner AgentRunner, evalDataPath string) (*EvaluationResult, error) {
	// TODO: Implementation will be added when needed
	// For now, return a simple success result
	return &EvaluationResult{
		OverallStatus: EvalStatusPassed,
		MetricResults: make(map[string]MetricSummary),
		TotalCases:    0,
		ExecutionTime: 0,
	}, nil
}
