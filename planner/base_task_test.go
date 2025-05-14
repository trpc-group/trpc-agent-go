package planner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBaseTask(t *testing.T) {
	// Create a new task
	taskID := "test-task"
	description := "Test task description"
	task := NewBaseTask(taskID, description)

	// Verify the task was created correctly
	assert.Equal(t, taskID, task.ID())
	assert.Equal(t, description, task.Description())
	assert.Empty(t, task.Steps())
	assert.Empty(t, task.Dependencies())
	assert.Equal(t, StatusNotStarted, task.Status())
	assert.Equal(t, 0, task.Priority())
	assert.NotNil(t, task.Metadata())
	assert.Empty(t, task.Metadata())
}

func TestBaseTask_AddStep(t *testing.T) {
	// Create a task
	task := NewBaseTask("task-1", "Test Task")

	// Create steps
	step1 := NewBaseStep("step-1", "Step 1", "action1")
	step2 := NewBaseStep("step-2", "Step 2", "action2")
	step3 := NewBaseStep("step-1", "Step 1 Duplicate", "action1") // Same ID as step1

	// Test adding steps
	err := task.AddStep(step1)
	assert.NoError(t, err)
	assert.Len(t, task.Steps(), 1)

	err = task.AddStep(step2)
	assert.NoError(t, err)
	assert.Len(t, task.Steps(), 2)

	// Test adding duplicate step ID
	err = task.AddStep(step3)
	assert.Error(t, err)
	assert.Equal(t, ErrStepAlreadyExists, err)
	assert.Len(t, task.Steps(), 2)

	// Test adding nil step
	err = task.AddStep(nil)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidStep, err)
	assert.Len(t, task.Steps(), 2)

	// Verify steps are in correct order
	steps := task.Steps()
	assert.Equal(t, "step-1", steps[0].ID())
	assert.Equal(t, "step-2", steps[1].ID())
}

func TestBaseTask_RemoveStep(t *testing.T) {
	// Create a task with steps
	task := NewBaseTask("task-1", "Test Task")
	step1 := NewBaseStep("step-1", "Step 1", "action1")
	step2 := NewBaseStep("step-2", "Step 2", "action2")

	_ = task.AddStep(step1)
	_ = task.AddStep(step2)

	// Verify initial state
	assert.Len(t, task.Steps(), 2)

	// Test removing a step
	err := task.RemoveStep("step-1")
	assert.NoError(t, err)
	assert.Len(t, task.Steps(), 1)

	// Verify the correct step was removed
	steps := task.Steps()
	assert.Equal(t, "step-2", steps[0].ID())

	// Test removing a non-existent step
	err = task.RemoveStep("non-existent")
	assert.Error(t, err)
	assert.Equal(t, ErrStepNotFound, err)
	assert.Len(t, task.Steps(), 1)
}

func TestBaseTask_GetStep(t *testing.T) {
	// Create a task with steps
	task := NewBaseTask("task-1", "Test Task")
	step1 := NewBaseStep("step-1", "Step 1", "action1")
	_ = task.AddStep(step1)

	// Test getting an existing step
	step, exists := task.GetStep("step-1")
	assert.True(t, exists)
	assert.Equal(t, "step-1", step.ID())
	assert.Equal(t, "Step 1", step.Description())

	// Test getting a non-existent step
	step, exists = task.GetStep("non-existent")
	assert.False(t, exists)
	assert.Nil(t, step)
}

func TestBaseTask_Dependencies(t *testing.T) {
	// Create a task
	task := NewBaseTask("task-1", "Test Task")

	// Initial dependencies should be empty
	assert.Empty(t, task.Dependencies())

	// Add dependencies
	_ = task.AddDependency("dep-1")
	_ = task.AddDependency("dep-2")

	// Verify dependencies
	deps := task.Dependencies()
	assert.Len(t, deps, 2)
	assert.Contains(t, deps, "dep-1")
	assert.Contains(t, deps, "dep-2")

	// Test adding a duplicate dependency
	_ = task.AddDependency("dep-1")
	deps = task.Dependencies()
	assert.Len(t, deps, 2) // Should still have only 2

	// Test removing a dependency
	_ = task.RemoveDependency("dep-1")
	deps = task.Dependencies()
	assert.Len(t, deps, 1)
	assert.Contains(t, deps, "dep-2")

	// Test removing a non-existent dependency
	_ = task.RemoveDependency("non-existent")
	deps = task.Dependencies()
	assert.Len(t, deps, 1) // Should remain unchanged
}

func TestBaseTask_Status(t *testing.T) {
	// Create a task
	task := NewBaseTask("task-1", "Test Task")

	// Test initial status
	assert.Equal(t, StatusNotStarted, task.Status())

	// Test setting status
	task.SetStatus(StatusInProgress)
	assert.Equal(t, StatusInProgress, task.Status())

	task.SetStatus(StatusCompleted)
	assert.Equal(t, StatusCompleted, task.Status())
}

func TestBaseTask_Priority(t *testing.T) {
	// Create a task
	task := NewBaseTask("task-1", "Test Task")

	// Test initial priority
	assert.Equal(t, 0, task.Priority())

	// Test setting priority
	task.SetPriority(5)
	assert.Equal(t, 5, task.Priority())

	task.SetPriority(-1)
	assert.Equal(t, -1, task.Priority())
}

func TestBaseTask_Metadata(t *testing.T) {
	// Create a task
	task := NewBaseTask("task-1", "Test Task")

	// Initial metadata should be empty
	assert.Empty(t, task.Metadata())

	// Set metadata
	task.SetMetadata("key1", "value1")
	task.SetMetadata("key2", 42)

	// Verify metadata
	metadata := task.Metadata()
	assert.Len(t, metadata, 2)
	assert.Equal(t, "value1", metadata["key1"])
	assert.Equal(t, 42, metadata["key2"])

	// Overwrite metadata
	task.SetMetadata("key1", "new-value")

	// Verify updated metadata
	metadata = task.Metadata()
	assert.Equal(t, "new-value", metadata["key1"])
}
