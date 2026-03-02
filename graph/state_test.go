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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
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
		nodeResponses, ok := GetStateValue[map[string]any](
			state,
			StateKeyNodeResponses,
		)
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
	clearAny := clear[StateKeyOneShotMessagesByNode]
	clearRaw, ok := clearAny.(map[string][]model.Message)
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

	byNode := map[string][]model.Message{
		"llm1": {model.NewUserMessage("a")},
		"llm2": {model.NewUserMessage("b")},
	}
	update = SetOneShotMessagesByNode(byNode)
	raw, ok = update[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	require.Len(t, raw, 2)
	require.Equal(t, "a", raw["llm1"][0].Content)
	require.Equal(t, "b", raw["llm2"][0].Content)

	byNode["llm1"][0].Content = "changed"
	require.Equal(t, "a", raw["llm1"][0].Content)

	clear = ClearOneShotMessagesByNode()
	_, exists = clear[StateKeyOneShotMessagesByNode]
	require.True(t, exists)
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

	mergedAny := state[StateKeyOneShotMessagesByNode]
	merged, ok := mergedAny.(map[string][]model.Message)
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
	mergedAny = state[StateKeyOneShotMessagesByNode]
	merged, ok = mergedAny.(map[string][]model.Message)
	require.True(t, ok)
	_, exists := merged["llm1"]
	require.False(t, exists)
	require.Equal(t, "b", merged["llm2"][0].Content)

	state = schema.ApplyUpdate(state, State{StateKeyOneShotMessagesByNode: nil})
	clearedAny := state[StateKeyOneShotMessagesByNode]
	cleared, ok := clearedAny.(map[string][]model.Message)
	require.True(t, ok)
	require.Nil(t, cleared)

	raw, err := json.Marshal(b[StateKeyOneShotMessagesByNode])
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	state = schema.ApplyUpdate(State{}, State{
		StateKeyOneShotMessagesByNode: decoded,
	})
	mergedAny = state[StateKeyOneShotMessagesByNode]
	merged, ok = mergedAny.(map[string][]model.Message)
	require.True(t, ok)
	require.Equal(t, "b", merged["llm2"][0].Content)
}

func TestOneShotMessagesByNodeCoveragePaths(t *testing.T) {
	t.Run("SetOneShotMessagesForNode handles empty", func(t *testing.T) {
		update := SetOneShotMessagesForNode("llm1", nil)
		rawAny := update[StateKeyOneShotMessagesByNode]
		raw, ok := rawAny.(map[string][]model.Message)
		require.True(t, ok)
		require.Contains(t, raw, "llm1")

		empty := SetOneShotMessagesForNode("", []model.Message{
			model.NewUserMessage("hi"),
		})
		require.Nil(t, empty)
	})

	t.Run("SetOneShotMessagesByNode handles empty", func(t *testing.T) {
		require.Nil(t, SetOneShotMessagesByNode(nil))
		require.Nil(t, SetOneShotMessagesByNode(map[string][]model.Message{}))
		onlyEmptyID := SetOneShotMessagesByNode(map[string][]model.Message{
			"": {model.NewUserMessage("hi")},
		})
		require.Nil(t, onlyEmptyID)

		clearOne := SetOneShotMessagesByNode(map[string][]model.Message{
			"llm1": nil,
		})
		rawAny := clearOne[StateKeyOneShotMessagesByNode]
		raw, ok := rawAny.(map[string][]model.Message)
		require.True(t, ok)
		require.Contains(t, raw, "llm1")
		require.Len(t, raw["llm1"], 0)
	})

	t.Run("ClearOneShotMessagesForNode handles empty ID", func(t *testing.T) {
		require.Nil(t, ClearOneShotMessagesForNode(""))
	})

	t.Run("GetOneShotMessagesForNode reads map[string]any", func(t *testing.T) {
		msgs := map[string][]model.Message{
			"llm1": {model.NewUserMessage("hi")},
		}
		raw, err := json.Marshal(msgs)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))

		state := State{StateKeyOneShotMessagesByNode: decoded}
		got, ok := GetOneShotMessagesForNode(state, "llm1")
		require.True(t, ok)
		require.Equal(t, "hi", got[0].Content)
	})

	t.Run("GetOneShotMessagesForNode uses default decode", func(t *testing.T) {
		type byNode struct {
			LLM1 []model.Message `json:"llm1"`
		}
		state := State{
			StateKeyOneShotMessagesByNode: byNode{
				LLM1: []model.Message{model.NewUserMessage("hi")},
			},
		}
		got, ok := GetOneShotMessagesForNode(state, "llm1")
		require.True(t, ok)
		require.Equal(t, "hi", got[0].Content)
	})

	t.Run("GetOneShotMessagesForNode decode failure", func(t *testing.T) {
		state := State{
			StateKeyOneShotMessagesByNode: make(chan int),
		}
		_, ok := GetOneShotMessagesForNode(state, "llm1")
		require.False(t, ok)
	})

	t.Run("Reducer handles default update types", func(t *testing.T) {
		type byNode struct {
			LLM1 []model.Message `json:"llm1"`
		}
		out := OneShotMessagesByNodeReducer(nil, byNode{
			LLM1: []model.Message{model.NewUserMessage("hi")},
		})
		merged, ok := out.(map[string][]model.Message)
		require.True(t, ok)
		require.Equal(t, "hi", merged["llm1"][0].Content)

		ch := make(chan int)
		out = OneShotMessagesByNodeReducer(nil, ch)
		typed, ok := out.(chan int)
		require.True(t, ok)
		require.Nil(t, typed)
	})

	t.Run("Reducer deletes on decode error", func(t *testing.T) {
		state := map[string][]model.Message{
			"llm1": {model.NewUserMessage("keep")},
		}
		update := map[string]any{
			"llm1": make(chan int),
		}
		out := OneShotMessagesByNodeReducer(state, update)
		merged, ok := out.(map[string][]model.Message)
		require.True(t, ok)
		_, exists := merged["llm1"]
		require.False(t, exists)
	})

	t.Run("Existing decode supports any/default", func(t *testing.T) {
		msgs := map[string][]model.Message{
			"llm1": {model.NewUserMessage("hi")},
		}
		raw, err := json.Marshal(msgs)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))

		out := OneShotMessagesByNodeReducer(decoded, map[string][]model.Message{
			"llm2": {model.NewUserMessage("yo")},
		})
		merged, ok := out.(map[string][]model.Message)
		require.True(t, ok)
		require.Equal(t, "hi", merged["llm1"][0].Content)
		require.Equal(t, "yo", merged["llm2"][0].Content)

		type byNode struct {
			LLM1 []model.Message `json:"llm1"`
		}
		out = OneShotMessagesByNodeReducer(byNode{
			LLM1: []model.Message{model.NewUserMessage("hi")},
		}, map[string][]model.Message{
			"llm2": {model.NewUserMessage("yo")},
		})
		merged, ok = out.(map[string][]model.Message)
		require.True(t, ok)
		require.Equal(t, "hi", merged["llm1"][0].Content)
		require.Equal(t, "yo", merged["llm2"][0].Content)

		out = OneShotMessagesByNodeReducer(
			make(chan int),
			map[string][]model.Message{
				"llm1": {model.NewUserMessage("hi")},
			},
		)
		merged, ok = out.(map[string][]model.Message)
		require.True(t, ok)
		require.Equal(t, "hi", merged["llm1"][0].Content)
	})
}

