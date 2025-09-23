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
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
	// capture the event filter key seen by Run for assertion.
	seenFilterKey string
}

func (m *streamingMockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// record the filter key used so tests can assert it equals agent name.
	m.seenFilterKey = inv.GetEventFilterKey()
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

	// Prepare a parent invocation context with a session and a different
	// filter key, to ensure sub agent overrides it.
	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Invoke stream
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"hi"}`))
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

	// Assert the sub agent saw its own name as filter key.
	if sa.seenFilterKey != sa.name {
		t.Fatalf("expected sub agent filter key %q, got %q", sa.name, sa.seenFilterKey)
	}
}

func TestTool_StreamInner_FlagFalse(t *testing.T) {
	a := &mockAgent{name: "agent-x", description: "d"}
	at := NewTool(a, WithStreamInner(false))
	if at.StreamInner() {
		t.Fatalf("expected StreamInner to be false")
	}
}

// errorMockAgent returns error from Run
type errorMockAgent struct{ name string }

func (m *errorMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return nil, fmt.Errorf("boom")
}
func (m *errorMockAgent) Tools() []tool.Tool              { return nil }
func (m *errorMockAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "err"} }
func (m *errorMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *errorMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_Call_RunError(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"})
	_, err := at.Call(context.Background(), []byte(`{"request":"x"}`))
	if err == nil {
		t.Fatalf("expected error from Call when agent run fails")
	}
}

func TestTool_StreamableCall_RunErrorEmitsChunk(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"}, WithStreamInner(true))
	r, err := at.StreamableCall(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected StreamableCall error: %v", err)
	}
	defer r.Close()
	ch, err := r.Recv()
	if err != nil {
		t.Fatalf("unexpected stream read error: %v", err)
	}
	if s, ok := ch.Content.(string); !ok || !strings.Contains(s, "agent tool run error") {
		t.Fatalf("expected error chunk, got: %#v", ch.Content)
	}
}

// agentWithSchemaMock returns input/output schema maps in Info()
type agentWithSchemaMock struct{ name, desc string }

func (m *agentWithSchemaMock) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}
func (m *agentWithSchemaMock) Tools() []tool.Tool { return nil }
func (m *agentWithSchemaMock) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.desc,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"request": map[string]any{"type": "string"}},
			"required":   []any{"request"},
		},
		OutputSchema: map[string]any{
			"type":        "string",
			"description": "out",
		},
	}
}
func (m *agentWithSchemaMock) SubAgents() []agent.Agent        { return nil }
func (m *agentWithSchemaMock) FindSubAgent(string) agent.Agent { return nil }

func TestNewTool_UsesAgentSchemas(t *testing.T) {
	at := NewTool(&agentWithSchemaMock{name: "s-agent", desc: "d"})
	decl := at.Declaration()
	if decl.InputSchema == nil || decl.InputSchema.Type != "object" {
		t.Fatalf("expected converted input schema, got: %#v", decl.InputSchema)
	}
	if decl.OutputSchema == nil || decl.OutputSchema.Type != "string" {
		t.Fatalf("expected converted output schema, got: %#v", decl.OutputSchema)
	}
}

// sessionCleanupMockAgent records the session events it sees during Run
type sessionCleanupMockAgent struct {
	name          string
	seenEvents    []event.Event
	seenFilterKey string
}

func (m *sessionCleanupMockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	// Record what we see in the session for testing
	m.seenFilterKey = inv.GetEventFilterKey()
	if inv.Session != nil {
		m.seenEvents = make([]event.Event, len(inv.Session.Events))
		copy(m.seenEvents, inv.Session.Events)
	}
	
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- &event.Event{
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("test response"),
				}},
			},
		}
	}()
	return ch, nil
}

func (m *sessionCleanupMockAgent) Tools() []tool.Tool { return nil }
func (m *sessionCleanupMockAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "session cleanup mock"}
}
func (m *sessionCleanupMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *sessionCleanupMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_SessionCleanup_FiltersPreviousSubAgentEvents(t *testing.T) {
	subAgent := &sessionCleanupMockAgent{name: "test-sub-agent"}
	agentTool := NewTool(subAgent, WithStreamInner(true))

	// Create a session with mixed events
	sess := &session.Session{
		Events: []event.Event{
			// Main agent events (should be kept)
			{FilterKey: "main-agent", Response: &model.Response{Object: "chat.completion"}},
			{FilterKey: "main-agent", Response: &model.Response{Object: "tool.response"}},
			// Previous sub-agent events (should be filtered out)
			{FilterKey: "test-sub-agent", Response: &model.Response{Object: "chat.completion"}},
			{FilterKey: "test-sub-agent", Response: &model.Response{Object: "tool.response"}},
			// Other sub-agent events (should be kept)
			{FilterKey: "other-sub-agent", Response: &model.Response{Object: "chat.completion"}},
		},
	}

	// Create parent invocation with session
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Call StreamableCall which should trigger session cleanup
	reader, err := agentTool.StreamableCall(ctx, []byte(`{"request":"test"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer reader.Close()

	// Read the stream to ensure the agent has run
	_, err = reader.Recv()
	if err != nil {
		t.Fatalf("Failed to read from stream: %v", err)
	}

	// Verify the sub-agent received cleaned session
	if len(subAgent.seenEvents) != 3 {
		t.Fatalf("expected 3 events after cleanup, got %d: %+v", len(subAgent.seenEvents), subAgent.seenEvents)
	}

	// Verify events - should only contain main-agent and other-sub-agent events
	expectedFilterKeys := []string{"main-agent", "main-agent", "other-sub-agent"}
	for i, ev := range subAgent.seenEvents {
		if ev.FilterKey != expectedFilterKeys[i] {
			t.Errorf("event %d: expected FilterKey %s, got %s", i, expectedFilterKeys[i], ev.FilterKey)
		}
	}

	// Verify sub-agent got correct filter key
	if subAgent.seenFilterKey != "test-sub-agent" {
		t.Errorf("expected sub-agent filter key 'test-sub-agent', got %s", subAgent.seenFilterKey)
	}
}

