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

func TestInvocation_State_Basic(t *testing.T) {
	inv := NewInvocation()

	// Test Set and Get.
	inv.SetState("callback:agent:key1", "value1")
	v1, ok1 := inv.GetState("callback:agent:key1")
	assert.True(t, ok1)
	assert.Equal(t, "value1", v1)

	// Test Get non-existent key.
	_, ok := inv.GetState("nonexistent")
	assert.False(t, ok)
}

func TestInvocation_State_DifferentTypes(t *testing.T) {
	inv := NewInvocation()

	// Test different value types.
	inv.SetState("agent:string", "hello")
	inv.SetState("agent:int", 42)
	inv.SetState("agent:float", 3.14)
	inv.SetState("agent:bool", true)
	inv.SetState("agent:time", time.Now())

	// Verify all values.
	v1, ok1 := inv.GetState("agent:string")
	assert.True(t, ok1)
	assert.Equal(t, "hello", v1)

	v2, ok2 := inv.GetState("agent:int")
	assert.True(t, ok2)
	assert.Equal(t, 42, v2)

	v3, ok3 := inv.GetState("agent:float")
	assert.True(t, ok3)
	assert.Equal(t, 3.14, v3)

	v4, ok4 := inv.GetState("agent:bool")
	assert.True(t, ok4)
	assert.Equal(t, true, v4)

	v5, ok5 := inv.GetState("agent:time")
	assert.True(t, ok5)
	assert.IsType(t, time.Time{}, v5)
}

func TestInvocation_State_PrefixIsolation(t *testing.T) {
	inv := NewInvocation()

	// Set values with different prefixes.
	inv.SetState("agent:key", "agent_value")
	inv.SetState("model:key", "model_value")
	inv.SetState("tool:calculator:key", "tool_value")

	// Verify isolation - different keys don't conflict.
	v1, ok1 := inv.GetState("agent:key")
	assert.True(t, ok1)
	assert.Equal(t, "agent_value", v1)

	v2, ok2 := inv.GetState("model:key")
	assert.True(t, ok2)
	assert.Equal(t, "model_value", v2)

	v3, ok3 := inv.GetState("tool:calculator:key")
	assert.True(t, ok3)
	assert.Equal(t, "tool_value", v3)

	// Verify that "key" alone doesn't exist.
	_, ok := inv.GetState("key")
	assert.False(t, ok)
}

func TestInvocation_State_Delete(t *testing.T) {
	inv := NewInvocation()

	// Set and verify.
	inv.SetState("agent:key", "value")
	v, ok := inv.GetState("agent:key")
	assert.True(t, ok)
	assert.Equal(t, "value", v)

	// Delete and verify.
	inv.DeleteState("agent:key")
	_, ok = inv.GetState("agent:key")
	assert.False(t, ok)

	// Delete non-existent key should not panic.
	inv.DeleteState("nonexistent")
}

func TestInvocation_State_Overwrite(t *testing.T) {
	inv := NewInvocation()

	// Set initial value.
	inv.SetState("agent:key", "value1")
	v1, ok1 := inv.GetState("agent:key")
	assert.True(t, ok1)
	assert.Equal(t, "value1", v1)

	// Overwrite with new value.
	inv.SetState("agent:key", "value2")
	v2, ok2 := inv.GetState("agent:key")
	assert.True(t, ok2)
	assert.Equal(t, "value2", v2)
}

func TestInvocation_State_LazyInit(t *testing.T) {
	inv := NewInvocation()

	// Verify lazy initialization - map is nil before first use.
	assert.Nil(t, inv.state)

	// First Set should initialize the map.
	inv.SetState("key", "value")
	assert.NotNil(t, inv.state)

	// Get on uninitialized invocation should not panic.
	inv2 := NewInvocation()
	_, ok := inv2.GetState("key")
	assert.False(t, ok)

	// Delete on uninitialized invocation should not panic.
	inv3 := NewInvocation()
	inv3.DeleteState("key")
}

