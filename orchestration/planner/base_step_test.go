package planner

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBaseStep(t *testing.T) {
	id := "step-001"
	description := "Test step"
	action := "test_action"

	step := NewBaseStep(id, description, action)

	assert.NotNil(t, step)
	assert.Equal(t, id, step.ID())
	assert.Equal(t, description, step.Description())
	assert.Equal(t, action, step.Action())
	assert.Equal(t, StatusNotStarted, step.Status())
	assert.Empty(t, step.Parameters())
	assert.Empty(t, step.Metadata())
	assert.Nil(t, step.Result())
}

func TestBaseStep_Parameters(t *testing.T) {
	step := NewBaseStep("step-001", "Test step", "test_action")

	// Test empty parameters
	params := step.Parameters()
	assert.Empty(t, params)

	// Test adding parameters
	step.SetParameter("key1", "value1")
	step.SetParameter("key2", 42)

	params = step.Parameters()
	assert.Len(t, params, 2)
	assert.Equal(t, "value1", params["key1"])
	assert.Equal(t, 42, params["key2"])

	// Test updating parameter
	step.SetParameter("key1", "updated_value")
	params = step.Parameters()
	assert.Equal(t, "updated_value", params["key1"])
}

func TestBaseStep_Status(t *testing.T) {
	step := NewBaseStep("step-001", "Test step", "test_action")

	// Initial status
	assert.Equal(t, StatusNotStarted, step.Status())

	// Test status updates
	statuses := []Status{
		StatusInProgress,
		StatusCompleted,
		StatusFailed,
		StatusCancelled,
	}

	for _, status := range statuses {
		step.SetStatus(status)
		assert.Equal(t, status, step.Status())
	}
}

func TestBaseStep_Result(t *testing.T) {
	step := NewBaseStep("step-001", "Test step", "test_action")

	// Initial result is nil
	assert.Nil(t, step.Result())

	// Test string result
	step.SetResult("test result")
	assert.Equal(t, "test result", step.Result())

	// Test map result
	mapResult := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}
	step.SetResult(mapResult)
	assert.Equal(t, mapResult, step.Result())

	// Test int result
	step.SetResult(42)
	assert.Equal(t, 42, step.Result())
}

func TestBaseStep_Metadata(t *testing.T) {
	step := NewBaseStep("step-001", "Test step", "test_action")

	// Test empty metadata
	metadata := step.Metadata()
	assert.Empty(t, metadata)

	// Test adding metadata
	step.SetMetadata("meta1", "value1")
	step.SetMetadata("meta2", 42)

	metadata = step.Metadata()
	assert.Len(t, metadata, 2)
	assert.Equal(t, "value1", metadata["meta1"])
	assert.Equal(t, 42, metadata["meta2"])

	// Test updating metadata
	step.SetMetadata("meta1", "updated_value")
	metadata = step.Metadata()
	assert.Equal(t, "updated_value", metadata["meta1"])
}

func TestBaseStep_ConcurrentSafety(t *testing.T) {
	step := NewBaseStep("step-001", "Test step", "test_action")

	// This test is primarily to ensure that the mutex guards work properly
	// We don't expect any race conditions even with concurrent access
	done := make(chan bool)

	// Simulate concurrent access to parameters
	go func() {
		for i := 0; i < 100; i++ {
			step.SetParameter(fmt.Sprintf("key%d", i), i)
		}
		done <- true
	}()

	// Simulate concurrent access to metadata
	go func() {
		for i := 0; i < 100; i++ {
			step.SetMetadata(fmt.Sprintf("meta%d", i), i)
		}
		done <- true
	}()

	// Simulate concurrent access to status
	go func() {
		statuses := []Status{
			StatusNotStarted,
			StatusInProgress,
			StatusCompleted,
			StatusFailed,
		}
		for i := 0; i < 100; i++ {
			step.SetStatus(statuses[i%len(statuses)])
		}
		done <- true
	}()

	// Simulate concurrent access to result
	go func() {
		for i := 0; i < 100; i++ {
			step.SetResult(i)
		}
		done <- true
	}()

	// Wait for all goroutines to complete
	for i := 0; i < 4; i++ {
		<-done
	}

	// Verify that the state is consistent
	params := step.Parameters()
	meta := step.Metadata()
	status := step.Status()
	result := step.Result()

	assert.NotNil(t, params)
	assert.NotNil(t, meta)
	assert.NotNil(t, status)
	assert.NotNil(t, result)
}
