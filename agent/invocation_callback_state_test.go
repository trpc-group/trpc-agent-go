//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvocation_CallbackState_Basic(t *testing.T) {
	inv := NewInvocation()

	// Test Set and Get.
	inv.SetCallbackState("agent:key1", "value1")
	v1, ok1 := inv.GetCallbackState("agent:key1")
	assert.True(t, ok1)
	assert.Equal(t, "value1", v1)

	// Test Get non-existent key.
	_, ok := inv.GetCallbackState("nonexistent")
	assert.False(t, ok)
}

func TestInvocation_CallbackState_DifferentTypes(t *testing.T) {
	inv := NewInvocation()

	// Test different value types.
	inv.SetCallbackState("agent:string", "hello")
	inv.SetCallbackState("agent:int", 42)
	inv.SetCallbackState("agent:float", 3.14)
	inv.SetCallbackState("agent:bool", true)
	inv.SetCallbackState("agent:time", time.Now())

	// Verify all values.
	v1, ok1 := inv.GetCallbackState("agent:string")
	assert.True(t, ok1)
	assert.Equal(t, "hello", v1)

	v2, ok2 := inv.GetCallbackState("agent:int")
	assert.True(t, ok2)
	assert.Equal(t, 42, v2)

	v3, ok3 := inv.GetCallbackState("agent:float")
	assert.True(t, ok3)
	assert.Equal(t, 3.14, v3)

	v4, ok4 := inv.GetCallbackState("agent:bool")
	assert.True(t, ok4)
	assert.Equal(t, true, v4)

	v5, ok5 := inv.GetCallbackState("agent:time")
	assert.True(t, ok5)
	assert.IsType(t, time.Time{}, v5)
}

func TestInvocation_CallbackState_PrefixIsolation(t *testing.T) {
	inv := NewInvocation()

	// Set values with different prefixes.
	inv.SetCallbackState("agent:key", "agent_value")
	inv.SetCallbackState("model:key", "model_value")
	inv.SetCallbackState("tool:calculator:key", "tool_value")

	// Verify isolation - different keys don't conflict.
	v1, ok1 := inv.GetCallbackState("agent:key")
	assert.True(t, ok1)
	assert.Equal(t, "agent_value", v1)

	v2, ok2 := inv.GetCallbackState("model:key")
	assert.True(t, ok2)
	assert.Equal(t, "model_value", v2)

	v3, ok3 := inv.GetCallbackState("tool:calculator:key")
	assert.True(t, ok3)
	assert.Equal(t, "tool_value", v3)

	// Verify that "key" alone doesn't exist.
	_, ok := inv.GetCallbackState("key")
	assert.False(t, ok)
}

func TestInvocation_CallbackState_Delete(t *testing.T) {
	inv := NewInvocation()

	// Set and verify.
	inv.SetCallbackState("agent:key", "value")
	v, ok := inv.GetCallbackState("agent:key")
	assert.True(t, ok)
	assert.Equal(t, "value", v)

	// Delete and verify.
	inv.DeleteCallbackState("agent:key")
	_, ok = inv.GetCallbackState("agent:key")
	assert.False(t, ok)

	// Delete non-existent key should not panic.
	inv.DeleteCallbackState("nonexistent")
}

func TestInvocation_CallbackState_Overwrite(t *testing.T) {
	inv := NewInvocation()

	// Set initial value.
	inv.SetCallbackState("agent:key", "value1")
	v1, ok1 := inv.GetCallbackState("agent:key")
	assert.True(t, ok1)
	assert.Equal(t, "value1", v1)

	// Overwrite with new value.
	inv.SetCallbackState("agent:key", "value2")
	v2, ok2 := inv.GetCallbackState("agent:key")
	assert.True(t, ok2)
	assert.Equal(t, "value2", v2)
}

func TestInvocation_CallbackState_LazyInit(t *testing.T) {
	inv := NewInvocation()

	// Verify lazy initialization - map is nil before first use.
	assert.Nil(t, inv.callbackState)

	// First Set should initialize the map.
	inv.SetCallbackState("key", "value")
	assert.NotNil(t, inv.callbackState)

	// Get on uninitialized invocation should not panic.
	inv2 := NewInvocation()
	_, ok := inv2.GetCallbackState("key")
	assert.False(t, ok)

	// Delete on uninitialized invocation should not panic.
	inv3 := NewInvocation()
	inv3.DeleteCallbackState("key")
}

func TestInvocation_CallbackState_NilInvocation(t *testing.T) {
	var inv *Invocation

	// All operations on nil invocation should not panic.
	inv.SetCallbackState("key", "value")
	_, ok := inv.GetCallbackState("key")
	assert.False(t, ok)
	inv.DeleteCallbackState("key")
}