func TestInvocation_State_NilInvocation(t *testing.T) {
	var inv *Invocation

	// All operations on nil invocation should not panic.
	inv.SetState("key", "value")
	_, ok := inv.GetState("key")
	assert.False(t, ok)
	inv.DeleteState("key")
}

func TestInvocation_State_Concurrent(t *testing.T) {
	inv := NewInvocation()
	var wg sync.WaitGroup

	// Concurrent writes to different keys.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			key := fmt.Sprintf("tool:tool%d:start_time", index)
			inv.SetState(key, time.Now())
			time.Sleep(10 * time.Millisecond)
			_, ok := inv.GetState(key)
			assert.True(t, ok)
		}(i)
	}

	wg.Wait()

	// Verify all keys exist.
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("tool:tool%d:start_time", i)
		_, ok := inv.GetState(key)
		assert.True(t, ok)
	}
}

func TestInvocation_State_ConcurrentReadWrite(t *testing.T) {
	inv := NewInvocation()
	var wg sync.WaitGroup

	// Initialize some keys.
	for i := 0; i < 5; i++ {
		inv.SetState(fmt.Sprintf("key%d", i), i)
	}

	// Concurrent reads and writes.
	for i := 0; i < 10; i++ {
		wg.Add(2)

		// Reader.
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%d", index%5)
				inv.GetState(key)
			}
		}(i)

		// Writer.
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%d", index%5)
				inv.SetState(key, j)
			}
		}(i)
	}

	wg.Wait()
}

func TestInvocation_State_CrossCallbackSharing(t *testing.T) {
	inv := NewInvocation()

	// Test cross-callback type data sharing.
	inv.SetState("shared:request_id", "req-123")

	// Agent callback can access.
	v1, ok1 := inv.GetState("shared:request_id")
	assert.True(t, ok1)
	assert.Equal(t, "req-123", v1)

	// Model callback can also access.
	v2, ok2 := inv.GetState("shared:request_id")
	assert.True(t, ok2)
	assert.Equal(t, "req-123", v2)

	// Tool callback can also access.
	v3, ok3 := inv.GetState("shared:request_id")
	assert.True(t, ok3)
	assert.Equal(t, "req-123", v3)
}

func TestInvocation_State_TimerUseCase(t *testing.T) {
	inv := NewInvocation()

	// Simulate agent callback timing.
	startTime := time.Now()
	inv.SetState("agent:start_time", startTime)

	// Simulate some work.
	time.Sleep(50 * time.Millisecond)

	// Retrieve and calculate duration.
	if st, ok := inv.GetState("agent:start_time"); ok {
		duration := time.Since(st.(time.Time))
		assert.True(t, duration >= 50*time.Millisecond)
	} else {
		t.Fatal("start_time not found")
	}

	// Clean up.
	inv.DeleteState("agent:start_time")
	_, ok := inv.GetState("agent:start_time")
	assert.False(t, ok)
}

func TestInvocation_State_ToolIsolation(t *testing.T) {
	inv := NewInvocation()

	// Simulate multiple tools executing concurrently.
	tools := []string{"calculator", "search", "weather"}

	for _, toolName := range tools {
		key := "tool:" + toolName + ":start_time"
		inv.SetState(key, time.Now())
	}

	// Verify each tool has its own state.
	for _, toolName := range tools {
		key := "tool:" + toolName + ":start_time"
		_, ok := inv.GetState(key)
		assert.True(t, ok, "Tool %s should have its state", toolName)
	}

	// Verify tool states don't interfere with each other.
	_, ok := inv.GetState("tool:calculator:start_time")
	assert.True(t, ok)

	_, ok = inv.GetState("tool:search:start_time")
	assert.True(t, ok)

	// Different tool's key should not exist.
	_, ok = inv.GetState("tool:calculator:search")
	assert.False(t, ok)
}

