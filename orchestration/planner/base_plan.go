package planner

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrTaskNotFound is returned when a task cannot be found in a plan.
	ErrTaskNotFound = errors.New("task not found")
	// ErrTaskAlreadyExists is returned when attempting to add a task that already exists.
	ErrTaskAlreadyExists = errors.New("task already exists in plan")
	// ErrInvalidTask is returned when an invalid task is provided.
	ErrInvalidTask = errors.New("invalid task")
)

// BasePlan provides a base implementation of the Plan interface.
type BasePlan struct {
	id        string
	goal      string
	taskMap   map[string]Task
	taskOrder []string
	status    Status
	createdAt time.Time
	updatedAt time.Time
	metadata  map[string]interface{}
	mu        sync.RWMutex
}

// NewBasePlan creates a new instance of BasePlan with the given ID and goal.
func NewBasePlan(id, goal string) *BasePlan {
	now := time.Now()
	return &BasePlan{
		id:        id,
		goal:      goal,
		taskMap:   make(map[string]Task),
		taskOrder: make([]string, 0),
		status:    StatusNotStarted,
		createdAt: now,
		updatedAt: now,
		metadata:  make(map[string]interface{}),
	}
}

// ID returns the unique identifier for this plan.
func (p *BasePlan) ID() string {
	return p.id
}

// Goal returns the goal statement of the plan.
func (p *BasePlan) Goal() string {
	return p.goal
}

// Tasks returns the list of tasks in this plan.
func (p *BasePlan) Tasks() []Task {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tasks := make([]Task, 0, len(p.taskOrder))
	for _, id := range p.taskOrder {
		if task, ok := p.taskMap[id]; ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// AddTask adds a new task to the plan.
func (p *BasePlan) AddTask(task Task) error {
	if task == nil {
		return ErrInvalidTask
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	taskID := task.ID()
	if _, exists := p.taskMap[taskID]; exists {
		return ErrTaskAlreadyExists
	}

	p.taskMap[taskID] = task
	p.taskOrder = append(p.taskOrder, taskID)
	p.updatedAt = time.Now()
	return nil
}

// RemoveTask removes a task from the plan by ID.
func (p *BasePlan) RemoveTask(taskID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.taskMap[taskID]; !exists {
		return ErrTaskNotFound
	}

	// Remove from map
	delete(p.taskMap, taskID)

	// Remove from order slice
	for i, id := range p.taskOrder {
		if id == taskID {
			p.taskOrder = append(p.taskOrder[:i], p.taskOrder[i+1:]...)
			break
		}
	}

	p.updatedAt = time.Now()
	return nil
}

// GetTask retrieves a task by ID.
func (p *BasePlan) GetTask(taskID string) (Task, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	task, ok := p.taskMap[taskID]
	return task, ok
}

// Status returns the current status of the plan.
func (p *BasePlan) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.status
}

// SetStatus updates the status of the plan.
func (p *BasePlan) SetStatus(status Status) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.status = status
	p.updatedAt = time.Now()
}

// CreatedAt returns the creation time of the plan.
func (p *BasePlan) CreatedAt() time.Time {
	return p.createdAt
}

// UpdatedAt returns the last update time of the plan.
func (p *BasePlan) UpdatedAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.updatedAt
}

// Metadata returns the metadata associated with the plan.
func (p *BasePlan) Metadata() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Create a copy to avoid concurrent map access issues
	metadataCopy := make(map[string]interface{}, len(p.metadata))
	for k, v := range p.metadata {
		metadataCopy[k] = v
	}

	return metadataCopy
}

// SetMetadata sets or updates the metadata for the plan.
func (p *BasePlan) SetMetadata(key string, value interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.metadata == nil {
		p.metadata = make(map[string]interface{})
	}

	p.metadata[key] = value
	p.updatedAt = time.Now()
}

// GetTasksInExecutionOrder returns tasks ordered by dependencies and priority.
func (p *BasePlan) GetTasksInExecutionOrder() ([]Task, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Create a copy of the task map to work with
	taskMap := make(map[string]Task, len(p.taskMap))
	for id, task := range p.taskMap {
		taskMap[id] = task
	}

	// Track visited and temporaryMarks for cycle detection
	visited := make(map[string]bool)
	tempMarks := make(map[string]bool)

	// Result will hold tasks in execution order
	var result []Task

	// Visit function for depth-first traversal
	var visit func(string) error
	visit = func(id string) error {
		// Check if the node is already visited
		if visited[id] {
			return nil
		}

		// Check for cycles
		if tempMarks[id] {
			return fmt.Errorf("cyclic dependency detected in tasks involving task '%s'", id)
		}

		// Task doesn't exist
		task, exists := taskMap[id]
		if !exists {
			return fmt.Errorf("task '%s' referenced as dependency but not found in plan", id)
		}

		// Mark temporarily for cycle detection
		tempMarks[id] = true

		// Visit all dependencies first
		for _, depID := range task.Dependencies() {
			if err := visit(depID); err != nil {
				return err
			}
		}

		// Remove temporary mark and add to visited
		delete(tempMarks, id)
		visited[id] = true

		// Add to result
		result = append(result, task)
		return nil
	}

	// Create a list of tasks sorted by priority (highest first)
	type prioritizedTask struct {
		id       string
		priority int
	}

	prioritized := make([]prioritizedTask, 0, len(taskMap))
	for id, task := range taskMap {
		prioritized = append(prioritized, prioritizedTask{id, task.Priority()})
	}

	// Sort by priority (highest first)
	for i := 0; i < len(prioritized); i++ {
		for j := i + 1; j < len(prioritized); j++ {
			if prioritized[i].priority < prioritized[j].priority {
				prioritized[i], prioritized[j] = prioritized[j], prioritized[i]
			}
		}
	}

	// Process all tasks in priority order
	for _, pt := range prioritized {
		if !visited[pt.id] {
			if err := visit(pt.id); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}
