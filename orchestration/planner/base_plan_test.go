package planner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewBasePlan(t *testing.T) {
	// Create a new plan
	planID := "test-plan"
	goal := "Test goal for plan"
	plan := NewBasePlan(planID, goal)

	// Verify the plan was created correctly
	assert.Equal(t, planID, plan.ID())
	assert.Equal(t, goal, plan.Goal())
	assert.Empty(t, plan.Tasks())
	assert.Equal(t, StatusNotStarted, plan.Status())
	assert.NotZero(t, plan.CreatedAt())
	assert.NotZero(t, plan.UpdatedAt())
	assert.NotNil(t, plan.Metadata())
	assert.Empty(t, plan.Metadata())
}

func TestBasePlan_AddTask(t *testing.T) {
	// Create a plan
	plan := NewBasePlan("plan-1", "Test Plan")

	// Create tasks
	task1 := NewBaseTask("task-1", "Task 1")
	task2 := NewBaseTask("task-2", "Task 2")
	task3 := NewBaseTask("task-1", "Task 1 Duplicate") // Same ID as task1

	// Test adding tasks
	err := plan.AddTask(task1)
	assert.NoError(t, err)
	assert.Len(t, plan.Tasks(), 1)

	err = plan.AddTask(task2)
	assert.NoError(t, err)
	assert.Len(t, plan.Tasks(), 2)

	// Test adding duplicate task ID
	err = plan.AddTask(task3)
	assert.Error(t, err)
	assert.Equal(t, ErrTaskAlreadyExists, err)
	assert.Len(t, plan.Tasks(), 2)

	// Test adding nil task
	err = plan.AddTask(nil)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidTask, err)
	assert.Len(t, plan.Tasks(), 2)

	// Verify tasks are in correct order
	tasks := plan.Tasks()
	assert.Equal(t, "task-1", tasks[0].ID())
	assert.Equal(t, "task-2", tasks[1].ID())
}

func TestBasePlan_RemoveTask(t *testing.T) {
	// Create a plan with tasks
	plan := NewBasePlan("plan-1", "Test Plan")
	task1 := NewBaseTask("task-1", "Task 1")
	task2 := NewBaseTask("task-2", "Task 2")

	_ = plan.AddTask(task1)
	_ = plan.AddTask(task2)

	// Verify initial state
	assert.Len(t, plan.Tasks(), 2)

	// Test removing a task
	err := plan.RemoveTask("task-1")
	assert.NoError(t, err)
	assert.Len(t, plan.Tasks(), 1)

	// Verify the correct task was removed
	tasks := plan.Tasks()
	assert.Equal(t, "task-2", tasks[0].ID())

	// Test removing a non-existent task
	err = plan.RemoveTask("non-existent")
	assert.Error(t, err)
	assert.Equal(t, ErrTaskNotFound, err)
	assert.Len(t, plan.Tasks(), 1)
}

func TestBasePlan_GetTask(t *testing.T) {
	// Create a plan with tasks
	plan := NewBasePlan("plan-1", "Test Plan")
	task1 := NewBaseTask("task-1", "Task 1")
	_ = plan.AddTask(task1)

	// Test getting an existing task
	task, exists := plan.GetTask("task-1")
	assert.True(t, exists)
	assert.Equal(t, "task-1", task.ID())
	assert.Equal(t, "Task 1", task.Description())

	// Test getting a non-existent task
	task, exists = plan.GetTask("non-existent")
	assert.False(t, exists)
	assert.Nil(t, task)
}

func TestBasePlan_Status(t *testing.T) {
	// Create a plan
	plan := NewBasePlan("plan-1", "Test Plan")

	// Test initial status
	assert.Equal(t, StatusNotStarted, plan.Status())

	// Test setting status
	plan.SetStatus(StatusInProgress)
	assert.Equal(t, StatusInProgress, plan.Status())

	plan.SetStatus(StatusCompleted)
	assert.Equal(t, StatusCompleted, plan.Status())
}

