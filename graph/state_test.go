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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestGetStateValue(t *testing.T) {
	t.Run("key not found", func(t *testing.T) {
		state := State{}
		val, ok := GetStateValue[string](state, "nonexistent")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("nil state", func(t *testing.T) {
		var state State
		val, ok := GetStateValue[string](state, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("matching type", func(t *testing.T) {
		state := State{
			"string_key": "hello",
			"int_key":    42,
			"float_key":  3.14,
			"bool_key":   true,
			"time_key":   time.Now(),
		}

		// Test string.
		strVal, ok := GetStateValue[string](state, "string_key")
		assert.True(t, ok)
		assert.Equal(t, "hello", strVal)

		// Test int.
		intVal, ok := GetStateValue[int](state, "int_key")
		assert.True(t, ok)
		assert.Equal(t, 42, intVal)

		// Test float64.
		floatVal, ok := GetStateValue[float64](state, "float_key")
		assert.True(t, ok)
		assert.Equal(t, 3.14, floatVal)

		// Test bool.
		boolVal, ok := GetStateValue[bool](state, "bool_key")
		assert.True(t, ok)
		assert.Equal(t, true, boolVal)

		// Test time.Time.
		timeVal, ok := GetStateValue[time.Time](state, "time_key")
		assert.True(t, ok)
		assert.IsType(t, time.Time{}, timeVal)
	})

	t.Run("type mismatch", func(t *testing.T) {
		state := State{
			"value": "hello",
		}

		// Try to get as int when it's actually string.
		intVal, ok := GetStateValue[int](state, "value")
		assert.False(t, ok)
		assert.Equal(t, 0, intVal)

		// Try to get as string when it's actually int.
		state["number"] = 42
		strVal, ok := GetStateValue[string](state, "number")
		assert.False(t, ok)
		assert.Equal(t, "", strVal)
	})

	t.Run("slice type", func(t *testing.T) {
		state := State{
			"messages": []model.Message{
				{Role: model.RoleUser, Content: "hello"},
				{Role: model.RoleAssistant, Content: "hi"},
			},
		}

		messages, ok := GetStateValue[[]model.Message](state, "messages")
		assert.True(t, ok)
		require.Len(t, messages, 2)
		assert.Equal(t, model.RoleUser, messages[0].Role)
		assert.Equal(t, "hello", messages[0].Content)
		assert.Equal(t, model.RoleAssistant, messages[1].Role)
		assert.Equal(t, "hi", messages[1].Content)
	})

	t.Run("map type", func(t *testing.T) {
		state := State{
			"node_responses": map[string]any{
				"node1": "response1",
				"node2": "response2",
			},
		}

		nodeResponses, ok := GetStateValue[map[string]any](state, "node_responses")
		assert.True(t, ok)
		assert.Equal(t, "response1", nodeResponses["node1"])
		assert.Equal(t, "response2", nodeResponses["node2"])
	})

	t.Run("pointer type", func(t *testing.T) {
		str := "hello"
		state := State{
			"ptr": &str,
		}

		ptrVal, ok := GetStateValue[*string](state, "ptr")
		assert.True(t, ok)
		require.NotNil(t, ptrVal)
		assert.Equal(t, "hello", *ptrVal)
	})

	t.Run("complex struct type", func(t *testing.T) {
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
		state := State{
			"custom_data": data,
		}

		retrieved, ok := GetStateValue[CustomData](state, "custom_data")
		require.True(t, ok)
		assert.Equal(t, data.ID, retrieved.ID)
		assert.Equal(t, data.Metadata, retrieved.Metadata)
	})

	t.Run("built-in state keys", func(t *testing.T) {
		state := State{
			StateKeyUserInput:     "user input",
			StateKeyLastResponse:  "last response",
			StateKeyCurrentNodeID: "node-123",
			StateKeyMessages:      []model.Message{{Role: model.RoleUser, Content: "test"}},
			StateKeyNodeResponses: map[string]any{"key": "value"},
		}

		// Test StateKeyUserInput.
		userInput, ok := GetStateValue[string](state, StateKeyUserInput)
		assert.True(t, ok)
		assert.Equal(t, "user input", userInput)

		// Test StateKeyLastResponse.
		lastResponse, ok := GetStateValue[string](state, StateKeyLastResponse)
		assert.True(t, ok)
		assert.Equal(t, "last response", lastResponse)

		// Test StateKeyCurrentNodeID.
		nodeID, ok := GetStateValue[string](state, StateKeyCurrentNodeID)
		assert.True(t, ok)
		assert.Equal(t, "node-123", nodeID)

		// Test StateKeyMessages.
		messages, ok := GetStateValue[[]model.Message](state, StateKeyMessages)
		assert.True(t, ok)
		require.Len(t, messages, 1)
		assert.Equal(t, model.RoleUser, messages[0].Role)

		// Test StateKeyNodeResponses.
		nodeResponses, ok := GetStateValue[map[string]any](state, StateKeyNodeResponses)
		assert.True(t, ok)
		assert.Equal(t, "value", nodeResponses["key"])
	})
}