func TestInvocation_CallbackState_Concurrent(t *testing.T) {
	inv := NewInvocation()
	var wg sync.WaitGroup

	// Concurrent writes to different keys.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			key := fmt.Sprintf("tool:tool%d:start_time", index)
			inv.SetCallbackState(key, time.Now())
			time.Sleep(10 * time.Millisecond)
			_, ok := inv.GetCallbackState(key)
			assert.True(t, ok)
		}(i)
	}

	wg.Wait()

	// Verify all keys exist.
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("tool:tool%d:start_time", i)
		_, ok := inv.GetCallbackState(key)
		assert.True(t, ok)
	}
}

func TestInvocation_CallbackState_ConcurrentReadWrite(t *testing.T) {
	inv := NewInvocation()
	var wg sync.WaitGroup

	// Initialize some keys.
	for i := 0; i < 5; i++ {
		inv.SetCallbackState(fmt.Sprintf("key%d", i), i)
	}

	// Concurrent reads and writes.
	for i := 0; i < 10; i++ {
		wg.Add(2)

		// Reader.
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%d", index%5)
				inv.GetCallbackState(key)
			}
		}(i)

		// Writer.
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%d", index%5)
				inv.SetCallbackState(key, j)
			}
		}(i)
	}

	wg.Wait()
}

func TestInvocation_CallbackState_CrossCallbackSharing(t *testing.T) {
	inv := NewInvocation()

	// Test cross-callback type data sharing.
	inv.SetCallbackState("shared:request_id", "req-123")

	// Agent callback can access.
	v1, ok1 := inv.GetCallbackState("shared:request_id")
	assert.True(t, ok1)
	assert.Equal(t, "req-123", v1)

	// Model callback can also access.
	v2, ok2 := inv.GetCallbackState("shared:request_id")
	assert.True(t, ok2)
	assert.Equal(t, "req-123", v2)

	// Tool callback can also access.
	v3, ok3 := inv.GetCallbackState("shared:request_id")
	assert.True(t, ok3)
	assert.Equal(t, "req-123", v3)
}

func TestInvocation_CallbackState_TimerUseCase(t *testing.T) {
	inv := NewInvocation()

	// Simulate agent callback timing.
	startTime := time.Now()
	inv.SetCallbackState("agent:start_time", startTime)

	// Simulate some work.
	time.Sleep(50 * time.Millisecond)

	// Retrieve and calculate duration.
	if st, ok := inv.GetCallbackState("agent:start_time"); ok {
		duration := time.Since(st.(time.Time))
		assert.True(t, duration >= 50*time.Millisecond)
	} else {
		t.Fatal("start_time not found")
	}

	// Clean up.
	inv.DeleteCallbackState("agent:start_time")
	_, ok := inv.GetCallbackState("agent:start_time")
	assert.False(t, ok)
}

func TestInvocation_CallbackState_ToolIsolation(t *testing.T) {
	inv := NewInvocation()

	// Simulate multiple tools executing concurrently.
	tools := []string{"calculator", "search", "weather"}

	for _, toolName := range tools {
		key := "tool:" + toolName + ":start_time"
		inv.SetCallbackState(key, time.Now())
	}

	// Verify each tool has its own state.
	for _, toolName := range tools {
		key := "tool:" + toolName + ":start_time"
		_, ok := inv.GetCallbackState(key)
		assert.True(t, ok, "Tool %s should have its state", toolName)
	}

	// Verify tool states don't interfere with each other.
	_, ok := inv.GetCallbackState("tool:calculator:start_time")
	assert.True(t, ok)

	_, ok = inv.GetCallbackState("tool:search:start_time")
	assert.True(t, ok)

	// Different tool's key should not exist.
	_, ok = inv.GetCallbackState("tool:calculator:search")
	assert.False(t, ok)
}

func TestInvocation_CallbackState_ComplexStruct(t *testing.T) {
	inv := NewInvocation()

	// Test storing complex structures.
	type CustomData struct {
		ID        string
		Timestamp time.Time
		Metadata  map[string]string
	}

	data := CustomData{
		ID:        "test-123",
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	inv.SetCallbackState("agent:custom_data", data)

	// Retrieve and verify.
	v, ok := inv.GetCallbackState("agent:custom_data")
	require.True(t, ok)

	retrieved, ok := v.(CustomData)
	require.True(t, ok)
	assert.Equal(t, data.ID, retrieved.ID)
	assert.Equal(t, data.Metadata, retrieved.Metadata)
}
