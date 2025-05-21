package planner

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewLoggingMonitor(t *testing.T) {
	// Create with nil logger
	monitor1 := NewLoggingMonitor(nil)
	assert.NotNil(t, monitor1)
	assert.NotNil(t, monitor1.logger)
	assert.Empty(t, monitor1.events)

	// Create with provided logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitor2 := NewLoggingMonitor(logger)
	assert.NotNil(t, monitor2)
	assert.Equal(t, logger, monitor2.logger)
	assert.Empty(t, monitor2.events)
}

func TestMonitorEvent_String(t *testing.T) {
	// Create a monitor event without error
	event1 := MonitorEvent{
		Type:      "TestEvent",
		ID:        "test-id",
		Status:    StatusCompleted,
		Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	// Verify string representation
	expected1 := "TestEvent(id=test-id, status=completed, time=2023-01-01T12:00:00Z)"
	assert.Equal(t, expected1, event1.String())

	// Create a monitor event with error
	event2 := MonitorEvent{
		Type:      "ErrorEvent",
		ID:        "error-id",
		Status:    StatusFailed,
		Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		Error:     errors.New("test error"),
	}

	// Verify string representation with error
	expected2 := "ErrorEvent(id=error-id, status=failed, time=2023-01-01T12:00:00Z, error=test error)"
	assert.Equal(t, expected2, event2.String())
}

func TestLoggingMonitor_GetEvents(t *testing.T) {
	// Create a monitor
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitor := NewLoggingMonitor(logger)

	// Initially should have no events
	assert.Empty(t, monitor.GetEvents())

	// Add some events manually
	monitor.events = append(monitor.events,
		MonitorEvent{Type: "Event1", ID: "id1"},
		MonitorEvent{Type: "Event2", ID: "id2"})

	// Get events
	events := monitor.GetEvents()
	assert.Len(t, events, 2)
	assert.Equal(t, "Event1", events[0].Type)
	assert.Equal(t, "Event2", events[1].Type)

	// Verify modifying the returned events doesn't affect the monitor
	events[0].ID = "modified"
	assert.Equal(t, "id1", monitor.events[0].ID)
}

func TestLoggingMonitor_PlanEvents(t *testing.T) {
	// Create a monitor
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitor := NewLoggingMonitor(logger)

	// Create a test plan
	plan := NewBasePlan("test-plan", "Test Plan")
	plan.SetStatus(StatusInProgress)

	// Test OnPlanStart
	monitor.OnPlanStart(plan)
	assert.Len(t, monitor.events, 1)
	assert.Equal(t, "PlanStart", monitor.events[0].Type)
	assert.Equal(t, "test-plan", monitor.events[0].ID)
	assert.Equal(t, StatusInProgress, monitor.events[0].Status)

	// Test OnPlanComplete
	plan.SetStatus(StatusCompleted)
	monitor.OnPlanComplete(plan)
	assert.Len(t, monitor.events, 2)
	assert.Equal(t, "PlanComplete", monitor.events[1].Type)
	assert.Equal(t, "test-plan", monitor.events[1].ID)
	assert.Equal(t, StatusCompleted, monitor.events[1].Status)

	// Test OnPlanFail
	testErr := errors.New("test plan error")
	plan.SetStatus(StatusFailed)
	monitor.OnPlanFail(plan, testErr)
	assert.Len(t, monitor.events, 3)
	assert.Equal(t, "PlanFail", monitor.events[2].Type)
	assert.Equal(t, "test-plan", monitor.events[2].ID)
	assert.Equal(t, StatusFailed, monitor.events[2].Status)
	assert.Equal(t, testErr, monitor.events[2].Error)
}

func TestLoggingMonitor_TaskEvents(t *testing.T) {
	// Create a monitor
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitor := NewLoggingMonitor(logger)

	// Create a test task
	task := NewBaseTask("test-task", "Test Task")
	task.SetStatus(StatusInProgress)

	// Test OnTaskStart
	monitor.OnTaskStart(task)
	assert.Len(t, monitor.events, 1)
	assert.Equal(t, "TaskStart", monitor.events[0].Type)
	assert.Equal(t, "test-task", monitor.events[0].ID)
	assert.Equal(t, StatusInProgress, monitor.events[0].Status)

	// Test OnTaskComplete
	task.SetStatus(StatusCompleted)
	monitor.OnTaskComplete(task)
	assert.Len(t, monitor.events, 2)
	assert.Equal(t, "TaskComplete", monitor.events[1].Type)
	assert.Equal(t, "test-task", monitor.events[1].ID)
	assert.Equal(t, StatusCompleted, monitor.events[1].Status)

	// Test OnTaskFail
	testErr := errors.New("test task error")
	task.SetStatus(StatusFailed)
	monitor.OnTaskFail(task, testErr)
	assert.Len(t, monitor.events, 3)
	assert.Equal(t, "TaskFail", monitor.events[2].Type)
	assert.Equal(t, "test-task", monitor.events[2].ID)
	assert.Equal(t, StatusFailed, monitor.events[2].Status)
	assert.Equal(t, testErr, monitor.events[2].Error)
}

func TestLoggingMonitor_StepEvents(t *testing.T) {
	// Create a monitor
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitor := NewLoggingMonitor(logger)

	// Create a test step
	step := NewBaseStep("test-step", "Test Step", "test.action")
	step.SetStatus(StatusInProgress)

	// Test OnStepStart
	monitor.OnStepStart(step)
	assert.Len(t, monitor.events, 1)
	assert.Equal(t, "StepStart", monitor.events[0].Type)
	assert.Equal(t, "test-step", monitor.events[0].ID)
	assert.Equal(t, StatusInProgress, monitor.events[0].Status)

	// Test OnStepComplete
	step.SetStatus(StatusCompleted)
	monitor.OnStepComplete(step)
	assert.Len(t, monitor.events, 2)
	assert.Equal(t, "StepComplete", monitor.events[1].Type)
	assert.Equal(t, "test-step", monitor.events[1].ID)
	assert.Equal(t, StatusCompleted, monitor.events[1].Status)

	// Test OnStepFail
	testErr := errors.New("test step error")
	step.SetStatus(StatusFailed)
	monitor.OnStepFail(step, testErr)
	assert.Len(t, monitor.events, 3)
	assert.Equal(t, "StepFail", monitor.events[2].Type)
	assert.Equal(t, "test-step", monitor.events[2].ID)
	assert.Equal(t, StatusFailed, monitor.events[2].Status)
	assert.Equal(t, testErr, monitor.events[2].Error)
}
