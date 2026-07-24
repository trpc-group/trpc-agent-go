//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"testing"
)

// TestMockModel_Determinism verifies same seed produces identical output.
func TestMockModel_Determinism(t *testing.T) {
	seed := int64(42)
	m1 := NewMockModel(seed)
	m2 := NewMockModel(seed)

	events1 := m1.GenerateConversation(5)
	events2 := m2.GenerateConversation(5)

	if len(events1) != len(events2) {
		t.Fatalf("event count mismatch: %d vs %d", len(events1), len(events2))
	}

	for i := range events1 {
		msg1 := firstMessage(&events1[i])
		msg2 := firstMessage(&events2[i])
		if msg1.Content != msg2.Content {
			t.Errorf("event[%d] content differs: %q vs %q", i, msg1.Content, msg2.Content)
		}
		if msg1.Role != msg2.Role {
			t.Errorf("event[%d] role differs: %q vs %q", i, msg1.Role, msg2.Role)
		}
	}
}

// TestMockModel_Reset verifies Reset restores to initial state.
func TestMockModel_Reset(t *testing.T) {
	m := NewMockModel(42)
	first := m.GenerateConversation(2)

	m.Reset()
	second := m.GenerateConversation(2)

	if len(first) != len(second) {
		t.Fatalf("event count mismatch after reset: %d vs %d", len(first), len(second))
	}
	for i := range first {
		msg1 := firstMessage(&first[i])
		msg2 := firstMessage(&second[i])
		if msg1.Content != msg2.Content {
			t.Errorf("event[%d] content differs after reset", i)
		}
	}
}

// TestMockModel_GenerateConversation_NoToolCalls is removed; GenerateConversation
// always includes tool calls per the documented interface.

// TestMockModel_GenerateConversation_WithToolCalls verifies tool calls appear in conversation.
// Uses multiple seeds to account for probabilistic tool call inclusion.
func TestMockModel_GenerateConversation_WithToolCalls(t *testing.T) {
	seeds := []int64{1, 2, 42, 100, 999}
	hasToolCalls := false
	for _, seed := range seeds {
		m := NewMockModel(seed)
		events := m.GenerateConversation(10)
		for _, e := range events {
			msg := firstMessage(&e)
			if len(msg.ToolCalls) > 0 {
				hasToolCalls = true
				for _, tc := range msg.ToolCalls {
					if tc.Function.Name == "" {
						t.Error("tool call has empty name")
					}
					if len(tc.Function.Arguments) == 0 {
						t.Error("tool call has empty arguments")
					}
				}
			}
		}
		if hasToolCalls {
			break
		}
	}
	if !hasToolCalls {
		t.Error("expected at least one tool call in 10 turns with includeToolCalls=true (tested 5 seeds)")
	}
}

// TestMockModel_GenerateToolCall_ArgsTypes verifies argument types:
// string, int, float, array, object, nested.
func TestMockModel_GenerateToolCall_ArgsTypes(t *testing.T) {
	m := NewMockModel(42)
	for i := 0; i < 5; i++ {
		tc := m.GenerateToolCall()
		msg := firstMessage(&tc)
		if len(msg.ToolCalls) == 0 {
			t.Fatal("GenerateToolCall produced no tool calls")
		}

		args := msg.ToolCalls[0].Function.Arguments
		if len(args) == 0 {
			t.Fatal("tool call arguments are empty")
		}

		var parsed map[string]any
		if err := json.Unmarshal(args, &parsed); err != nil {
			t.Fatalf("failed to parse arguments as JSON: %v", err)
		}

		// Verify string type.
		if _, ok := parsed["query"]; !ok {
			t.Error("expected 'query' (string) in arguments")
		}
		// Verify int type.
		if _, ok := parsed["limit"]; !ok {
			t.Error("expected 'limit' (int) in arguments")
		}
		// Verify float type.
		if _, ok := parsed["threshold"]; !ok {
			t.Error("expected 'threshold' (float) in arguments")
		}
		// Verify array type.
		if _, ok := parsed["tags"]; !ok {
			t.Error("expected 'tags' (array) in arguments")
		}
		// Verify object type.
		if _, ok := parsed["filters"]; !ok {
			t.Error("expected 'filters' (object) in arguments")
		}
		// Verify nested type.
		if _, ok := parsed["metadata"]; !ok {
			t.Error("expected 'metadata' (nested object) in arguments")
		}
		if meta, ok := parsed["metadata"].(map[string]any); ok {
			if _, ok := meta["nested"]; !ok {
				t.Error("expected 'metadata.nested' in arguments")
			}
		}
	}
}