func TestSafeClone_FiltersUnsafeKeys(t *testing.T) {
	ch := make(chan<- *event.Event, 1)
	execCtx := &ExecutionContext{
		InvocationID: "inv-1",
		EventChan:    ch,
	}
	state := State{
		StateKeyExecContext:   execCtx,
		StateKeyCurrentNodeID: "node-1",
		StateKeyUserInput:     "hello",
		"custom_key":          42,
	}

	clone := state.safeClone()

	// Unsafe keys must be absent.
	require.NotContains(t, clone, StateKeyExecContext)
	require.NotContains(t, clone, StateKeyCurrentNodeID)

	// Safe keys must be present.
	require.Equal(t, "hello", clone[StateKeyUserInput])
	require.Equal(t, 42, clone["custom_key"])
}

func TestSafeClone_DeepCopiesValues(t *testing.T) {
	inner := map[string]any{"a": 1, "b": 2}
	state := State{
		"data": inner,
	}

	clone := state.safeClone()

	// Mutate original; clone must not be affected.
	inner["a"] = 999
	clonedData := clone["data"].(map[string]any)
	require.Equal(t, 1, clonedData["a"])
	require.Equal(t, 2, clonedData["b"])
}

func TestSafeClone_NestedChannelBecomesNilAndSerializable(t *testing.T) {
	// Simulate a value stored under a safe key that happens to
	// contain a channel (e.g. a struct with mixed fields).
	type config struct {
		Name    string
		Notify  chan<- *event.Event
		Counter int
	}

	ch := make(chan<- *event.Event, 1)
	state := State{
		"config": config{
			Name:    "test",
			Notify:  ch,
			Counter: 10,
		},
	}

	clone := state.safeClone()

	// jsonSafeCopy converts structs with channel fields into
	// map[string]any, omitting the channel field entirely.
	cloned, ok := clone["config"].(map[string]any)
	require.True(t, ok, "expected map[string]any, got %T",
		clone["config"])
	require.Equal(t, "test", cloned["Name"])
	require.Equal(t, 10, cloned["Counter"])
	_, hasChan := cloned["Notify"]
	require.False(t, hasChan,
		"channel field should be omitted")

	// The cloned state must be JSON-serializable.
	_, err := json.Marshal(clone)
	require.NoError(t, err)
}

func TestSafeClone_NestedFuncAndMutexBecomeZero(t *testing.T) {
	type wrapper struct {
		Label  string
		Action func() string
		Mu     sync.Mutex
	}

	state := State{
		"wrap": wrapper{
			Label:  "w",
			Action: func() string { return "hi" },
		},
	}

	clone := state.safeClone()

	// Struct with func field is converted to map[string]any.
	cloned, ok := clone["wrap"].(map[string]any)
	require.True(t, ok, "expected map[string]any, got %T",
		clone["wrap"])
	require.Equal(t, "w", cloned["Label"])
	_, hasAction := cloned["Action"]
	require.False(t, hasAction,
		"func field should be omitted")

	// The cloned state must be JSON-serializable.
	_, err := json.Marshal(clone)
	require.NoError(t, err)
}

func TestSafeClone_EmptyState(t *testing.T) {
	state := State{}
	clone := state.safeClone()
	require.NotNil(t, clone)
	require.Empty(t, clone)
}

func TestSafeClone_NilValues(t *testing.T) {
	state := State{
		"nil_val": nil,
		"str_val": "hello",
	}
	clone := state.safeClone()
	require.Nil(t, clone["nil_val"])
	require.Equal(t, "hello", clone["str_val"])
}
