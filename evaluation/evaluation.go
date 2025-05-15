// Package evaluation provides tools for evaluating agent performance.
// It supports evaluating tool usage accuracy, response quality, and other metrics.
package evaluation

import (
	"context"
	"fmt"
	"time"
)

// Evaluator defines the interface for agent evaluation components.
type Evaluator interface {
	// Evaluate evaluates an agent against provided test cases.
	Evaluate(ctx context.Context, input EvaluationInput) (*Result, error)

	// Name returns the name of the evaluator.
	Name() string
}

// Result represents the results of an evaluation run.
type Result struct {
	// ID is the unique identifier for this evaluation run.
	ID string `json:"id"`

	// Timestamp records when the evaluation was performed.
	Timestamp time.Time `json:"timestamp"`

	// Metrics contains the evaluation metrics.
	Metrics Metrics `json:"metrics"`

	// Details contains additional information about the evaluation.
	Details map[string]interface{} `json:"details,omitempty"`

	// Successes is the number of successful test cases.
	Successes int `json:"successes"`

	// Failures is the number of failed test cases.
	Failures int `json:"failures"`

	// Total is the total number of test cases.
	Total int `json:"total"`
}

// Metrics contains evaluation metrics.
type Metrics struct {
	// ResponseQuality is a score from 0-1 representing response quality.
	ResponseQuality float64 `json:"response_quality,omitempty"`

	// Correctness is a score from 0-1 representing factual correctness.
	Correctness float64 `json:"correctness,omitempty"`

	// Relevance is a score from 0-1 representing relevance to the query.
	Relevance float64 `json:"relevance,omitempty"`

	// ToolUsage is a score from 0-1 representing proper tool usage.
	ToolUsage float64 `json:"tool_usage,omitempty"`

	// Custom contains additional custom metrics.
	Custom map[string]interface{} `json:"custom,omitempty"`
}

// EvaluationInput contains input for an evaluation.
type EvaluationInput struct {
	// TestCases contains the test cases to evaluate.
	TestCases []TestCase `json:"test_cases"`

	// Config contains configuration for the evaluation.
	Config Config `json:"config"`
}

// TestCase represents a single test case for evaluation.
type TestCase struct {
	// ID is a unique identifier for the test case.
	ID string `json:"id"`

	// Input is the query or prompt for the agent.
	Input string `json:"input"`

	// ExpectedOutput is the expected response from the agent.
	ExpectedOutput string `json:"expected_output,omitempty"`

	// ExpectedToolCalls describes the expected tool usage.
	ExpectedToolCalls []ToolCall `json:"expected_tool_calls,omitempty"`
}

// ToolCall represents a tool invocation.
type ToolCall struct {
	// Name is the name of the tool.
	Name string `json:"name"`

	// Input is the input provided to the tool.
	Input map[string]interface{} `json:"input"`

	// ExpectedOutput is the expected output from the tool.
	ExpectedOutput interface{} `json:"expected_output,omitempty"`
}

// Config contains configuration for evaluations.
type Config struct {
	// MinResponseQuality is the minimum required score for response quality.
	MinResponseQuality float64 `json:"min_response_quality"`

	// MinToolUsageScore is the minimum required score for tool usage accuracy.
	MinToolUsageScore float64 `json:"min_tool_usage_score"`

	// MinCorrectness is the minimum required score for factual correctness.
	MinCorrectness float64 `json:"min_correctness"`

	// MinRelevance is the minimum required score for relevance.
	MinRelevance float64 `json:"min_relevance"`

	// Runs is the number of evaluation runs to perform.
	Runs int `json:"runs"`

	// Custom contains additional custom configuration.
	Custom map[string]interface{} `json:"custom,omitempty"`
}

// DefaultConfig returns a default configuration for evaluations.
func DefaultConfig() Config {
	return Config{
		MinResponseQuality: 0.7,
		MinToolUsageScore:  0.8,
		MinCorrectness:     0.8,
		MinRelevance:       0.7,
		Runs:               1,
	}
}

// NewResult creates a new evaluation result.
func NewResult(id string, metrics Metrics, successes, failures, total int) *Result {
	return &Result{
		ID:        id,
		Timestamp: time.Now(),
		Metrics:   metrics,
		Details:   make(map[string]interface{}),
		Successes: successes,
		Failures:  failures,
		Total:     total,
	}
}

// Passed determines if the evaluation passed based on configured thresholds.
func (r *Result) Passed(config Config) bool {
	return r.Metrics.ResponseQuality >= config.MinResponseQuality &&
		r.Metrics.ToolUsage >= config.MinToolUsageScore &&
		r.Metrics.Correctness >= config.MinCorrectness &&
		r.Metrics.Relevance >= config.MinRelevance &&
		r.Failures == 0
}

// String returns a string representation of the evaluation result.
func (r *Result) String() string {
	passRate := float64(r.Successes) / float64(r.Total) * 100
	return fmt.Sprintf(
		"Evaluation ID: %s\n"+
			"Timestamp: %s\n"+
			"Success Rate: %.1f%% (%d/%d)\n"+
			"Metrics:\n"+
			"  Response Quality: %.2f\n"+
			"  Correctness: %.2f\n"+
			"  Relevance: %.2f\n"+
			"  Tool Usage: %.2f\n",
		r.ID,
		r.Timestamp.Format(time.RFC3339),
		passRate,
		r.Successes,
		r.Total,
		r.Metrics.ResponseQuality,
		r.Metrics.Correctness,
		r.Metrics.Relevance,
		r.Metrics.ToolUsage,
	)
}
