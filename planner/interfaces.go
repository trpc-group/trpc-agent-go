// Package planner provides interfaces and implementations for planning and task management.
package planner

import (
	"context"
	"time"
)

// Status represents the execution status of a plan, task, or step.
type Status string

const (
	// StatusNotStarted indicates the item has not started execution.
	StatusNotStarted Status = "not_started"
	// StatusInProgress indicates the item is currently executing.
	StatusInProgress Status = "in_progress"
	// StatusCompleted indicates the item has completed successfully.
	StatusCompleted Status = "completed"
	// StatusFailed indicates the item has failed execution.
	StatusFailed Status = "failed"
	// StatusCancelled indicates the item was cancelled before completion.
	StatusCancelled Status = "cancelled"
)

// Plan represents a structured plan with steps to accomplish a goal.
type Plan interface {
	// ID returns the unique identifier for this plan.
	ID() string

	// Goal returns the goal statement of the plan.
	Goal() string

	// Tasks returns the list of tasks in this plan.
	Tasks() []Task

	// AddTask adds a new task to the plan.
	AddTask(task Task) error

	// RemoveTask removes a task from the plan by ID.
	RemoveTask(taskID string) error

	// GetTask retrieves a task by ID.
	GetTask(taskID string) (Task, bool)

	// Status returns the current status of the plan.
	Status() Status

	// SetStatus updates the status of the plan.
	SetStatus(status Status)

	// CreatedAt returns the creation time of the plan.
	CreatedAt() time.Time

	// UpdatedAt returns the last update time of the plan.
	UpdatedAt() time.Time

	// Metadata returns the metadata associated with the plan.
	Metadata() map[string]interface{}

	// SetMetadata sets or updates the metadata for the plan.
	SetMetadata(key string, value interface{})
}

// Task represents a discrete task within a plan.
type Task interface {
	// ID returns the unique identifier for this task.
	ID() string

	// Description returns the description of the task.
	Description() string

	// Steps returns the ordered steps to complete this task.
	Steps() []Step

	// AddStep adds a new step to the task.
	AddStep(step Step) error

	// RemoveStep removes a step from the task by ID.
	RemoveStep(stepID string) error

	// GetStep retrieves a step by ID.
	GetStep(stepID string) (Step, bool)

	// Dependencies returns IDs of tasks that must be completed before this task.
	Dependencies() []string

	// AddDependency adds a task dependency.
	AddDependency(taskID string) error

	// RemoveDependency removes a task dependency.
	RemoveDependency(taskID string) error

	// Status returns the current status of the task.
	Status() Status

	// SetStatus updates the status of the task.
	SetStatus(status Status)

	// Priority returns the priority level of the task.
	Priority() int

	// SetPriority sets the priority level of the task.
	SetPriority(priority int)

	// Metadata returns the metadata associated with the task.
	Metadata() map[string]interface{}

	// SetMetadata sets or updates metadata for the task.
	SetMetadata(key string, value interface{})
}

// Step represents a single step within a task.
type Step interface {
	// ID returns the unique identifier for this step.
	ID() string

	// Description returns the description of the step.
	Description() string

	// Action returns the action to be performed in this step.
	Action() string

	// Parameters returns the parameters for the step's action.
	Parameters() map[string]interface{}

	// SetParameter sets or updates a parameter for the step.
	SetParameter(key string, value interface{})

	// Status returns the current status of the step.
	Status() Status

	// SetStatus updates the status of the step.
	SetStatus(status Status)

	// Result returns the result of executing the step.
	Result() interface{}

	// SetResult sets the result of the step execution.
	SetResult(result interface{})

	// Metadata returns the metadata associated with the step.
	Metadata() map[string]interface{}

	// SetMetadata sets or updates metadata for the step.
	SetMetadata(key string, value interface{})
}

// Planner defines the interface for planning components that can
// generate and execute plans based on goals.
type Planner interface {
	// Name returns the name of the planner.
	Name() string

	// Description returns a description of the planner.
	Description() string

	// CreatePlan generates a new plan for the given goal.
	CreatePlan(ctx context.Context, goal string, options map[string]interface{}) (Plan, error)

	// ExecutePlan executes the given plan and returns the result.
	ExecutePlan(ctx context.Context, plan Plan) error

	// UpdatePlan updates an existing plan based on new information.
	UpdatePlan(ctx context.Context, plan Plan, newGoal string, options map[string]interface{}) (Plan, error)
}

// PlanExecutionMonitor monitors and reports on plan execution.
type PlanExecutionMonitor interface {
	// OnPlanStart is called when a plan starts execution.
	OnPlanStart(plan Plan)

	// OnPlanComplete is called when a plan completes execution.
	OnPlanComplete(plan Plan)

	// OnPlanFail is called when a plan fails execution.
	OnPlanFail(plan Plan, err error)

	// OnTaskStart is called when a task starts execution.
	OnTaskStart(task Task)

	// OnTaskComplete is called when a task completes execution.
	OnTaskComplete(task Task)

	// OnTaskFail is called when a task fails execution.
	OnTaskFail(task Task, err error)

	// OnStepStart is called when a step starts execution.
	OnStepStart(step Step)

	// OnStepComplete is called when a step completes execution.
	OnStepComplete(step Step)

	// OnStepFail is called when a step fails execution.
	OnStepFail(step Step, err error)
}
