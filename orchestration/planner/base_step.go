package planner

import (
	"sync"
)

// BaseStep provides a base implementation of the Step interface.
type BaseStep struct {
	id          string
	description string
	action      string
	parameters  map[string]interface{}
	status      Status
	result      interface{}
	metadata    map[string]interface{}
	mu          sync.RWMutex
}

// NewBaseStep creates a new instance of BaseStep with the given ID, description, and action.
func NewBaseStep(id, description, action string) *BaseStep {
	return &BaseStep{
		id:          id,
		description: description,
		action:      action,
		parameters:  make(map[string]interface{}),
		status:      StatusNotStarted,
		metadata:    make(map[string]interface{}),
	}
}

// ID returns the unique identifier for this step.
func (s *BaseStep) ID() string {
	return s.id
}

// Description returns the description of the step.
func (s *BaseStep) Description() string {
	return s.description
}

// Action returns the action to be performed in this step.
func (s *BaseStep) Action() string {
	return s.action
}

// Parameters returns the parameters for the step's action.
func (s *BaseStep) Parameters() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy to avoid concurrent map access issues
	paramsCopy := make(map[string]interface{}, len(s.parameters))
	for k, v := range s.parameters {
		paramsCopy[k] = v
	}

	return paramsCopy
}

// SetParameter sets or updates a parameter for the step.
func (s *BaseStep) SetParameter(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.parameters == nil {
		s.parameters = make(map[string]interface{})
	}

	s.parameters[key] = value
}

// Status returns the current status of the step.
func (s *BaseStep) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.status
}

// SetStatus updates the status of the step.
func (s *BaseStep) SetStatus(status Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = status
}

// Result returns the result of executing the step.
func (s *BaseStep) Result() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.result
}

// SetResult sets the result of the step execution.
func (s *BaseStep) SetResult(result interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.result = result
}

// Metadata returns the metadata associated with the step.
func (s *BaseStep) Metadata() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy to avoid concurrent map access issues
	metadataCopy := make(map[string]interface{}, len(s.metadata))
	for k, v := range s.metadata {
		metadataCopy[k] = v
	}

	return metadataCopy
}

// SetMetadata sets or updates metadata for the step.
func (s *BaseStep) SetMetadata(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.metadata == nil {
		s.metadata = make(map[string]interface{})
	}

	s.metadata[key] = value
}
