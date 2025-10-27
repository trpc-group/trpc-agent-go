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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessage_SetAndGet(t *testing.T) {
	msg := NewMessage()

	// Test setting and getting string.
	msg.Set("key1", "value1")
	val, ok := msg.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	// Test setting and getting int.
	msg.Set("key2", 42)
	val, ok = msg.Get("key2")
	assert.True(t, ok)
	assert.Equal(t, 42, val)

	// Test setting and getting time.Time.
	now := time.Now()
	msg.Set("key3", now)
	val, ok = msg.Get("key3")
	assert.True(t, ok)
	assert.Equal(t, now, val)

	// Test getting non-existent key.
	val, ok = msg.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, val)
}

func TestMessage_Delete(t *testing.T) {
	msg := NewMessage()

	// Set a value.
	msg.Set("key1", "value1")
	_, ok := msg.Get("key1")
	assert.True(t, ok)

	// Delete the value.
	msg.Delete("key1")
	_, ok = msg.Get("key1")
	assert.False(t, ok)

	// Delete non-existent key should not panic.
	assert.NotPanics(t, func() {
		msg.Delete("nonexistent")
	})
}

func TestMessage_Clear(t *testing.T) {
	msg := NewMessage()

	// Set multiple values.
	msg.Set("key1", "value1")
	msg.Set("key2", 42)
	msg.Set("key3", time.Now())

	// Verify values exist.
	_, ok := msg.Get("key1")
	assert.True(t, ok)
	_, ok = msg.Get("key2")
	assert.True(t, ok)
	_, ok = msg.Get("key3")
	assert.True(t, ok)

	// Clear all values.
	msg.Clear()

	// Verify all values are cleared.
	_, ok = msg.Get("key1")
	assert.False(t, ok)
	_, ok = msg.Get("key2")
	assert.False(t, ok)
	_, ok = msg.Get("key3")
	assert.False(t, ok)
}

func TestMessage_Overwrite(t *testing.T) {
	msg := NewMessage()

	// Set initial value.
	msg.Set("key1", "value1")
	val, _ := msg.Get("key1")
	assert.Equal(t, "value1", val)

	// Overwrite with new value.
	msg.Set("key1", "value2")
	val, _ = msg.Get("key1")
	assert.Equal(t, "value2", val)

	// Overwrite with different type.
	msg.Set("key1", 123)
	val, _ = msg.Get("key1")
	assert.Equal(t, 123, val)
}

func TestMessage_TypeAssertion(t *testing.T) {
	msg := NewMessage()

	// Store different types.
	msg.Set("string", "hello")
	msg.Set("int", 42)
	msg.Set("float", 3.14)
	msg.Set("bool", true)
	now := time.Now()
	msg.Set("time", now)

	// Test type assertions.
	val, ok := msg.Get("string")
	require.True(t, ok)
	assert.Equal(t, "hello", val.(string))

	val, ok = msg.Get("int")
	require.True(t, ok)
	assert.Equal(t, 42, val.(int))

	val, ok = msg.Get("float")
	require.True(t, ok)
	assert.Equal(t, 3.14, val.(float64))

	val, ok = msg.Get("bool")
	require.True(t, ok)
	assert.True(t, val.(bool))

	val, ok = msg.Get("time")
	require.True(t, ok)
	assert.IsType(t, time.Time{}, val)
	assert.Equal(t, now, val.(time.Time))
}

func TestMessage_NilValue(t *testing.T) {
	msg := NewMessage()

	// Set nil value.
	msg.Set("key1", nil)
	val, ok := msg.Get("key1")
	assert.True(t, ok)
	assert.Nil(t, val)
}

func TestMessage_EmptyKey(t *testing.T) {
	msg := NewMessage()

	// Set value with empty key.
	msg.Set("", "value")
	val, ok := msg.Get("")
	assert.True(t, ok)
	assert.Equal(t, "value", val)
}
