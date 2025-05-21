package react

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestReactMemoryWrapper(t *testing.T) {
	// Create a base memory
	baseMemory := memory.NewBaseMemory()

	// Create a wrapper around it
	wrapper := NewReactMemoryWrapper(baseMemory)

	// Test that it implements both Memory and ReactMemory interfaces
	var _ memory.Memory = wrapper
	var _ Memory = wrapper

	// Create context
	ctx := context.Background()

	// Test storing a regular message
	regularMsg := message.NewUserMessage("Hello")
	err := wrapper.Store(ctx, regularMsg)
	require.NoError(t, err)

	// Should be stored in base memory
	msgs, err := baseMemory.Retrieve(ctx)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Content)

	// But no ReactSteps yet
	steps, err := wrapper.RetrieveSteps(ctx)
	require.NoError(t, err)
	assert.Empty(t, steps)

	// Create a step
	step := &Step{
		Thought: "I need to test this",
		Action:  "test",
		ActionParams: map[string]interface{}{
			"param1": "value1",
		},
		Observation: NewTextObservation("Test result", "test"),
	}

	// Store step directly
	err = wrapper.StoreStep(ctx, step)
	require.NoError(t, err)

	// Check that it's stored
	steps, err = wrapper.RetrieveSteps(ctx)
	require.NoError(t, err)
	assert.Len(t, steps, 1)
	assert.Equal(t, "I need to test this", steps[0].Thought)

	// Test last step
	lastStep, err := wrapper.LastStep(ctx)
	require.NoError(t, err)
	require.NotNil(t, lastStep)
	assert.Equal(t, "test", lastStep.Action)

	// Test storing a step through a message
	stepMsg, err := CreateReactStepMessage(step, message.RoleAssistant)
	require.NoError(t, err)

	err = wrapper.Store(ctx, stepMsg)
	require.NoError(t, err)

	// Should now have 2 steps
	steps, err = wrapper.RetrieveSteps(ctx)
	require.NoError(t, err)
	assert.Len(t, steps, 2)

	// But only 2 messages in the base memory
	msgs, err = baseMemory.Retrieve(ctx)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)

	// Test clear
	err = wrapper.Clear(ctx)
	require.NoError(t, err)

	// Both steps and messages should be cleared
	steps, err = wrapper.RetrieveSteps(ctx)
	require.NoError(t, err)
	assert.Empty(t, steps)

	msgs, err = baseMemory.Retrieve(ctx)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestReactMemoryWrapper_NilStep(t *testing.T) {
	// Create wrapper
	wrapper := NewReactMemoryWrapper(memory.NewBaseMemory())

	// Test storing nil step (should be a no-op)
	err := wrapper.StoreStep(context.Background(), nil)
	assert.NoError(t, err)

	// No steps should be stored
	steps, err := wrapper.RetrieveSteps(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, steps)
}

func TestReactMemoryWrapper_LastStepEmpty(t *testing.T) {
	// Create wrapper
	wrapper := NewReactMemoryWrapper(memory.NewBaseMemory())

	// Test getting last step when empty
	lastStep, err := wrapper.LastStep(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, lastStep)
}
