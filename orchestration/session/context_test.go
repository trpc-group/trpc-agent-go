package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestSessionContext(t *testing.T) {
	// Create a base context
	ctx := context.Background()

	// Create test messages
	msg1 := message.NewUserMessage("Hello")
	msg2 := message.NewAssistantMessage("Hi there")
	history := []*message.Message{msg1, msg2}

	// Create a session context
	sessionID := "test-session-123"
	sessCtx := NewContext(ctx, sessionID, history)

	// Test basic properties
	assert.Equal(t, sessionID, sessCtx.SessionID())
	assert.Equal(t, history, sessCtx.History())
	assert.NotNil(t, sessCtx.State())
	assert.Empty(t, sessCtx.State())

	// Test adding state
	sessCtx2 := sessCtx.WithState("key1", "value1")
	assert.Equal(t, "value1", sessCtx2.State()["key1"])

	// Original context should be unchanged
	assert.Empty(t, sessCtx.State())

	// Test adding multiple state values
	sessCtx3 := sessCtx2.WithState("key2", 123)
	assert.Equal(t, "value1", sessCtx3.State()["key1"])
	assert.Equal(t, 123, sessCtx3.State()["key2"])

	// Test merging state maps
	stateMap := map[string]interface{}{
		"key3": true,
		"key4": 3.14,
	}
	sessCtx4 := sessCtx3.WithStateMap(stateMap)
	assert.Equal(t, "value1", sessCtx4.State()["key1"])
	assert.Equal(t, 123, sessCtx4.State()["key2"])
	assert.Equal(t, true, sessCtx4.State()["key3"])
	assert.Equal(t, 3.14, sessCtx4.State()["key4"])

	// Test standard context methods
	deadline, ok := sessCtx4.Deadline()
	assert.False(t, ok)
	assert.Equal(t, time.Time{}, deadline)

	assert.Nil(t, sessCtx4.Done())
	assert.Nil(t, sessCtx4.Err())

	// Test the helper functions
	assert.Equal(t, sessionID, SessionIDFromContext(sessCtx4))
	assert.Equal(t, history, HistoryFromContext(sessCtx4))
	assert.Equal(t, sessCtx4.State(), StateFromContext(sessCtx4))
	assert.Equal(t, "value1", ValueFromState(sessCtx4, "key1"))
	assert.Equal(t, 123, ValueFromState(sessCtx4, "key2"))

	// Test with non-session context
	plainCtx := context.Background()
	assert.Equal(t, "", SessionIDFromContext(plainCtx))
	assert.Nil(t, HistoryFromContext(plainCtx))
	assert.Nil(t, StateFromContext(plainCtx))
	assert.Nil(t, ValueFromState(plainCtx, "key1"))
	assert.Nil(t, FromContext(plainCtx))
}

func TestContextWithTimeout(t *testing.T) {
	// Test that session context works with context.WithTimeout
	baseCtx := context.Background()

	// We need to create a timeout context that implements the Context interface
	// Using WithTimeout directly on a session context won't work because it returns
	// a standard context.Context, not a session Context
	timeoutCtx, cancel := context.WithTimeout(baseCtx, 10*time.Millisecond)
	defer cancel()

	// Create a new session context with the timeout context as parent
	sessTimeoutCtx := NewContext(timeoutCtx, "timeout-test", nil)

	// Verify we can still get session info
	assert.Equal(t, "timeout-test", SessionIDFromContext(sessTimeoutCtx))

	// Wait for timeout
	<-timeoutCtx.Done()
	assert.ErrorIs(t, timeoutCtx.Err(), context.DeadlineExceeded)
}