func TestTool_SessionCleanup_EmptySession(t *testing.T) {
	subAgent := &sessionCleanupMockAgent{name: "test-sub-agent"}
	agentTool := NewTool(subAgent, WithStreamInner(true))

	// Create empty session
	sess := &session.Session{Events: []event.Event{}}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Call StreamableCall
	reader, err := agentTool.StreamableCall(ctx, []byte(`{"request":"test"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer reader.Close()

	// Should handle empty session gracefully
	if len(subAgent.seenEvents) != 0 {
		t.Fatalf("expected 0 events for empty session, got %d", len(subAgent.seenEvents))
	}
}

func TestTool_SessionCleanup_NoParentInvocation(t *testing.T) {
	subAgent := &sessionCleanupMockAgent{name: "test-sub-agent"}
	agentTool := NewTool(subAgent, WithStreamInner(true))

	// Call without parent invocation (fallback to ad-hoc runner)
	reader, err := agentTool.StreamableCall(context.Background(), []byte(`{"request":"test"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer reader.Close()

	// Read the stream to ensure the agent has run
	_, err = reader.Recv()
	if err != nil {
		t.Fatalf("Failed to read from stream: %v", err)
	}

	// Should work fine without parent session
	if subAgent.seenFilterKey != "test-sub-agent" {
		t.Errorf("expected sub-agent filter key 'test-sub-agent', got %s", subAgent.seenFilterKey)
	}
}

func TestTool_SessionCleanup_PreservesMainAgentContext(t *testing.T) {
	subAgent := &sessionCleanupMockAgent{name: "math-specialist"}
	agentTool := NewTool(subAgent, WithStreamInner(true))

	// Create session simulating multi-round conversation
	sess := &session.Session{
		Events: []event.Event{
			// First round - main agent and math-specialist interaction
			{FilterKey: "chat-assistant", Response: &model.Response{Object: "chat.completion"}},
			{FilterKey: "math-specialist", Response: &model.Response{Object: "chat.completion"}},
			{FilterKey: "math-specialist", Response: &model.Response{Object: "tool.response"}},
			// Second round - main agent starts new request
			{FilterKey: "chat-assistant", Response: &model.Response{Object: "chat.completion"}},
		},
	}

	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("chat-assistant"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Second round AgentTool call
	reader, err := agentTool.StreamableCall(ctx, []byte(`{"request":"new calculation"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer reader.Close()

	// Read the stream to ensure the agent has run
	_, err = reader.Recv()
	if err != nil {
		t.Fatalf("Failed to read from stream: %v", err)
	}

	// Should only see main agent events, no previous math-specialist events
	expectedEvents := 2 // Two chat-assistant events
	if len(subAgent.seenEvents) != expectedEvents {
		t.Fatalf("expected %d events, got %d: %+v", expectedEvents, len(subAgent.seenEvents), subAgent.seenEvents)
	}

	// All remaining events should be from main agent
	for i, ev := range subAgent.seenEvents {
		if ev.FilterKey != "chat-assistant" {
			t.Errorf("event %d: expected main agent FilterKey 'chat-assistant', got %s", i, ev.FilterKey)
		}
	}
}
