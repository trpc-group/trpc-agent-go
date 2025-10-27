//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package callback

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessage_IntegrationScenario tests a realistic callback scenario.
// This simulates how callbacks would use the message to share data.
func TestMessage_IntegrationScenario(t *testing.T) {
	// Simulate a before callback.
	beforeCallback := func(ctx context.Context, msg Message) {
		msg.Set("start_time", time.Now())
		msg.Set("operation", "test_operation")
		msg.Set("user_id", "user123")
	}

	// Simulate an after callback.
	afterCallback := func(ctx context.Context, msg Message) time.Duration {
		startTimeVal, ok := msg.Get("start_time")
		require.True(t, ok)
		startTime, ok := startTimeVal.(time.Time)
		require.True(t, ok)

		duration := time.Since(startTime)

		// Verify metadata.
		op, ok := msg.Get("operation")
		assert.True(t, ok)
		assert.Equal(t, "test_operation", op)

		uid, ok := msg.Get("user_id")
		assert.True(t, ok)
		assert.Equal(t, "user123", uid)

		return duration
	}

	// Execute the scenario.
	ctx := context.Background()
	msg := NewMessage()

	beforeCallback(ctx, msg)
	time.Sleep(10 * time.Millisecond)
	duration := afterCallback(ctx, msg)

	assert.GreaterOrEqual(t, duration, 10*time.Millisecond)
	assert.Less(t, duration, 100*time.Millisecond)
}

// TestMessage_MultipleMessages tests using multiple separate messages.
// Note: The message implementation is NOT thread-safe by design.
// This test verifies that separate message instances work correctly.
func TestMessage_MultipleMessages(t *testing.T) {
	messages := make([]Message, 10)
	for i := 0; i < 10; i++ {
		messages[i] = NewMessage()
	}

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			msg := messages[id]
			msg.Set("goroutine_id", id)
			msg.Set("timestamp", time.Now())
			time.Sleep(1 * time.Millisecond)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify each message has its own data.
	for i := 0; i < 10; i++ {
		val, ok := messages[i].Get("goroutine_id")
		require.True(t, ok, "message %d should have goroutine_id", i)
		id, ok := val.(int)
		require.True(t, ok)
		assert.Equal(t, i, id)
	}
}

// TestMessage_ClearAndReuse tests clearing and reusing the message.
func TestMessage_ClearAndReuse(t *testing.T) {
	msg := NewMessage()

	// First use.
	msg.Set("key1", "value1")
	msg.Set("key2", 42)

	_, ok := msg.Get("key1")
	assert.True(t, ok)
	_, ok = msg.Get("key2")
	assert.True(t, ok)

	// Clear.
	msg.Clear()

	_, ok = msg.Get("key1")
	assert.False(t, ok)
	_, ok = msg.Get("key2")
	assert.False(t, ok)

	// Reuse with new values.
	msg.Set("key3", "value3")
	msg.Set("key4", 84)

	val, ok := msg.Get("key3")
	assert.True(t, ok)
	assert.Equal(t, "value3", val)

	val, ok = msg.Get("key4")
	assert.True(t, ok)
	assert.Equal(t, 84, val)
}
