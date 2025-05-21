// Package react provides the ReAct agent implementation.
package react

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

const (
	// ReactStepMetadataKey is the key used to store ReAct steps in message metadata.
	ReactStepMetadataKey = "react_step"
)

// BaseReactMemory provides a basic implementation of the ReactMemory interface.
type BaseReactMemory struct {
	*memory.BaseMemory
	steps []*Step
	mutex sync.RWMutex
}

// NewBaseReactMemory creates a new BaseReactMemory.
func NewBaseReactMemory() *BaseReactMemory {
	return &BaseReactMemory{
		BaseMemory: memory.NewBaseMemory(),
		steps:      make([]*Step, 0),
	}
}

// Store adds a message to the memory.
func (m *BaseReactMemory) Store(ctx context.Context, msg *message.Message) error {
	// First store the message using the base implementation
	if err := m.BaseMemory.Store(ctx, msg); err != nil {
		return err
	}

	// If this message contains a ReactStep in its metadata, store that too
	if msg.Metadata != nil {
		if stepData, ok := msg.Metadata[ReactStepMetadataKey]; ok {
			// Try to unmarshal the step data
			var step Step

			switch v := stepData.(type) {
			case string:
				if err := json.Unmarshal([]byte(v), &step); err != nil {
					return fmt.Errorf("failed to unmarshal ReactStep from metadata: %w", err)
				}
			case []byte:
				if err := json.Unmarshal(v, &step); err != nil {
					return fmt.Errorf("failed to unmarshal ReactStep from metadata: %w", err)
				}
			case map[string]interface{}:
				// Convert map to JSON string, then unmarshal to ReactStep
				data, err := json.Marshal(v)
				if err != nil {
					return fmt.Errorf("failed to marshal metadata map: %w", err)
				}
				if err := json.Unmarshal(data, &step); err != nil {
					return fmt.Errorf("failed to unmarshal ReactStep from metadata map: %w", err)
				}
			case *Step:
				step = *v
			}

			// Store the step
			return m.StoreStep(ctx, &step)
		}
	}

	return nil
}

// StoreStep stores a ReAct step.
func (m *BaseReactMemory) StoreStep(ctx context.Context, step *Step) error {
	if step == nil {
		return nil
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.steps = append(m.steps, step)
	return nil
}

// RetrieveSteps retrieves all ReAct steps.
func (m *BaseReactMemory) RetrieveSteps(ctx context.Context) ([]*Step, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Make a copy to avoid race conditions
	steps := make([]*Step, len(m.steps))
	copy(steps, m.steps)
	return steps, nil
}

// LastStep retrieves the most recent ReAct step.
func (m *BaseReactMemory) LastStep(ctx context.Context) (*Step, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if len(m.steps) == 0 {
		return nil, nil
	}

	return m.steps[len(m.steps)-1], nil
}

// Clear empties the memory.
func (m *BaseReactMemory) Clear(ctx context.Context) error {
	// First clear the base memory
	if err := m.BaseMemory.Clear(ctx); err != nil {
		return err
	}

	// Then clear the steps
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.steps = make([]*Step, 0)
	return nil
}

// ReactMemoryWrapper wraps a regular Memory implementation to add ReactMemory capabilities.
type ReactMemoryWrapper struct {
	memory.Memory
	steps []*Step
	mutex sync.RWMutex
}

// NewReactMemoryWrapper creates a new wrapper that implements ReactMemory around any Memory.
func NewReactMemoryWrapper(mem memory.Memory) *ReactMemoryWrapper {
	return &ReactMemoryWrapper{
		Memory: mem,
		steps:  make([]*Step, 0),
	}
}

// Store adds a message to the memory and extracts any ReactStep data.
func (w *ReactMemoryWrapper) Store(ctx context.Context, msg *message.Message) error {
	// Store using the wrapped memory
	if err := w.Memory.Store(ctx, msg); err != nil {
		return err
	}

	// Extract ReactStep data if present
	if msg.Metadata != nil {
		if stepData, ok := msg.Metadata[ReactStepMetadataKey]; ok {
			var step Step

			switch v := stepData.(type) {
			case string:
				if err := json.Unmarshal([]byte(v), &step); err != nil {
					return fmt.Errorf("failed to unmarshal ReactStep from metadata: %w", err)
				}
			case []byte:
				if err := json.Unmarshal(v, &step); err != nil {
					return fmt.Errorf("failed to unmarshal ReactStep from metadata: %w", err)
				}
			case map[string]interface{}:
				data, err := json.Marshal(v)
				if err != nil {
					return fmt.Errorf("failed to marshal metadata map: %w", err)
				}
				if err := json.Unmarshal(data, &step); err != nil {
					return fmt.Errorf("failed to unmarshal ReactStep from metadata map: %w", err)
				}
			case *Step:
				step = *v
			}

			return w.StoreStep(ctx, &step)
		}
	}

	return nil
}

// StoreStep stores a ReAct step.
func (w *ReactMemoryWrapper) StoreStep(ctx context.Context, step *Step) error {
	if step == nil {
		return nil
	}

	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.steps = append(w.steps, step)
	return nil
}

// RetrieveSteps retrieves all ReAct steps.
func (w *ReactMemoryWrapper) RetrieveSteps(ctx context.Context) ([]*Step, error) {
	w.mutex.RLock()
	defer w.mutex.RUnlock()

	steps := make([]*Step, len(w.steps))
	copy(steps, w.steps)
	return steps, nil
}

// LastStep retrieves the most recent ReAct step.
func (w *ReactMemoryWrapper) LastStep(ctx context.Context) (*Step, error) {
	w.mutex.RLock()
	defer w.mutex.RUnlock()

	if len(w.steps) == 0 {
		return nil, nil
	}

	return w.steps[len(w.steps)-1], nil
}

// Clear empties both the wrapped memory and the steps.
func (w *ReactMemoryWrapper) Clear(ctx context.Context) error {
	if err := w.Memory.Clear(ctx); err != nil {
		return err
	}

	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.steps = make([]*Step, 0)
	return nil
}

// CreateReactStepMessage creates a message that contains a ReAct step in its metadata.
func CreateReactStepMessage(step *Step, role message.Role) (*message.Message, error) {
	if step == nil {
		return nil, fmt.Errorf("step cannot be nil")
	}

	// Create a message based on the step content
	var content string
	if step.Thought != "" {
		content = fmt.Sprintf("Thought: %s\n", step.Thought)
	}

	if step.Action != "" {
		content += fmt.Sprintf("Action: %s\n", step.Action)
		// Add action parameters if present
		if len(step.ActionParams) > 0 {
			paramsJSON, err := json.Marshal(step.ActionParams)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal action parameters: %w", err)
			}
			content += fmt.Sprintf("Action Input: %s\n", string(paramsJSON))
		}
	}

	if step.Observation != nil {
		content += fmt.Sprintf("Observation: %s\n", step.Observation.Content)
	}

	// Create metadata with the ReactStep
	metadata := map[string]interface{}{
		ReactStepMetadataKey: step,
	}

	// Create message with the appropriate role
	var msg *message.Message
	switch role {
	case message.RoleUser:
		msg = message.NewUserMessage(content)
	case message.RoleAssistant:
		msg = message.NewAssistantMessage(content)
	case message.RoleSystem:
		msg = message.NewSystemMessage(content)
	default:
		msg = message.NewAssistantMessage(content)
	}

	// Add the metadata
	msg.Metadata = metadata

	return msg, nil
}
