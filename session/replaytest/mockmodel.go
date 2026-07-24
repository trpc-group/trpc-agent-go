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
	"fmt"
	"math/rand"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MockModel generates reproducible multi-turn conversation and tool call
// event sequences without requiring a real LLM API. The output is deterministic
// given a fixed seed, making it suitable for cross-backend replay testing.
type MockModel struct {
	Seed int64
	rng  *rand.Rand
}

// NewMockModel creates a new MockModel with the given seed.
func NewMockModel(seed int64) *MockModel {
	return &MockModel{
		Seed: seed,
		rng:  rand.New(rand.NewSource(seed)),
	}
}

// GenerateConversation generates a multi-turn conversation as a sequence of events.
// Each turn alternates between a user message and an assistant response.
// Some assistant responses include tool calls with tool responses.
func (m *MockModel) GenerateConversation(turns int) []event.Event {
	events := make([]event.Event, 0, turns*2)

	for i := 0; i < turns; i++ {
		// User message.
		userMsg := m.randomUserMessage(i)
		events = append(events, *m.newEvent("user", userMsg, "", nil, nil))

		if m.rng.Intn(3) == 0 {
			// Every ~3rd turn, insert a tool call + tool response pair.
			tcEvent := m.GenerateToolCall()
			events = append(events, tcEvent)

			// Tool response.
			trEvent := m.newEvent("tool", "Tool executed successfully.",
				tcEvent.Response.Choices[0].Message.ToolCalls[0].ID,
				&tcEvent.Response.Choices[0].Message.ToolCalls[0].Function.Name,
				nil)
			events = append(events, *trEvent)
		} else {
			// Assistant response.
			assistantMsg := m.randomAssistantMessage(i)
			events = append(events, *m.newEvent("assistant", assistantMsg, "", nil, nil))
		}
	}

	return events
}

// GenerateToolCall generates a single tool call event with complex parameters.
// Parameter types cover string, int, float, array, object, and nested structures.
func (m *MockModel) GenerateToolCall() event.Event {
	toolCallID := fmt.Sprintf("call_%s", m.randomString(8))
	toolName := m.randomToolName()

	args := m.generateRandomArgs()
	argsJSON, _ := json.Marshal(args)

	return *m.newEvent("assistant", "",
		toolCallID, &toolName,
		[]model.ToolCall{
			{
				ID:   toolCallID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: argsJSON,
				},
			},
		},
	)
}

// GenerateToolCallWithArgs generates a tool call with specific arguments.
func (m *MockModel) GenerateToolCallWithArgs(toolName string, args map[string]any) event.Event {
	toolCallID := fmt.Sprintf("call_%s", m.randomString(8))
	argsJSON, _ := json.Marshal(args)

	return *m.newEvent("assistant", "",
		toolCallID, &toolName,
		[]model.ToolCall{
			{
				ID:   toolCallID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: argsJSON,
				},
			},
		},
	)
}

// GenerateEventsForToolCall creates a 3-event sequence: user → tool_call → tool_response.
// This is a convenience method for building replay test cases.
func (m *MockModel) GenerateEventsForToolCall() []event.Event {
	userMsg := m.randomUserMessage(0)
	tc := m.GenerateToolCall()

	toolID := tc.Response.Choices[0].Message.ToolCalls[0].ID
	toolName := tc.Response.Choices[0].Message.ToolCalls[0].Function.Name
	tr := m.newEvent("tool", "Tool executed successfully.", toolID, &toolName, nil)

	return []event.Event{
		*m.newEvent("user", userMsg, "", nil, nil),
		tc,
		*tr,
	}
}

// newEvent creates a basic event with the given parameters.
func (m *MockModel) newEvent(author, content, toolID string, toolName *string, toolCalls []model.ToolCall) *event.Event {
	msg := model.Message{
		Content: content,
	}

	switch author {
	case "user":
		msg.Role = model.RoleUser
	case "assistant":
		msg.Role = model.RoleAssistant
		if toolCalls != nil {
			msg.ToolCalls = toolCalls
		}
	case "tool":
		msg.Role = model.RoleTool
		msg.ToolID = toolID
		if toolName != nil {
			msg.ToolName = *toolName
		}
	}

	e := event.New("", author,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{Message: msg},
			},
		}),
	)
	return e
}

