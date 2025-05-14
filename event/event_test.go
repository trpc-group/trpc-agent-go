package event

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

func TestNewEvent(t *testing.T) {
	data := map[string]string{"key": "value"}
	event := NewEvent(TypeSystem, data)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, TypeSystem, event.Type)
	assert.NotZero(t, event.CreatedAt)
	assert.Equal(t, data, event.Data)
	assert.NotNil(t, event.Metadata)
}

func TestNewCustomEvent(t *testing.T) {
	data := map[string]string{"key": "value"}
	event := NewCustomEvent("custom-event", data)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, TypeCustom, event.Type)
	assert.Equal(t, Name("custom-event"), event.Name)
	assert.NotZero(t, event.CreatedAt)
	assert.Equal(t, data, event.Data)
	assert.NotNil(t, event.Metadata)
}

func TestNewMessageEvent(t *testing.T) {
	msg := message.NewUserMessage("Hello, world!")
	event := NewMessageEvent(msg)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, TypeMessage, event.Type)
	assert.NotZero(t, event.CreatedAt)

	// Check that the message is stored in Data
	assert.Equal(t, msg, event.Data)

	// Check that message role is stored in metadata
	role, ok := event.GetMetadata("message_role")
	assert.True(t, ok)
	assert.Equal(t, message.RoleUser, role)
}

func TestNewErrorEvent(t *testing.T) {
	err := errors.New("test error")
	event := NewErrorEvent(err, 500)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, TypeError, event.Type)
	assert.NotZero(t, event.CreatedAt)

	// Check that error details are stored in metadata
	errStr, ok := event.GetMetadata("error")
	assert.True(t, ok)
	assert.Equal(t, "test error", errStr)

	errCode, ok := event.GetMetadata("error_code")
	assert.True(t, ok)
	assert.Equal(t, 500, errCode)
}

func TestNewStreamEvent(t *testing.T) {
	event := NewStreamEvent("stream content")

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, Type(TypeStream), event.Type)
	assert.NotZero(t, event.CreatedAt)

	// Check that content is stored in metadata
	content, ok := event.GetMetadata("content")
	assert.True(t, ok)
	assert.Equal(t, "stream content", content)
}

func TestNewLoopIterationEvent(t *testing.T) {
	event := NewLoopIterationEvent(5)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, Type(TypeLoopIteration), event.Type)
	assert.NotZero(t, event.CreatedAt)

	// Check that iteration number is stored in metadata
	iteration, ok := event.GetMetadata("iteration")
	assert.True(t, ok)
	assert.Equal(t, 5, iteration)
}

func TestNewAgentStartEvent(t *testing.T) {
	event := NewAgentStartEvent("test-agent", 1)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, Type(TypeAgentStart), event.Type)
	assert.NotZero(t, event.CreatedAt)

	// Check that agent details are stored in metadata
	agentName, ok := event.GetMetadata("agent_name")
	assert.True(t, ok)
	assert.Equal(t, "test-agent", agentName)

	index, ok := event.GetMetadata("index")
	assert.True(t, ok)
	assert.Equal(t, 1, index)
}

func TestNewAgentEndEvent(t *testing.T) {
	event := NewAgentEndEvent("test-agent", 1)

	assert.NotEmpty(t, event.ID)
	assert.Equal(t, Type(TypeAgentEnd), event.Type)
	assert.NotZero(t, event.CreatedAt)

	// Check that agent details are stored in metadata
	agentName, ok := event.GetMetadata("agent_name")
	assert.True(t, ok)
	assert.Equal(t, "test-agent", agentName)

	index, ok := event.GetMetadata("index")
	assert.True(t, ok)
	assert.Equal(t, 1, index)
}

func TestEventMetadata(t *testing.T) {
	event := NewEvent(TypeSystem, nil)

	// Test setting and getting metadata
	event.SetMetadata("key1", "value1")
	event.SetMetadata("key2", 42)

	value1, ok := event.GetMetadata("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", value1)

	value2, ok := event.GetMetadata("key2")
	assert.True(t, ok)
	assert.Equal(t, 42, value2)

	// Test getting non-existent metadata
	value3, ok := event.GetMetadata("key3")
	assert.False(t, ok)
	assert.Nil(t, value3)

	// Test with nil metadata map
	eventWithNilMetadata := &Event{
		ID:       "test",
		Type:     TypeSystem,
		Metadata: nil,
	}

	value4, ok := eventWithNilMetadata.GetMetadata("key")
	assert.False(t, ok)
	assert.Nil(t, value4)
}

func TestEventTypes(t *testing.T) {
	// Verify the predefined event types
	assert.Equal(t, Type("message"), TypeMessage)
	assert.Equal(t, Type("tool"), TypeTool)
	assert.Equal(t, Type("error"), TypeError)
	assert.Equal(t, Type("agent"), TypeAgent)
	assert.Equal(t, Type("system"), TypeSystem)
	assert.Equal(t, Type("evaluation"), TypeEvaluation)
	assert.Equal(t, Type("custom"), TypeCustom)

	// Verify specialized agent event types
	assert.Equal(t, "stream", TypeStream)
	assert.Equal(t, "loop_iteration", TypeLoopIteration)
	assert.Equal(t, "agent_start", TypeAgentStart)
	assert.Equal(t, "agent_end", TypeAgentEnd)
}