func TestBasePlan_UpdatedAt(t *testing.T) {
	// Create a plan
	plan := NewBasePlan("plan-1", "Test Plan")
	initialTime := plan.UpdatedAt()

	// Sleep to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Modify the plan
	plan.SetStatus(StatusInProgress)

	// Check that UpdatedAt has changed
	assert.True(t, plan.UpdatedAt().After(initialTime))
}

func TestBasePlan_Metadata(t *testing.T) {
	// Create a plan
	plan := NewBasePlan("plan-1", "Test Plan")

	// Initial metadata should be empty
	assert.Empty(t, plan.Metadata())

	// Set metadata
	plan.SetMetadata("key1", "value1")
	plan.SetMetadata("key2", 42)

	// Verify metadata
	metadata := plan.Metadata()
	assert.Len(t, metadata, 2)
	assert.Equal(t, "value1", metadata["key1"])
	assert.Equal(t, 42, metadata["key2"])

	// Overwrite metadata
	plan.SetMetadata("key1", "new-value")

	// Verify updated metadata
	metadata = plan.Metadata()
	assert.Equal(t, "new-value", metadata["key1"])
}

func TestBasePlan_GetTasksInExecutionOrder(t *testing.T) {
	// Create a plan
	plan := NewBasePlan("plan-1", "Test Plan")

	// Create tasks with dependencies and priorities
	taskA := NewBaseTask("task-A", "Task A")
	taskB := NewBaseTask("task-B", "Task B")
	taskC := NewBaseTask("task-C", "Task C")
	taskD := NewBaseTask("task-D", "Task D")

	// Set dependencies: B depends on A, C depends on B, D depends on A
	_ = taskB.AddDependency("task-A")
	_ = taskC.AddDependency("task-B")
	_ = taskD.AddDependency("task-A")

	// Set priorities
	taskA.SetPriority(1)
	taskB.SetPriority(2)
	taskC.SetPriority(1)
	taskD.SetPriority(3) // D has highest priority

	// Add tasks in random order
	_ = plan.AddTask(taskC)
	_ = plan.AddTask(taskA)
	_ = plan.AddTask(taskD)
	_ = plan.AddTask(taskB)

	// Get tasks in execution order
	orderedTasks, err := plan.GetTasksInExecutionOrder()
	assert.NoError(t, err)
	assert.Len(t, orderedTasks, 4)

	// Validate the order - A must come before B and D, B must come before C
	// Expected order considering dependencies and priorities:
	// First A (no dependencies), then either B or D (both depend on A, but D has higher priority),
	// then the other, then C (depends on B)
	aIndex := -1
	bIndex := -1
	cIndex := -1
	dIndex := -1

	for i, task := range orderedTasks {
		switch task.ID() {
		case "task-A":
			aIndex = i
		case "task-B":
			bIndex = i
		case "task-C":
			cIndex = i
		case "task-D":
			dIndex = i
		}
	}

	// Check that all tasks are included
	assert.GreaterOrEqual(t, aIndex, 0)
	assert.GreaterOrEqual(t, bIndex, 0)
	assert.GreaterOrEqual(t, cIndex, 0)
	assert.GreaterOrEqual(t, dIndex, 0)

	// Verify dependencies are respected
	assert.Less(t, aIndex, bIndex, "Task A should come before Task B")
	assert.Less(t, bIndex, cIndex, "Task B should come before Task C")
	assert.Less(t, aIndex, dIndex, "Task A should come before Task D")

	// Test cyclic dependency detection
	taskE := NewBaseTask("task-E", "Task E")
	taskF := NewBaseTask("task-F", "Task F")

	_ = taskE.AddDependency("task-F")
	_ = taskF.AddDependency("task-E") // Creates a cycle

	_ = plan.AddTask(taskE)
	_ = plan.AddTask(taskF)

	// This should fail due to cyclic dependencies
	_, err = plan.GetTasksInExecutionOrder()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cyclic dependency")
}
