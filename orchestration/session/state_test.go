package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewState(t *testing.T) {
	state := NewState()

	assert.NotNil(t, state)
	assert.NotNil(t, state.Value)
	assert.NotNil(t, state.Delta)
	assert.Equal(t, 0, len(state.Value))
	assert.Equal(t, 0, len(state.Delta))
}

func TestState_Set(t *testing.T) {
	state := NewState()

	// Test setting a new key
	state.Set("key1", []byte("value1"))
	assert.Equal(t, []byte("value1"), state.Value["key1"])
	assert.Equal(t, []byte("value1"), state.Delta["key1"])

	// Test updating an existing key
	state.Set("key1", []byte("updated_value"))
	assert.Equal(t, []byte("updated_value"), state.Value["key1"])
	assert.Equal(t, []byte("updated_value"), state.Delta["key1"])

	// Test setting multiple keys
	state.Set("key2", []byte("value2"))
	state.Set("key3", []byte("value3"))

	assert.Equal(t, []byte("value2"), state.Value["key2"])
	assert.Equal(t, []byte("value2"), state.Delta["key2"])
	assert.Equal(t, []byte("value3"), state.Value["key3"])
	assert.Equal(t, []byte("value3"), state.Delta["key3"])

	// Test setting empty value
	state.Set("empty_key", []byte{})
	assert.Equal(t, []byte{}, state.Value["empty_key"])
	assert.Equal(t, []byte{}, state.Delta["empty_key"])
}

func TestState_Get(t *testing.T) {
	state := NewState()

	// Test getting non-existent key
	value, exists := state.Get("non_existent")
	assert.False(t, exists)
	assert.Nil(t, value)

	// Test getting key that exists in Delta but not in Value
	state.Delta["delta_only"] = []byte("delta_value")
	value, exists = state.Get("delta_only")
	assert.True(t, exists)
	assert.Equal(t, []byte("delta_value"), value)

	// Test getting key that exists in Value but not in Delta
	state.Value["value_only"] = []byte("value_only_data")
	value, exists = state.Get("value_only")
	assert.True(t, exists)
	assert.Equal(t, []byte("value_only_data"), value)

	// Test getting key that exists in both Delta and Value (Delta should take precedence)
	state.Value["both"] = []byte("value_data")
	state.Delta["both"] = []byte("delta_data")
	value, exists = state.Get("both")
	assert.True(t, exists)
	assert.Equal(t, []byte("delta_data"), value) // Delta should take precedence

	// Test getting empty value
	state.Set("empty_key", []byte{})
	value, exists = state.Get("empty_key")
	assert.True(t, exists)
	assert.Equal(t, []byte{}, value)
}

func TestState_Integration(t *testing.T) {
	state := NewState()

	// Test the complete workflow: Set -> Get
	state.Set("integration_key", []byte("integration_value"))
	value, exists := state.Get("integration_key")
	assert.True(t, exists)
	assert.Equal(t, []byte("integration_value"), value)

	// Test updating and getting
	state.Set("integration_key", []byte("updated_integration_value"))
	value, exists = state.Get("integration_key")
	assert.True(t, exists)
	assert.Equal(t, []byte("updated_integration_value"), value)

	// Test multiple operations
	keys := []string{"key1", "key2", "key3"}
	values := [][]byte{[]byte("value1"), []byte("value2"), []byte("value3")}

	for i, key := range keys {
		state.Set(key, values[i])
	}

	for i, key := range keys {
		value, exists := state.Get(key)
		assert.True(t, exists)
		assert.Equal(t, values[i], value)
	}
}

func TestState_EmptyValues(t *testing.T) {
	state := NewState()

	// Test setting and getting empty byte slice
	state.Set("empty_bytes", []byte{})
	value, exists := state.Get("empty_bytes")
	assert.True(t, exists)
	assert.Equal(t, []byte{}, value)

	// Test setting and getting nil value
	state.Set("nil_value", nil)
	value, exists = state.Get("nil_value")
	assert.True(t, exists)
	assert.Nil(t, value)
}

func TestState_ConcurrentAccess(t *testing.T) {
	state := NewState()

	// Test that Value and Delta are separate maps
	state.Value["value_key"] = []byte("value_data")
	state.Delta["delta_key"] = []byte("delta_data")

	// Value should not contain delta_key
	_, exists := state.Value["delta_key"]
	assert.False(t, exists)

	// Delta should not contain value_key
	_, exists = state.Delta["value_key"]
	assert.False(t, exists)

	// But Get should find both
	value, exists := state.Get("value_key")
	assert.True(t, exists)
	assert.Equal(t, []byte("value_data"), value)

	value, exists = state.Get("delta_key")
	assert.True(t, exists)
	assert.Equal(t, []byte("delta_data"), value)
}

func TestStateConstants(t *testing.T) {
	// Test that constants are properly defined
	assert.Equal(t, "app:", StateAppPrefix)
	assert.Equal(t, "user:", StateUserPrefix)
	assert.Equal(t, "temp:", StateTempPrefix)
}
