package planner

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// MonitorEvent represents an event captured by the monitor.
type MonitorEvent struct {
	Type      string
	ID        string
	Status    Status
	Timestamp time.Time
	Error     error
}

// String returns a human-readable representation of a MonitorEvent.
func (e MonitorEvent) String() string {
	errStr := ""
	if e.Error != nil {
		errStr = fmt.Sprintf(", error=%v", e.Error)
	}

	return fmt.Sprintf("%s(id=%s, status=%s, time=%s%s)",
		e.Type, e.ID, e.Status, e.Timestamp.Format(time.RFC3339), errStr)
}

// LoggingMonitor is a simple implementation of PlanExecutionMonitor that logs events.
type LoggingMonitor struct {
	logger *slog.Logger
	events []MonitorEvent
	mu     sync.RWMutex
}

// NewLoggingMonitor creates a new LoggingMonitor with the given logger.
func NewLoggingMonitor(logger *slog.Logger) *LoggingMonitor {
	// If no logger is provided, create one that discards output
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(nil, nil))
	}

	return &LoggingMonitor{
		logger: logger,
		events: make([]MonitorEvent, 0),
	}
}

// GetEvents returns all events captured by the monitor.
func (m *LoggingMonitor) GetEvents() []MonitorEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	events := make([]MonitorEvent, len(m.events))
	copy(events, m.events)
	return events
}

// OnPlanStart is called when a plan starts execution.
func (m *LoggingMonitor) OnPlanStart(plan Plan) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "PlanStart",
		ID:        plan.ID(),
		Status:    plan.Status(),
		Timestamp: time.Now(),
	}

	m.events = append(m.events, event)
	m.logger.Info("Plan started",
		"plan_id", plan.ID(),
		"goal", plan.Goal(),
		"status", plan.Status())
}

// OnPlanComplete is called when a plan completes execution.
func (m *LoggingMonitor) OnPlanComplete(plan Plan) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "PlanComplete",
		ID:        plan.ID(),
		Status:    plan.Status(),
		Timestamp: time.Now(),
	}

	m.events = append(m.events, event)
	m.logger.Info("Plan completed",
		"plan_id", plan.ID(),
		"goal", plan.Goal(),
		"status", plan.Status())
}

// OnPlanFail is called when a plan fails execution.
func (m *LoggingMonitor) OnPlanFail(plan Plan, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "PlanFail",
		ID:        plan.ID(),
		Status:    plan.Status(),
		Timestamp: time.Now(),
		Error:     err,
	}

	m.events = append(m.events, event)
	m.logger.Error("Plan failed",
		"plan_id", plan.ID(),
		"goal", plan.Goal(),
		"status", plan.Status(),
		"error", err)
}

// OnTaskStart is called when a task starts execution.
func (m *LoggingMonitor) OnTaskStart(task Task) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "TaskStart",
		ID:        task.ID(),
		Status:    task.Status(),
		Timestamp: time.Now(),
	}

	m.events = append(m.events, event)
	m.logger.Info("Task started",
		"task_id", task.ID(),
		"description", task.Description(),
		"status", task.Status())
}

// OnTaskComplete is called when a task completes execution.
func (m *LoggingMonitor) OnTaskComplete(task Task) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "TaskComplete",
		ID:        task.ID(),
		Status:    task.Status(),
		Timestamp: time.Now(),
	}

	m.events = append(m.events, event)
	m.logger.Info("Task completed",
		"task_id", task.ID(),
		"description", task.Description(),
		"status", task.Status())
}

// OnTaskFail is called when a task fails execution.
func (m *LoggingMonitor) OnTaskFail(task Task, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "TaskFail",
		ID:        task.ID(),
		Status:    task.Status(),
		Timestamp: time.Now(),
		Error:     err,
	}

	m.events = append(m.events, event)
	m.logger.Error("Task failed",
		"task_id", task.ID(),
		"description", task.Description(),
		"status", task.Status(),
		"error", err)
}

// OnStepStart is called when a step starts execution.
func (m *LoggingMonitor) OnStepStart(step Step) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "StepStart",
		ID:        step.ID(),
		Status:    step.Status(),
		Timestamp: time.Now(),
	}

	m.events = append(m.events, event)
	m.logger.Info("Step started",
		"step_id", step.ID(),
		"description", step.Description(),
		"action", step.Action(),
		"status", step.Status())
}

// OnStepComplete is called when a step completes execution.
func (m *LoggingMonitor) OnStepComplete(step Step) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "StepComplete",
		ID:        step.ID(),
		Status:    step.Status(),
		Timestamp: time.Now(),
	}

	m.events = append(m.events, event)
	m.logger.Info("Step completed",
		"step_id", step.ID(),
		"description", step.Description(),
		"action", step.Action(),
		"status", step.Status())
}

// OnStepFail is called when a step fails execution.
func (m *LoggingMonitor) OnStepFail(step Step, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	event := MonitorEvent{
		Type:      "StepFail",
		ID:        step.ID(),
		Status:    step.Status(),
		Timestamp: time.Now(),
		Error:     err,
	}

	m.events = append(m.events, event)
	m.logger.Error("Step failed",
		"step_id", step.ID(),
		"description", step.Description(),
		"action", step.Action(),
		"status", step.Status(),
		"error", err)
}