func TestInvocation_State_ComplexStruct(t *testing.T) {
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

	inv.SetState("agent:custom_data", data)

	// Retrieve and verify.
	v, ok := inv.GetState("agent:custom_data")
	require.True(t, ok)

	retrieved, ok := v.(CustomData)
	require.True(t, ok)
	assert.Equal(t, data.ID, retrieved.ID)
	assert.Equal(t, data.Metadata, retrieved.Metadata)
}

func TestGetStateValue(t *testing.T) {
	t.Run("key not found", func(t *testing.T) {
		inv := NewInvocation()
		val, ok := GetStateValue[string](inv, "nonexistent")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("matching type", func(t *testing.T) {
		inv := NewInvocation()
		inv.SetState("agent:string", "hello")
		inv.SetState("agent:int", 42)
		inv.SetState("agent:float", 3.14)
		inv.SetState("agent:bool", true)
		inv.SetState("agent:time", time.Now())

		// Test string.
		strVal, ok := GetStateValue[string](inv, "agent:string")
		assert.True(t, ok)
		assert.Equal(t, "hello", strVal)

		// Test int.
		intVal, ok := GetStateValue[int](inv, "agent:int")
		assert.True(t, ok)
		assert.Equal(t, 42, intVal)

		// Test float64.
		floatVal, ok := GetStateValue[float64](inv, "agent:float")
		assert.True(t, ok)
		assert.Equal(t, 3.14, floatVal)

		// Test bool.
		boolVal, ok := GetStateValue[bool](inv, "agent:bool")
		assert.True(t, ok)
		assert.Equal(t, true, boolVal)

		// Test time.Time.
		timeVal, ok := GetStateValue[time.Time](inv, "agent:time")
		assert.True(t, ok)
		assert.IsType(t, time.Time{}, timeVal)
	})

	t.Run("type mismatch", func(t *testing.T) {
		inv := NewInvocation()
		inv.SetState("agent:value", "hello")

		// Try to get as int when it's actually string.
		intVal, ok := GetStateValue[int](inv, "agent:value")
		assert.False(t, ok)
		assert.Equal(t, 0, intVal)

		// Try to get as string when it's actually int.
		inv.SetState("agent:number", 42)
		strVal, ok := GetStateValue[string](inv, "agent:number")
		assert.False(t, ok)
		assert.Equal(t, "", strVal)
	})

	t.Run("nil invocation", func(t *testing.T) {
		var inv *Invocation
		val, ok := GetStateValue[string](inv, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("complex struct type", func(t *testing.T) {
		type CustomData struct {
			ID        string
			Timestamp time.Time
			Metadata  map[string]string
		}

		inv := NewInvocation()
		data := CustomData{
			ID:        "test-123",
			Timestamp: time.Now(),
			Metadata: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		}
		inv.SetState("agent:custom_data", data)

		retrieved, ok := GetStateValue[CustomData](inv, "agent:custom_data")
		require.True(t, ok)
		assert.Equal(t, data.ID, retrieved.ID)
		assert.Equal(t, data.Metadata, retrieved.Metadata)
	})

	t.Run("pointer type", func(t *testing.T) {
		inv := NewInvocation()
		str := "hello"
		inv.SetState("agent:ptr", &str)

		ptrVal, ok := GetStateValue[*string](inv, "agent:ptr")
		assert.True(t, ok)
		require.NotNil(t, ptrVal)
		assert.Equal(t, "hello", *ptrVal)
	})

	t.Run("slice type", func(t *testing.T) {
		inv := NewInvocation()
		slice := []int{1, 2, 3}
		inv.SetState("agent:slice", slice)

		sliceVal, ok := GetStateValue[[]int](inv, "agent:slice")
		assert.True(t, ok)
		assert.Equal(t, []int{1, 2, 3}, sliceVal)
	})

	t.Run("map type", func(t *testing.T) {
		inv := NewInvocation()
		m := map[string]int{"a": 1, "b": 2}
		inv.SetState("agent:map", m)

		mapVal, ok := GetStateValue[map[string]int](inv, "agent:map")
		assert.True(t, ok)
		assert.Equal(t, map[string]int{"a": 1, "b": 2}, mapVal)
	})
}
