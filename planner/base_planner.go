package planner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrPlanNotFound is returned when a plan cannot be found.
	ErrPlanNotFound = errors.New("plan not found")
	// ErrInvalidGoal is returned when an invalid goal is provided.
	ErrInvalidGoal = errors.New("invalid goal")
	// ErrInvalidPlan is returned when an invalid plan is provided.
	ErrInvalidPlan = errors.New("invalid plan")
	// ErrPlanExecutionFailed is returned when plan execution fails.
	ErrPlanExecutionFailed = errors.New("plan execution failed")
)

// BasePlanner provides a base implementation of the Planner interface.
type BasePlanner struct {
	name        string
	description string
	plans       map[string]Plan
	monitor     PlanExecutionMonitor
	mu          sync.RWMutex
}

// NewBasePlanner creates a new instance of BasePlanner with the given name and description.
func NewBasePlanner(name, description string) *BasePlanner {
	return &BasePlanner{
		name:        name,
		description: description,
		plans:       make(map[string]Plan),
	}
}

// Name returns the name of the planner.
func (p *BasePlanner) Name() string {
	return p.name
}

// Description returns a description of the planner.
func (p *BasePlanner) Description() string {
	return p.description
}

// SetExecutionMonitor sets the execution monitor for this planner.
func (p *BasePlanner) SetExecutionMonitor(monitor PlanExecutionMonitor) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.monitor = monitor
}

// GetExecutionMonitor returns the current execution monitor.
func (p *BasePlanner) GetExecutionMonitor() PlanExecutionMonitor {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.monitor
}

// CreatePlan generates a new plan for the given goal.
// This base implementation creates an empty plan with the given goal.
// Specialized planners should override this method to implement custom planning logic.
func (p *BasePlanner) CreatePlan(ctx context.Context, goal string, options map[string]interface{}) (Plan, error) {
	if goal == "" {
		return nil, ErrInvalidGoal
	}

	// Generate a plan ID based on timestamp and goal
	planID := fmt.Sprintf("plan-%d", time.Now().UnixNano())

	// Create a new plan
	plan := NewBasePlan(planID, goal)

	// Store the plan
	p.mu.Lock()
	p.plans[planID] = plan
	p.mu.Unlock()

	return plan, nil
}

// ExecutePlan executes the given plan.
// This base implementation simply executes tasks in dependency order.
// Specialized planners should override this method for custom execution logic.
func (p *BasePlanner) ExecutePlan(ctx context.Context, plan Plan) error {
	if plan == nil {
		return ErrInvalidPlan
	}

	// Get the monitor
	p.mu.RLock()
	monitor := p.monitor
	p.mu.RUnlock()

	// Update plan status
	plan.SetStatus(StatusInProgress)

	// Notify plan start
	if monitor != nil {
		monitor.OnPlanStart(plan)
	}

	// Convert the plan to a BasePlan to access its extended methods
	basePlan, ok := plan.(*BasePlan)
	if !ok {
		// If not a BasePlan, we'll need to implement our own topological sort
		return p.executeTasksManually(ctx, plan, monitor)
	}

	// Get tasks in execution order
	tasks, err := basePlan.GetTasksInExecutionOrder()
	if err != nil {
		// Plan failed
		plan.SetStatus(StatusFailed)

		// Notify plan failure
		if monitor != nil {
			monitor.OnPlanFail(plan, err)
		}

		return fmt.Errorf("%w: %v", ErrPlanExecutionFailed, err)
	}

	// Execute tasks in order
	for _, task := range tasks {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			// Mark plan as failed due to cancellation
			plan.SetStatus(StatusCancelled)

			// Notify plan failure
			if monitor != nil {
				monitor.OnPlanFail(plan, ctx.Err())
			}

			return ctx.Err()
		default:
			// Continue execution
		}

		// Update task status
		task.SetStatus(StatusInProgress)

		// Notify task start
		if monitor != nil {
			monitor.OnTaskStart(task)
		}

		// Execute each step in the task
		if err := p.executeSteps(ctx, task, monitor); err != nil {
			// Task failed
			task.SetStatus(StatusFailed)

			// Notify task failure
			if monitor != nil {
				monitor.OnTaskFail(task, err)
			}

			// Plan failed
			plan.SetStatus(StatusFailed)

			// Notify plan failure
			if monitor != nil {
				monitor.OnPlanFail(plan, err)
			}

			return fmt.Errorf("%w: task %s failed: %v", ErrPlanExecutionFailed, task.ID(), err)
		}

		// Task succeeded
		task.SetStatus(StatusCompleted)

		// Notify task completion
		if monitor != nil {
			monitor.OnTaskComplete(task)
		}
	}

	// Plan completed successfully
	plan.SetStatus(StatusCompleted)

	// Notify plan completion
	if monitor != nil {
		monitor.OnPlanComplete(plan)
	}

	return nil
}

