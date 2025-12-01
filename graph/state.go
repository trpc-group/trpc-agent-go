//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"fmt"
	"reflect"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// StateKeyUserInput is the key of the user input.
	// It is consumed once and then cleared after successful LLM execution.
	StateKeyUserInput = "user_input"
	// StateKeyOneShotMessages is the key for one-shot messages that override
	// the current round input completely. It is consumed once and then cleared.
	StateKeyOneShotMessages = "one_shot_messages"
	// StateKeyLastResponse is the key of the last response.
	StateKeyLastResponse = "last_response"
	// StateKeyNodeResponses is the key of the node responses.
	StateKeyNodeResponses = "node_responses"
	// StateKeySession is the key of the session.
	StateKeySession = "session"
	// StateKeyMessages is the key of the messages.
	// Typically it is used and updated by the LLM node.
	StateKeyMessages = "messages"
	// StateKeyLastResponseID stores the ID of the last model response.
	StateKeyLastResponseID = "last_response_id"
	// StateKeyMetadata is the key of the metadata.
	StateKeyMetadata = "metadata"
	// StateKeyExecContext is the key of the execution context.
	StateKeyExecContext = "exec_context"
	// StateKeyToolCallbacks is the key of the tool callbacks.
	StateKeyToolCallbacks = "tool_callbacks"
	// StateKeyModelCallbacks is the key of the model callbacks.
	StateKeyModelCallbacks = "model_callbacks"
	// StateKeyAgentCallbacks is the key of the agent callbacks.
	StateKeyAgentCallbacks = "agent_callbacks"
	// StateKeyCurrentNodeID is the key for storing the current node ID in the state.
	StateKeyCurrentNodeID = "current_node_id"
	// StateKeyParentAgent is the key for storing the parent GraphAgent that owns sub-agents.
	StateKeyParentAgent = "parent_agent"
)

// State represents the state that flows through the graph.
// This is the shared data structure that flows between nodes.
type State map[string]any

// GetStateValue retrieves a typed value from the state.
//
// Returns the typed value and true if the key exists and the type matches,
// or the zero value and false otherwise.
//
// Example:
//
//	if messages, ok := GetStateValue[[]model.Message](state, StateKeyMessages); ok {
//	    // use messages
//	}
//	if userInput, ok := GetStateValue[string](state, StateKeyUserInput); ok {
//	    // use userInput
//	}
func GetStateValue[T any](s State, key string) (T, bool) {
	return util.GetMapValue[string, T](s, key)
}

// Clone creates a deep copy of the state.
func (s State) Clone() State {
	clone := make(State, len(s))
	for k, v := range s {
		clone[k] = v
	}
	return clone
}

func (s State) safeClone() State {
	clone := make(State, len(s))
	for k, v := range s {
		if isUnsafeStateKey(k) {
			continue
		}
		clone[k] = v
	}
	return clone
}

func (s State) deepCopy(retainUnsafeKey bool, fields map[string]StateField) State {
	isDisableDeepCopyKey := func(key string) bool {
		if field, ok := fields[key]; ok {
			return field.DisableDeepCopy
		}
		return false
	}

	tmp := make(State, len(s))
	for k, v := range s {
		unsafeKey := isUnsafeStateKey(k)
		if unsafeKey && !retainUnsafeKey {
			continue
		}

		if unsafeKey || isDisableDeepCopyKey(k) {
			tmp[k] = v
			continue
		}
		tmp[k] = deepCopyAny(v)
	}

	return tmp
}

// StateReducer is a function that determines how state updates are merged.
// It takes existing and new values and returns the merged result.
type StateReducer func(existing, update any) any

// StateField defines a field in the state schema with its type and reducer.
type StateField struct {
	Type            reflect.Type
	Reducer         StateReducer
	Default         func() any
	Required        bool
	DisableDeepCopy bool
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
			// Ignore internal/ephemeral keys in updates. They are owned by
			// the executor and may contain concurrently-mutated maps.
			if isInternalStateKey(key) {
				continue
			}
			// If no field definition, use default behavior (override) with
			// deep copy to avoid sharing mutable references across goroutines.
			result[key] = deepCopyAny(updateValue)
			continue
		}
		currentValue, hasCurrentValue := result[key]
		if !hasCurrentValue && field.Default != nil {
			currentValue = field.Default()
		}

		if field.DisableDeepCopy {
			result[key] = field.Reducer(currentValue, updateValue)
			continue
		}

		// Apply reducer with deep-copied update to prevent reference sharing.
		safeUpdate := deepCopyAny(updateValue)
		merged := field.Reducer(currentValue, safeUpdate)
		// Ensure merged complex values are not shared by taking a deep copy.
		switch merged.(type) {
		case map[string]any, []any, []string, []int, []float64:
			result[key] = deepCopyAny(merged)
		default:
			result[key] = merged
		}
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

// validateSchema validates the schema struct.
func (s *StateSchema) validateSchema() error {
	if s == nil {
		return fmt.Errorf("graph must have a state schema")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for name, field := range s.Fields {
		// Validate that Type and Reducer are not nil.
		if field.Type == nil {
			return fmt.Errorf("field %s has nil type", name)
		}
		if field.Reducer == nil {
			return fmt.Errorf("field %s has nil reducer", name)
		}

		// Validate that Default is assignable to Type.
		if field.Default != nil {
			defaultValue := field.Default()
			if defaultValue == nil {
				kind := field.Type.Kind()
				switch kind {
				case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
				default:
					return fmt.Errorf("field %s has incompatible default value: nil is not assignable to type %v", name, field.Type)
				}
			} else {
				defaultType := reflect.TypeOf(defaultValue)
				if !defaultType.AssignableTo(field.Type) {
					return fmt.Errorf("field %s has incompatible default value: expected %v, got %v",
						name, field.Type, defaultType)
				}
			}
		}
	}
	return nil
}

// Common reducer functions.

// DefaultReducer overwrites the existing value with the update.
func DefaultReducer(existing, update any) any {
	// For composite types, return a deep copy to avoid shared references.
	switch update.(type) {
	case map[string]any, []any, []string, []int, []float64:
		return deepCopyAny(update)
	}
	return update
}

// CoverReducer overwrites the existing value with the update.
func CoverReducer(existing, update any) any {
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
		// Fallback to default behavior; deep copy if composite
		return deepCopyAny(update)
	}

	result := make(map[string]any, len(existingMap)+len(updateMap))
	for k, v := range existingMap {
		result[k] = deepCopyAny(v)
	}
	for k, v := range updateMap {
		result[k] = deepCopyAny(v)
	}
	return result
}

// MessageReducer handles message arrays with ID-based updates and MessageOp support.
func MessageReducer(existing, update any) any {
	if existing == nil {
		existing = []model.Message{}
	}
	existingMsgs, ok1 := existing.([]model.Message)
	if !ok1 {
		return update
	}
	switch x := update.(type) {
	case nil:
		// no-op
		return existingMsgs
	case model.Message:
		return append(existingMsgs, x)
	case []model.Message:
		return append(existingMsgs, x...)
	case MessageOp:
		return x.Apply(existingMsgs)
	case []MessageOp:
		result := existingMsgs
		for _, op := range x {
			result = op.Apply(result)
		}
		return result
	default:
		// Fallback to default behavior for unsupported types
		return update
	}
}
