package graph

import (
	"fmt"
	"reflect"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// StateKeyUserInput is the key of the user input.
	// Typically it remains constant across the graph.
	StateKeyUserInput = "user_input"
	// StateKeyLastResponse is the key of the last response.
	StateKeyLastResponse = "last_response"
	// StateKeySession is the key of the session.
	StateKeySession = "session"
	// StateKeyMessages is the key of the messages.
	// Typically it is used and updated by the LLM node.
	StateKeyMessages = "messages"
	// StateKeyMetadata is the key of the metadata.
	StateKeyMetadata = "metadata"
	// StateKeyExecContext is the key of the execution context.
	StateKeyExecContext = "exec_context"
	// StateKeyToolCallbacks is the key of the tool callbacks.
	StateKeyToolCallbacks = "tool_callbacks"
	// StateKeyModelCallbacks is the key of the model callbacks.
	StateKeyModelCallbacks = "model_callbacks"
	// StateKeyCurrentNodeID is the key for storing the current node ID in the state.
	StateKeyCurrentNodeID = "current_node_id"
)

// State represents the state that flows through the graph.
// This is the shared data structure that flows between nodes.
type State map[string]any

// Clone creates a deep copy of the state.
func (s State) Clone() State {
	clone := make(State)
	for k, v := range s {
		clone[k] = v
	}
	return clone
}

// StateReducer is a function that determines how state updates are merged.
// It takes existing and new values and returns the merged result.
type StateReducer func(existing, update any) any

// StateField defines a field in the state schema with its type and reducer.
type StateField struct {
	Type     reflect.Type
	Reducer  StateReducer
	Default  func() any
	Required bool
}

// StateSchema defines the structure and behavior of graph state.
// This defines the structure and behavior of state.
type StateSchema struct {
	mu     sync.RWMutex
	Fields map[string]StateField
}

// NewStateSchema creates a new state schema.
func NewStateSchema() *StateSchema {
	return &StateSchema{
		Fields: make(map[string]StateField),
	}
}

// AddField adds a field to the state schema.
func (s *StateSchema) AddField(name string, field StateField) *StateSchema {
	s.mu.Lock()
	defer s.mu.Unlock()

	if field.Reducer == nil {
		field.Reducer = DefaultReducer
	}

	s.Fields[name] = field
	return s
}

// ApplyUpdate applies a state update using the defined reducers.
func (s *StateSchema) ApplyUpdate(currentState State, update State) State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := currentState.Clone()
	for key, updateValue := range update {
		field, exists := s.Fields[key]
		if !exists {
			// If no field definition, use default behavior (override).
			result[key] = updateValue
			continue
		}
		currentValue, hasCurrentValue := result[key]
		if !hasCurrentValue && field.Default != nil {
			currentValue = field.Default()
		}
		// Apply reducer.
		result[key] = field.Reducer(currentValue, updateValue)
	}
	return result
}

// Validate validates a state against the schema.
func (s *StateSchema) Validate(state State) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for name, field := range s.Fields {
		value, exists := state[name]

		if field.Required && !exists {
			return fmt.Errorf("required field %s is missing", name)
		}

		if exists && value != nil {
			valueType := reflect.TypeOf(value)
			if !valueType.AssignableTo(field.Type) {
				return fmt.Errorf("field %s has wrong type: expected %v, got %v",
					name, field.Type, valueType)
			}
		}
	}
	return nil
}

// Common reducer functions.

// DefaultReducer overwrites the existing value with the update.
func DefaultReducer(existing, update any) any {
	return update
}

// AppendReducer appends update to existing slice.
func AppendReducer(existing, update any) any {
	if existing == nil {
		existing = []any{}
	}

	existingSlice, ok1 := existing.([]any)
	updateSlice, ok2 := update.([]any)

	if !ok1 || !ok2 {
		// Fallback to default behavior if not slices
		return update
	}
	return append(existingSlice, updateSlice...)
}

// StringSliceReducer appends string slices specifically.
func StringSliceReducer(existing, update any) any {
	if existing == nil {
		existing = []string{}
	}

	existingSlice, ok1 := existing.([]string)
	updateSlice, ok2 := update.([]string)

	if !ok1 || !ok2 {
		// Fallback to default behavior if not string slices
		return update
	}
	return append(existingSlice, updateSlice...)
}

// MergeReducer merges update map into existing map.
func MergeReducer(existing, update any) any {
	if existing == nil {
		existing = make(map[string]any)
	}

	existingMap, ok1 := existing.(map[string]any)
	updateMap, ok2 := update.(map[string]any)

	if !ok1 || !ok2 {
		// Fallback to default behavior if not maps
		return update
	}

	result := make(map[string]any)
	for k, v := range existingMap {
		result[k] = v
	}
	for k, v := range updateMap {
		result[k] = v
	}
	return result
}

// MessageReducer handles message arrays with ID-based updates.
func MessageReducer(existing, update any) any {
	if existing == nil {
		existing = []model.Message{}
	}
	existingMsgs, ok1 := existing.([]model.Message)
	updateMsgs, ok2 := update.([]model.Message)
	if !ok1 || !ok2 {
		return update
	}
	return append(existingMsgs, updateMsgs...)
}
