package runner

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Verify default values
	if config.MaxConcurrent != 10 {
		t.Errorf("Expected MaxConcurrent to be 10, got %d", config.MaxConcurrent)
	}

	if config.Timeout != time.Minute {
		t.Errorf("Expected Timeout to be 1 minute, got %v", config.Timeout)
	}

	if config.RetryCount != 3 {
		t.Errorf("Expected RetryCount to be 3, got %d", config.RetryCount)
	}

	if config.RetryDelay != time.Second {
		t.Errorf("Expected RetryDelay to be 1 second, got %v", config.RetryDelay)
	}

	if config.BufferSize != 100 {
		t.Errorf("Expected BufferSize to be 100, got %d", config.BufferSize)
	}

	if config.Custom == nil {
		t.Error("Expected Custom to be initialized, got nil")
	}
}

func TestWithTimeout(t *testing.T) {
	config := DefaultConfig()

	// Test with zero value
	config = config.WithTimeout(0)
	if config.Timeout != 0 {
		t.Errorf("Expected Timeout to be 0, got %v", config.Timeout)
	}

	// Test with non-zero value
	testTimeout := 30 * time.Second
	config = config.WithTimeout(testTimeout)
	if config.Timeout != testTimeout {
		t.Errorf("Expected Timeout to be %v, got %v", testTimeout, config.Timeout)
	}

	// Verify other fields remain unchanged
	if config.MaxConcurrent != DefaultConfig().MaxConcurrent {
		t.Errorf("Expected MaxConcurrent to remain %d, got %d", DefaultConfig().MaxConcurrent, config.MaxConcurrent)
	}
}

func TestWithMaxConcurrent(t *testing.T) {
	config := DefaultConfig()

	// Test with zero value
	config = config.WithMaxConcurrent(0)
	if config.MaxConcurrent != 0 {
		t.Errorf("Expected MaxConcurrent to be 0, got %d", config.MaxConcurrent)
	}

	// Test with positive value
	config = config.WithMaxConcurrent(5)
	if config.MaxConcurrent != 5 {
		t.Errorf("Expected MaxConcurrent to be 5, got %d", config.MaxConcurrent)
	}

	// Verify other fields remain unchanged
	if config.Timeout != DefaultConfig().Timeout {
		t.Errorf("Expected Timeout to remain %v, got %v", DefaultConfig().Timeout, config.Timeout)
	}
}

func TestWithRetry(t *testing.T) {
	config := DefaultConfig()

	// Test with zero values
	config = config.WithRetry(0, 0)
	if config.RetryCount != 0 {
		t.Errorf("Expected RetryCount to be 0, got %d", config.RetryCount)
	}
	if config.RetryDelay != 0 {
		t.Errorf("Expected RetryDelay to be 0, got %v", config.RetryDelay)
	}

	// Test with non-zero values
	testCount := 5
	testDelay := 2 * time.Second
	config = config.WithRetry(testCount, testDelay)
	if config.RetryCount != testCount {
		t.Errorf("Expected RetryCount to be %d, got %d", testCount, config.RetryCount)
	}
	if config.RetryDelay != testDelay {
		t.Errorf("Expected RetryDelay to be %v, got %v", testDelay, config.RetryDelay)
	}

	// Verify other fields remain unchanged
	if config.MaxConcurrent != DefaultConfig().MaxConcurrent {
		t.Errorf("Expected MaxConcurrent to remain %d, got %d", DefaultConfig().MaxConcurrent, config.MaxConcurrent)
	}
}

func TestWithBufferSize(t *testing.T) {
	config := DefaultConfig()

	// Test with zero value
	config = config.WithBufferSize(0)
	if config.BufferSize != 0 {
		t.Errorf("Expected BufferSize to be 0, got %d", config.BufferSize)
	}

	// Test with positive value
	config = config.WithBufferSize(200)
	if config.BufferSize != 200 {
		t.Errorf("Expected BufferSize to be 200, got %d", config.BufferSize)
	}

	// Verify other fields remain unchanged
	if config.MaxConcurrent != DefaultConfig().MaxConcurrent {
		t.Errorf("Expected MaxConcurrent to remain %d, got %d", DefaultConfig().MaxConcurrent, config.MaxConcurrent)
	}
}

func TestWithCustomParam(t *testing.T) {
	config := DefaultConfig()

	// Test with nil Custom map
	config.Custom = nil
	config = config.WithCustomParam("test-key", "test-value")
	if config.Custom == nil {
		t.Error("Expected Custom map to be initialized, got nil")
	}
	if value, ok := config.Custom["test-key"]; !ok || value != "test-value" {
		t.Errorf("Expected Custom[\"test-key\"] to be \"test-value\", got %v", value)
	}

	// Test with existing key
	config = config.WithCustomParam("test-key", "new-value")
	if value, ok := config.Custom["test-key"]; !ok || value != "new-value" {
		t.Errorf("Expected Custom[\"test-key\"] to be \"new-value\", got %v", value)
	}

	// Test with different value types
	config = config.WithCustomParam("int-key", 123)
	if value, ok := config.Custom["int-key"]; !ok || value != 123 {
		t.Errorf("Expected Custom[\"int-key\"] to be 123, got %v", value)
	}

	config = config.WithCustomParam("bool-key", true)
	if value, ok := config.Custom["bool-key"]; !ok || value != true {
		t.Errorf("Expected Custom[\"bool-key\"] to be true, got %v", value)
	}

	// Verify other fields remain unchanged
	if config.MaxConcurrent != DefaultConfig().MaxConcurrent {
		t.Errorf("Expected MaxConcurrent to remain %d, got %d", DefaultConfig().MaxConcurrent, config.MaxConcurrent)
	}
}
