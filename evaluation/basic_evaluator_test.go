package evaluation

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestBasicEvaluator(t *testing.T) {
	// Create a test logger with a nil writer to discard logs during testing
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Create a basic evaluator with custom config
	config := Config{
		MinResponseQuality: 0.7,
		MinToolUsageScore:  0.6,
		MinCorrectness:     0.7,
		MinRelevance:       0.7,
		Runs:               1,
	}
	evaluator := NewBasicEvaluator("test-basic", config, logger)

	// Check evaluator name
	if evaluator.Name() != "test-basic" {
		t.Errorf("Expected evaluator name 'test-basic', got %s", evaluator.Name())
	}

	// Create test cases
	testCases := []TestCase{
		{
			ID:             "test-1",
			Input:          "What is the capital of France?",
			ExpectedOutput: "The capital of France is Paris.",
		},
		{
			ID:             "test-2",
			Input:          "How tall is Mount Everest?",
			ExpectedOutput: "Mount Everest is 8,848.86 meters (29,031.7 feet) tall.",
		},
	}

	// Create evaluation input
	input := EvaluationInput{
		TestCases: testCases,
		Config:    DefaultConfig(),
	}

	// Run evaluation
	ctx := context.Background()
	result, err := evaluator.Evaluate(ctx, input)
	if err != nil {
		t.Fatalf("Error evaluating test cases: %v", err)
	}

	// Check result fields
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result.ID == "" {
		t.Error("Expected result ID to be non-empty")
	}
	if result.Timestamp.IsZero() {
		t.Error("Expected timestamp to be set")
	}
	if result.Total != 2 {
		t.Errorf("Expected 2 total tests, got %d", result.Total)
	}

	// Check metrics are within expected ranges
	if result.Metrics.ResponseQuality < 0 || result.Metrics.ResponseQuality > 1 {
		t.Errorf("ResponseQuality should be between 0 and 1, got %f", result.Metrics.ResponseQuality)
	}
	if result.Metrics.Correctness < 0 || result.Metrics.Correctness > 1 {
		t.Errorf("Correctness should be between 0 and 1, got %f", result.Metrics.Correctness)
	}
	if result.Metrics.Relevance < 0 || result.Metrics.Relevance > 1 {
		t.Errorf("Relevance should be between 0 and 1, got %f", result.Metrics.Relevance)
	}
	if result.Metrics.ToolUsage < 0 || result.Metrics.ToolUsage > 1 {
		t.Errorf("ToolUsage should be between 0 and 1, got %f", result.Metrics.ToolUsage)
	}

	// Check details
	if _, ok := result.Details["duration_ms"]; !ok {
		t.Error("Expected duration_ms in details")
	}
}

func TestFormatResult(t *testing.T) {
	// Create a test result
	metrics := Metrics{
		ResponseQuality: 0.95,
		Correctness:     0.85,
		Relevance:       0.92,
		ToolUsage:       0.88,
	}
	result := &Result{
		ID:        "test-run-id",
		Timestamp: time.Now(),
		Metrics:   metrics,
		Details: map[string]interface{}{
			"duration_ms": int64(1234),
		},
		Successes: 9,
		Failures:  1,
		Total:     10,
	}

	// Format the result
	formatted := FormatResult(result)

	// Check that the formatted string contains expected elements
	expectedSubstrings := []string{
		"Evaluation ID: test-run-id",
		"Success Rate: 90.0% (9/10)",
		"Response Quality: 0.95",
		"Correctness: 0.85",
		"Relevance: 0.92",
		"Tool Usage: 0.88",
		"Duration: 1.234s",
	}

	for _, substr := range expectedSubstrings {
		if !contains(formatted, substr) {
			t.Errorf("Expected formatted result to contain '%s', but it doesn't.\nGot: %s", substr, formatted)
		}
	}
}

func TestUpdateMetric(t *testing.T) {
	testCases := []struct {
		current float64
		new     float64
		count   int
		want    float64
	}{
		{1.0, 0.5, 1, 0.5},
		{0.5, 0.7, 2, 0.6},
		{0.6, 0.9, 3, 0.7},
	}

	for i, tc := range testCases {
		got := updateMetric(tc.current, tc.new, tc.count)
		if !floatEquals(got, tc.want, 0.001) {
			t.Errorf("Case %d: updateMetric(%f, %f, %d) = %f, want %f",
				i, tc.current, tc.new, tc.count, got, tc.want)
		}
	}
}

func TestDefaultNameForBasicEvaluator(t *testing.T) {
	evaluator := NewBasicEvaluator("", DefaultConfig(), nil)
	if evaluator.Name() != "basic-evaluator" {
		t.Errorf("Expected default name 'basic-evaluator', got %s", evaluator.Name())
	}
}

// Helper functions

func contains(s, substr string) bool {
	if s == "" || substr == "" || len(s) < len(substr) {
		return false
	}

	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func floatEquals(a, b, epsilon float64) bool {
	return a-b < epsilon && b-a < epsilon
}