// randomUserMessage generates a random user message.
func (m *MockModel) randomUserMessage(turn int) string {
	messages := []string{
		"Hello, can you help me with something?",
		"What's the weather like today?",
		"Can you book a flight to New York?",
		"Tell me about the latest news.",
		"Calculate 15% of 3400.",
		"Write a short poem about coding.",
		"Translate 'hello' to French.",
		"What is the capital of Australia?",
		"Can you summarize this article for me?",
		"Find the best restaurants nearby.",
	}
	return messages[m.rng.Intn(len(messages))]
}

// randomAssistantMessage generates a random assistant response.
func (m *MockModel) randomAssistantMessage(turn int) string {
	messages := []string{
		"Sure, I'd be happy to help you with that!",
		"The weather today is sunny with a high of 25°C.",
		"I've found a flight to New York for tomorrow at 3 PM.",
		"Here are the latest headlines from today.",
		"15% of 3400 is 510.",
		"Here's a short poem for you:\nCode by night, debug by day,\nIn logic's maze we find our way.",
		"'Hello' in French is 'Bonjour'.",
		"The capital of Australia is Canberra.",
		"I'd be happy to summarize the article. Please share it with me.",
		"Here are the top-rated restaurants nearby.",
	}
	return messages[m.rng.Intn(len(messages))]
}

// randomToolName generates a random tool name.
func (m *MockModel) randomToolName() string {
	tools := []string{
		"get_weather",
		"search_flights",
		"calculate",
		"send_email",
		"create_document",
		"search_database",
		"analyze_sentiment",
		"generate_image",
	}
	return tools[m.rng.Intn(len(tools))]
}

// randomString generates a random alphanumeric string of the given length.
func (m *MockModel) randomString(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[m.rng.Intn(len(chars))]
	}
	return string(result)
}

// generateRandomArgs generates random tool call arguments covering
// string, int, float, array, object, and nested parameter types.
func (m *MockModel) generateRandomArgs() map[string]any {
	return map[string]any{
		"query":     m.randomString(10),
		"limit":     m.rng.Intn(100),
		"threshold": m.rng.Float64() * 100,
		"tags":      []string{m.randomString(5), m.randomString(5), m.randomString(5)},
		"filters": map[string]any{
			"category": m.randomString(6),
			"enabled":  m.rng.Intn(2) == 1,
			"priority": m.rng.Intn(5) + 1,
		},
		"metadata": map[string]any{
			"source":  m.randomString(8),
			"version": fmt.Sprintf("v%d.%d", m.rng.Intn(10), m.rng.Intn(10)),
			"scores":  []float64{m.rng.Float64(), m.rng.Float64(), m.rng.Float64()},
			"nested": map[string]any{
				"key":   m.randomString(4),
				"value": m.rng.Intn(1000),
			},
		},
	}
}

// Reset resets the random generator to the initial seed for reproducibility.
func (m *MockModel) Reset() {
	m.rng = rand.New(rand.NewSource(m.Seed))
}

// GenerateStateMap generates a random state map for testing.
func (m *MockModel) GenerateStateMap(numKeys int) map[string][]byte {
	state := make(map[string][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key_%s", m.randomString(4))
		value := []byte(fmt.Sprintf("value_%s", m.randomString(8)))
		state[key] = value
	}
	return state
}

// GenerateMemoryContent generates a random memory content string.
func (m *MockModel) GenerateMemoryContent() string {
	templates := []string{
		"The user prefers %s over %s.",
		"User mentioned they work at %s as a %s.",
		"The user's favorite color is %s.",
		"User completed task %s on %s.",
		"The user enjoys %s in their free time.",
	}
	tpl := templates[m.rng.Intn(len(templates))]
	args := []any{m.randomString(6), m.randomString(8)}
	return fmt.Sprintf(tpl, args...)
}

// GenerateTrackEvent generates a random track event payload.
func (m *MockModel) GenerateTrackEvent() json.RawMessage {
	payload := map[string]any{
		"event_type":  []string{"tool_execution", "subtask", "error", "milestone"}[m.rng.Intn(4)],
		"duration_ms": m.rng.Intn(5000),
		"status":      []string{"success", "failed", "running"}[m.rng.Intn(3)],
		"message":     m.randomString(20),
	}
	data, _ := json.Marshal(payload)
	return data
}
