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
	"encoding/json"
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
			StateKeyUserInput:        "user input",
			StateKeyLastResponse:     "last response",
			StateKeyLastToolResponse: "last tool response",
			StateKeyCurrentNodeID:    "node-123",
			StateKeyMessages: []model.Message{
				{Role: model.RoleUser, Content: "test"},
			},
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

		// Test StateKeyLastToolResponse.
		lastToolResponse, ok := GetStateValue[string](
			state,
			StateKeyLastToolResponse,
		)
		assert.True(t, ok)
		assert.Equal(t, "last tool response", lastToolResponse)

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

func TestOneShotMessagesByNodeHelpers(t *testing.T) {
	nodeID := "llm1"
	msgs := []model.Message{
		model.NewUserMessage("hi"),
	}

	update := SetOneShotMessagesForNode(nodeID, msgs)
	raw, ok := update[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	require.Len(t, raw, 1)
	require.Equal(t, "hi", raw[nodeID][0].Content)

	msgs[0].Content = "changed"
	require.Equal(t, "hi", raw[nodeID][0].Content)

	clear := ClearOneShotMessagesForNode(nodeID)
	clearRaw, ok := clear[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	_, exists := clearRaw[nodeID]
	require.True(t, exists)
	require.Len(t, clearRaw[nodeID], 0)

	state := State{
		StateKeyOneShotMessagesByNode: raw,
	}
	got, ok := GetOneShotMessagesForNode(state, nodeID)
	require.True(t, ok)
	require.Equal(t, "hi", got[0].Content)
	got[0].Content = "mutated"
	require.Equal(t, "hi", raw[nodeID][0].Content)
}

func TestStateSchemaApplyUpdate_OneShotMessagesByNode(t *testing.T) {
	schema := NewStateSchema()

	a := State{
		StateKeyOneShotMessagesByNode: map[string][]model.Message{
			"llm1": {model.NewUserMessage("a")},
		},
	}
	b := State{
		StateKeyOneShotMessagesByNode: map[string][]model.Message{
			"llm2": {model.NewUserMessage("b")},
		},
	}

	state := schema.ApplyUpdate(State{}, a)
	state = schema.ApplyUpdate(state, b)

	merged, ok := state[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	require.Len(t, merged, 2)
	require.Equal(t, "a", merged["llm1"][0].Content)
	require.Equal(t, "b", merged["llm2"][0].Content)

	del := State{
		StateKeyOneShotMessagesByNode: map[string][]model.Message{
			"llm1": nil,
		},
	}
	state = schema.ApplyUpdate(state, del)
	merged, ok = state[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	_, exists := merged["llm1"]
	require.False(t, exists)
	require.Equal(t, "b", merged["llm2"][0].Content)

	state = schema.ApplyUpdate(state, State{StateKeyOneShotMessagesByNode: nil})
	cleared, ok := state[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	require.Nil(t, cleared)

	raw, err := json.Marshal(b[StateKeyOneShotMessagesByNode])
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	state = schema.ApplyUpdate(State{}, State{
		StateKeyOneShotMessagesByNode: decoded,
	})
	merged, ok = state[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	require.Equal(t, "b", merged["llm2"][0].Content)
}