// executeTasksManually executes tasks in the correct order for non-BasePlan plans.
func (p *BasePlanner) executeTasksManually(ctx context.Context, plan Plan, monitor PlanExecutionMonitor) error {
	// Simple implementation that doesn't handle dependencies
	// Specialized planners should override ExecutePlan with better handling

	tasks := plan.Tasks()

	for _, task := range tasks {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Continue execution
		}

		// Update task status
		task.SetStatus(StatusInProgress)

		// Notify task start
		if monitor != nil {
			monitor.OnTaskStart(task)
		}

		// Execute steps
		if err := p.executeSteps(ctx, task, monitor); err != nil {
			// Task failed
			task.SetStatus(StatusFailed)

			// Notify task failure
			if monitor != nil {
				monitor.OnTaskFail(task, err)
			}

			return err
		}

		// Task succeeded
		task.SetStatus(StatusCompleted)

		// Notify task completion
		if monitor != nil {
			monitor.OnTaskComplete(task)
		}
	}

	return nil
}

// executeSteps executes all steps in a task.
func (p *BasePlanner) executeSteps(ctx context.Context, task Task, monitor PlanExecutionMonitor) error {
	steps := task.Steps()

	for _, step := range steps {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Continue execution
		}

		// Update step status
		step.SetStatus(StatusInProgress)

		// Notify step start
		if monitor != nil {
			monitor.OnStepStart(step)
		}

		// This is a placeholder for step execution
		// In a real implementation, this would execute the step's action
		// using the provided parameters

		// For now, we'll just succeed immediately
		// Specialized planners should implement actual execution logic

		// Set the step as completed
		step.SetStatus(StatusCompleted)

		// Notify step completion
		if monitor != nil {
			monitor.OnStepComplete(step)
		}
	}

	return nil
}

// UpdatePlan updates an existing plan based on new information.
// This base implementation simply replaces the goal.
// Specialized planners should override this method for more advanced plan updates.
func (p *BasePlanner) UpdatePlan(ctx context.Context, plan Plan, newGoal string, options map[string]interface{}) (Plan, error) {
	if plan == nil {
		return nil, ErrInvalidPlan
	}

	if newGoal == "" {
		return nil, ErrInvalidGoal
	}

	// Create a new plan with the new goal
	updatedPlan := NewBasePlan(plan.ID(), newGoal)

	// Copy tasks from the original plan
	for _, task := range plan.Tasks() {
		if err := updatedPlan.AddTask(task); err != nil {
			return nil, err
		}
	}

	// Store the updated plan
	p.mu.Lock()
	p.plans[plan.ID()] = updatedPlan
	p.mu.Unlock()

	return updatedPlan, nil
}

// ListPlans returns a list of all plans.
func (p *BasePlanner) ListPlans() []Plan {
	p.mu.RLock()
	defer p.mu.RUnlock()

	plans := make([]Plan, 0, len(p.plans))
	for _, plan := range p.plans {
		plans = append(plans, plan)
	}

	return plans
}

// GetPlan retrieves a plan by ID.
func (p *BasePlanner) GetPlan(planID string) (Plan, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	plan, ok := p.plans[planID]
	return plan, ok
}

// RemovePlan removes a plan by ID.
func (p *BasePlanner) RemovePlan(planID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.plans[planID]; !exists {
		return ErrPlanNotFound
	}

	delete(p.plans, planID)
	return nil
}
