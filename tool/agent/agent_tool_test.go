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
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a simple mock agent for testing.
type mockAgent struct {
	name        string
	description string
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Mock implementation - return a simple response.
	eventChan := make(chan *event.Event, 1)

	response := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.NewAssistantMessage("Hello from mock agent!"),
				},
			},
		},
	}

	go func() {
		eventChan <- response
		close(eventChan)
	}()

	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func TestNewTool(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)

	if agentTool.name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", agentTool.name)
	}

	if agentTool.description != "A test agent for testing" {
		t.Errorf("Expected description 'A test agent for testing', got '%s'", agentTool.description)
	}

	if agentTool.agent != mockAgent {
		t.Error("Expected agent to be the same as the input agent")
	}
}

func TestTool_Declaration(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)
	declaration := agentTool.Declaration()

	if declaration.Name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", declaration.Name)
	}

	if declaration.Description != "A test agent for testing" {
		t.Errorf("Expected description 'A test agent for testing', got '%s'", declaration.Description)
	}

	if declaration.InputSchema == nil {
		t.Error("Expected InputSchema to not be nil")
	}

	if declaration.OutputSchema == nil {
		t.Error("Expected OutputSchema to not be nil")
	}
}

func TestTool_Call(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)

	// Test input
	input := struct {
		Request string `json:"request"`
	}{
		Request: "Hello, agent!",
	}

	jsonArgs, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Failed to marshal input: %v", err)
	}

	// Call the agent tool.
	result, err := agentTool.Call(context.Background(), jsonArgs)
	if err != nil {
		t.Fatalf("Failed to call agent tool: %v", err)
	}

	// Check the result.
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("Expected result to be string, got %T", result)
	}

	if resultStr == "" {
		t.Error("Expected non-empty result")
	}
}

func TestTool_WithSkipSummarization(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent, WithSkipSummarization(true))

	if !agentTool.skipSummarization {
		t.Error("Expected skip summarization to be true")
	}
}

// streamingMockAgent streams a few delta events then a final full message.
type streamingMockAgent struct {
	name string
}

func (m *streamingMockAgent) Run(ctx context.Context, _ *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 3)
	go func() {
		defer close(ch)
		// delta 1
		ch <- &event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: "hello"}}}}}
		// delta 2
		ch <- &event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: " world"}}}}}
		// final full assistant message (should not be forwarded by UI typically)
		ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ignored full"}}}}}
	}()
	return ch, nil
}

func (m *streamingMockAgent) Tools() []tool.Tool { return nil }
func (m *streamingMockAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "streaming mock"}
}
func (m *streamingMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *streamingMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_StreamInner_And_StreamableCall(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true))

	if !at.StreamInner() {
		t.Fatalf("expected StreamInner to be true")
	}

	// Invoke stream
	reader, err := at.StreamableCall(context.Background(), []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer reader.Close()

	// Expect to receive forwarded event chunks
	var got []string
	for i := 0; i < 3; i++ {
		chunk, err := reader.Recv()
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if ev, ok := chunk.Content.(*event.Event); ok {
			if len(ev.Choices) > 0 {
				if ev.Choices[0].Delta.Content != "" {
					got = append(got, ev.Choices[0].Delta.Content)
				} else if ev.Choices[0].Message.Content != "" {
					got = append(got, ev.Choices[0].Message.Content)
				}
			}
		} else {
			t.Fatalf("expected chunk content to be *event.Event, got %T", chunk.Content)
		}
	}
	// We pushed 3 events; delta1, delta2, final full
	if got[0] != "hello" || got[1] != " world" || got[2] != "ignored full" {
		t.Fatalf("unexpected forwarded contents: %#v", got)
	}
}

func TestTool_StreamInner_FlagFalse(t *testing.T) {
	a := &mockAgent{name: "agent-x", description: "d"}
	at := NewTool(a, WithStreamInner(false))
	if at.StreamInner() {
		t.Fatalf("expected StreamInner to be false")
	}
}
