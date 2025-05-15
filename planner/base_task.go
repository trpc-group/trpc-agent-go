package planner

import (
	"errors"
	"sync"
)

var (
	// ErrStepNotFound is returned when a step cannot be found in a task.
	ErrStepNotFound = errors.New("step not found")
	// ErrStepAlreadyExists is returned when attempting to add a step that already exists.
	ErrStepAlreadyExists = errors.New("step already exists in task")
	// ErrInvalidStep is returned when an invalid step is provided.
	ErrInvalidStep = errors.New("invalid step")
)

// BaseTask provides a base implementation of the Task interface.
type BaseTask struct {
	id           string
	description  string
	stepMap      map[string]Step
	stepOrder    []string
	dependencies []string
	status       Status
	priority     int
	metadata     map[string]interface{}
	mu           sync.RWMutex
}

// NewBaseTask creates a new instance of BaseTask with the given ID and description.
func NewBaseTask(id, description string) *BaseTask {
	return &BaseTask{
		id:           id,
		description:  description,
		stepMap:      make(map[string]Step),
		stepOrder:    make([]string, 0),
		dependencies: make([]string, 0),
		status:       StatusNotStarted,
		priority:     0,
		metadata:     make(map[string]interface{}),
	}
}

// ID returns the unique identifier for this task.
func (t *BaseTask) ID() string {
	return t.id
}

// Description returns the description of the task.
func (t *BaseTask) Description() string {
	return t.description
}

// Steps returns the ordered steps to complete this task.
func (t *BaseTask) Steps() []Step {
	t.mu.RLock()
	defer t.mu.RUnlock()

	steps := make([]Step, 0, len(t.stepOrder))
	for _, id := range t.stepOrder {
		if step, ok := t.stepMap[id]; ok {
			steps = append(steps, step)
		}
	}
	return steps
}

// AddStep adds a new step to the task.
func (t *BaseTask) AddStep(step Step) error {
	if step == nil {
		return ErrInvalidStep
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	stepID := step.ID()
	if _, exists := t.stepMap[stepID]; exists {
		return ErrStepAlreadyExists
	}

	t.stepMap[stepID] = step
	t.stepOrder = append(t.stepOrder, stepID)
	return nil
}

// RemoveStep removes a step from the task by ID.
func (t *BaseTask) RemoveStep(stepID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.stepMap[stepID]; !exists {
		return ErrStepNotFound
	}

	// Remove from map
	delete(t.stepMap, stepID)

	// Remove from order slice
	for i, id := range t.stepOrder {
		if id == stepID {
			t.stepOrder = append(t.stepOrder[:i], t.stepOrder[i+1:]...)
			break
		}
	}

	return nil
}

// GetStep retrieves a step by ID.
func (t *BaseTask) GetStep(stepID string) (Step, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	step, ok := t.stepMap[stepID]
	return step, ok
}

// Dependencies returns IDs of tasks that must be completed before this task.
func (t *BaseTask) Dependencies() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Return a copy to prevent modification of the internal slice
	deps := make([]string, len(t.dependencies))
	copy(deps, t.dependencies)
	return deps
}

// AddDependency adds a task dependency.
func (t *BaseTask) AddDependency(taskID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if dependency already exists
	for _, dep := range t.dependencies {
		if dep == taskID {
			return nil // Already exists, no error
		}
	}

	t.dependencies = append(t.dependencies, taskID)
	return nil
}

// RemoveDependency removes a task dependency.
func (t *BaseTask) RemoveDependency(taskID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Find and remove the dependency
	for i, dep := range t.dependencies {
		if dep == taskID {
			t.dependencies = append(t.dependencies[:i], t.dependencies[i+1:]...)
			return nil
		}
	}

	return nil // Not found, but not an error
}

// Status returns the current status of the task.
func (t *BaseTask) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.status
}

// SetStatus updates the status of the task.
func (t *BaseTask) SetStatus(status Status) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.status = status
}

// Priority returns the priority level of the task.
func (t *BaseTask) Priority() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.priority
}

// SetPriority sets the priority level of the task.
func (t *BaseTask) SetPriority(priority int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.priority = priority
}

// Metadata returns the metadata associated with the task.
func (t *BaseTask) Metadata() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Create a copy to avoid concurrent map access issues
	metadataCopy := make(map[string]interface{}, len(t.metadata))
	for k, v := range t.metadata {
		metadataCopy[k] = v
	}

	return metadataCopy
}

// SetMetadata sets or updates metadata for the task.
func (t *BaseTask) SetMetadata(key string, value interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.metadata == nil {
		t.metadata = make(map[string]interface{})
	}

	t.metadata[key] = value
}
