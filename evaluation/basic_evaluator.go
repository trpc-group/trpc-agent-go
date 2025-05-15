package evaluation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BasicEvaluator provides a simple implementation of the Evaluator interface.
type BasicEvaluator struct {
	name   string
	logger *slog.Logger
	config Config
}

// NewBasicEvaluator creates a new basic evaluator with the given name and configuration.
func NewBasicEvaluator(name string, config Config, logger *slog.Logger) *BasicEvaluator {
	if logger == nil {
		logger = slog.Default()
	}

	if name == "" {
		name = "basic-evaluator"
	}

	return &BasicEvaluator{
		name:   name,
		logger: logger,
		config: config,
	}
}

// Name returns the name of the evaluator.
func (e *BasicEvaluator) Name() string {
	return e.name
}

// Evaluate evaluates test cases and returns results.
func (e *BasicEvaluator) Evaluate(ctx context.Context, input EvaluationInput) (*Result, error) {
	e.logger.Info("Starting evaluation", "evaluator", e.name, "test_cases", len(input.TestCases))

	// If no config is provided in the input, use the evaluator's config
	config := input.Config
	if config.Runs == 0 {
		config = e.config
	}

	metrics := Metrics{
		ResponseQuality: 1.0, // Start with perfect scores
		Correctness:     1.0,
		Relevance:       1.0,
		ToolUsage:       1.0,
	}

	runID := fmt.Sprintf("run-%s", uuid.New().String()[:8])
	totalTests := len(input.TestCases)
	successes := 0
	failures := 0

	// Track the start time
	startTime := time.Now()

	// Process each test case
	for i, tc := range input.TestCases {
		e.logger.Debug("Evaluating test case", "index", i, "id", tc.ID)

		// Simulate evaluation (in a real implementation, this would evaluate the actual responses)
		caseQuality := 0.9 // Simulated scores
		caseCorrectness := 0.85
		caseRelevance := 0.95
		caseToolUsage := 0.8

		// Update metrics (simple averaging - a real implementation would be more sophisticated)
		metrics.ResponseQuality = updateMetric(metrics.ResponseQuality, caseQuality, i+1)
		metrics.Correctness = updateMetric(metrics.Correctness, caseCorrectness, i+1)
		metrics.Relevance = updateMetric(metrics.Relevance, caseRelevance, i+1)
		metrics.ToolUsage = updateMetric(metrics.ToolUsage, caseToolUsage, i+1)

		// Determine if the test case passed based on thresholds
		if caseQuality >= config.MinResponseQuality &&
			caseCorrectness >= config.MinCorrectness &&
			caseRelevance >= config.MinRelevance &&
			caseToolUsage >= config.MinToolUsageScore {
			successes++
		} else {
			failures++
		}
	}

	// Calculate duration
	duration := time.Since(startTime)

	// Create additional details
	details := map[string]interface{}{
		"duration_ms": duration.Milliseconds(),
		"config":      config,
	}

	// Create the result
	result := &Result{
		ID:        runID,
		Timestamp: time.Now(),
		Metrics:   metrics,
		Details:   details,
		Successes: successes,
		Failures:  failures,
		Total:     totalTests,
	}

	e.logger.Info("Evaluation completed",
		"evaluator", e.name,
		"success_rate", fmt.Sprintf("%.1f%%", float64(successes)/float64(totalTests)*100),
		"duration_ms", duration.Milliseconds(),
	)

	return result, nil
}

// updateMetric updates a metric by incorporating a new value.
func updateMetric(current, new float64, count int) float64 {
	// Simple running average
	return current + (new-current)/float64(count)
}

// FormatResult formats a Result as a human-readable string.
func FormatResult(result *Result) string {
	var sb strings.Builder

	passRate := float64(result.Successes) / float64(result.Total) * 100

	sb.WriteString(fmt.Sprintf("Evaluation ID: %s\n", result.ID))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", result.Timestamp.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Success Rate: %.1f%% (%d/%d)\n", passRate, result.Successes, result.Total))
	sb.WriteString("Metrics:\n")
	sb.WriteString(fmt.Sprintf("  Response Quality: %.2f\n", result.Metrics.ResponseQuality))
	sb.WriteString(fmt.Sprintf("  Correctness: %.2f\n", result.Metrics.Correctness))
	sb.WriteString(fmt.Sprintf("  Relevance: %.2f\n", result.Metrics.Relevance))
	sb.WriteString(fmt.Sprintf("  Tool Usage: %.2f\n", result.Metrics.ToolUsage))

	// Include any duration information if available
	if durationMS, ok := result.Details["duration_ms"].(int64); ok {
		duration := time.Duration(durationMS) * time.Millisecond
		sb.WriteString(fmt.Sprintf("Duration: %s\n", duration))
	}

	return sb.String()
}