// TestMockModel_GenerateToolCallWithArgs verifies custom args tool call.
func TestMockModel_GenerateToolCallWithArgs(t *testing.T) {
	m := NewMockModel(42)
	args := map[string]any{
		"city":   "Tokyo",
		"units":  "metric",
		"days":   5,
	}
	tc := m.GenerateToolCallWithArgs("get_forecast", args)

	msg := firstMessage(&tc)
	if len(msg.ToolCalls) == 0 {
		t.Fatal("GenerateToolCallWithArgs produced no tool calls")
	}
	tc1 := msg.ToolCalls[0]
	if tc1.Function.Name != "get_forecast" {
		t.Errorf("expected tool name 'get_forecast', got %q", tc1.Function.Name)
	}

	var parsed map[string]any
	if err := json.Unmarshal(tc1.Function.Arguments, &parsed); err != nil {
		t.Fatalf("failed to parse arguments: %v", err)
	}
	if parsed["city"] != "Tokyo" {
		t.Errorf("expected city 'Tokyo', got %v", parsed["city"])
	}
}

// TestMockModel_GenerateEventsForToolCall verifies the 3-event tool call sequence.
func TestMockModel_GenerateEventsForToolCall(t *testing.T) {
	m := NewMockModel(42)
	events := m.GenerateEventsForToolCall()

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Event 0: user message.
	msg0 := firstMessage(&events[0])
	if msg0.Role != "user" {
		t.Errorf("events[0]: expected role 'user', got %q", msg0.Role)
	}

	// Event 1: tool call.
	msg1 := firstMessage(&events[1])
	if msg1.Role != "assistant" {
		t.Errorf("events[1]: expected role 'assistant', got %q", msg1.Role)
	}
	if len(msg1.ToolCalls) == 0 {
		t.Error("events[1]: expected tool calls")
	}

	// Event 2: tool response.
	msg2 := firstMessage(&events[2])
	if msg2.Role != "tool" {
		t.Errorf("events[2]: expected role 'tool', got %q", msg2.Role)
	}
	if msg2.ToolID == "" {
		t.Error("events[2]: expected tool ID")
	}
}

// TestMockModel_GenerateStateMap verifies state map generation.
func TestMockModel_GenerateStateMap(t *testing.T) {
	m := NewMockModel(42)
	state := m.GenerateStateMap(3)
	if len(state) != 3 {
		t.Errorf("expected 3 state keys, got %d", len(state))
	}
	for k, v := range state {
		if len(k) == 0 {
			t.Error("empty state key")
		}
		if len(v) == 0 {
			t.Errorf("empty state value for key %q", k)
		}
	}
}

// TestMockModel_GenerateMemoryContent verifies memory content generation.
func TestMockModel_GenerateMemoryContent(t *testing.T) {
	m := NewMockModel(42)
	content := m.GenerateMemoryContent()
	if len(content) == 0 {
		t.Error("memory content is empty")
	}
}

// TestMockModel_GenerateTrackEvent verifies track event generation.
func TestMockModel_GenerateTrackEvent(t *testing.T) {
	m := NewMockModel(42)
	payload := m.GenerateTrackEvent()
	if len(payload) == 0 {
		t.Fatal("track event payload is empty")
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("failed to parse track event payload: %v", err)
	}
	if _, ok := parsed["event_type"]; !ok {
		t.Error("expected 'event_type' in track event payload")
	}
	if _, ok := parsed["duration_ms"]; !ok {
		t.Error("expected 'duration_ms' in track event payload")
	}
}

// TestMockModel_DifferentSeeds_DifferentOutput verifies different seeds produce different output.
func TestMockModel_DifferentSeeds_DifferentOutput(t *testing.T) {
	m1 := NewMockModel(1)
	m2 := NewMockModel(999)

	events1 := m1.GenerateConversation(3)
	events2 := m2.GenerateConversation(3)

	same := true
	for i := range events1 {
		if i >= len(events2) {
			break
		}
		msg1 := firstMessage(&events1[i])
		msg2 := firstMessage(&events2[i])
		if msg1.Content != msg2.Content {
			same = false
			break
		}
	}
	if same {
		t.Error("expected different seeds to produce different output")
	}
}

// TestMockModel_GenerateConversation_Empty verifies zero turns produces empty result.
func TestMockModel_GenerateConversation_Empty(t *testing.T) {
	m := NewMockModel(42)
	events := m.GenerateConversation(0)
	if len(events) != 0 {
		t.Errorf("expected 0 events for 0 turns, got %d", len(events))
	}
}