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

func TestTool_DefaultSkipSummarization(t *testing.T) {
	mockAgent := &mockAgent{
		name:        "test-agent",
		description: "A test agent for testing",
	}

	agentTool := NewTool(mockAgent)

	if agentTool.skipSummarization {
		t.Error("Expected skip summarization to be false by default")
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
	for i := 0; i < 4; i++ { // Now expecting 4 events: tool input + original 3 events
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
	// We now get 4 events: tool input event + original 3 events (delta1, delta2, final full)
	if got[0] != `{"request":"hi"}` || got[1] != "hello" || got[2] != " world" || got[3] != "ignored full" {
		t.Fatalf("unexpected forwarded contents: %#v", got)
	}

	// Assert the sub agent saw a filter key starting with its own name (now includes UUID suffix).
	expectedPrefix := sa.name + "-"
	if !strings.HasPrefix(sa.seenFilterKey, expectedPrefix) {
		t.Fatalf("expected sub agent filter key to start with %q, got %q", expectedPrefix, sa.seenFilterKey)
	}
}

func TestTool_HistoryScope_ParentBranch_Streamable_FilterKeyPrefix(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))

	// Parent invocation with base filter key.
	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	r, err := at.StreamableCall(ctx, []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()
	// Drain stream
	for i := 0; i < 4; i++ {
		if _, err := r.Recv(); err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}

	// Expect child filter key prefixed by parent key.
	if !strings.HasPrefix(sa.seenFilterKey, "parent-agent/"+sa.name+"-") {
		t.Fatalf("expected child filter key to start with %q, got %q", "parent-agent/"+sa.name+"-", sa.seenFilterKey)
	}
}

// inspectAgent collects matched contents from session using the invocation's filter key
// and returns them joined by '|'.
type inspectAgent struct{ name string }

func (m *inspectAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	fk := inv.GetEventFilterKey()
	var matched []string
	if inv.Session != nil {
		for i := range inv.Session.Events {
			evt := inv.Session.Events[i]
			if evt.Filter(fk) && evt.Response != nil && len(evt.Response.Choices) > 0 {
				msg := evt.Response.Choices[0].Message
				if msg.Content != "" {
					matched = append(matched, msg.Content)
				}
			}
		}
	}
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage(strings.Join(matched, "|"))}}}}
	close(ch)
	return ch, nil
}

func (m *inspectAgent) Tools() []tool.Tool              { return nil }
func (m *inspectAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "inspect"} }
func (m *inspectAgent) SubAgents() []agent.Agent        { return nil }
func (m *inspectAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_HistoryScope_ParentBranch_Call_InheritsParentHistory(t *testing.T) {
	ia := &inspectAgent{name: "child"}
	at := NewTool(ia, WithHistoryScope(HistoryScopeParentBranch))

	// Build parent session with a prior assistant event under parent branch.
	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-branch"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Append a parent assistant event (author parent, content "PARENT").
	parentEvt := event.NewResponseEvent(parent.InvocationID, "parent", &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("PARENT")}}})
	agent.InjectIntoEvent(parent, parentEvt)
	sess.Events = append(sess.Events, *parentEvt)

	// Call the tool with child input.
	out, err := at.Call(ctx, []byte(`{"request":"CHILD"}`))
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	s, _ := out.(string)
	// Expect both parent content and tool input to be visible via filter inheritance.
	if !strings.Contains(s, "PARENT") || !strings.Contains(s, `{"request":"CHILD"}`) {
		t.Fatalf("expected output to contain both parent and child contents, got: %q", s)
	}
}

func TestTool_HistoryScope_Isolated_Streamable_NoParentPrefix(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true), WithHistoryScope(HistoryScopeIsolated))

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	r, err := at.StreamableCall(ctx, []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()
	for i := 0; i < 4; i++ { // drain
		if _, err := r.Recv(); err != nil {
			t.Fatalf("stream read error: %v", err)
		}
	}
	// Expect isolated (no parent prefix)
	if !strings.HasPrefix(sa.seenFilterKey, sa.name+"-") || strings.HasPrefix(sa.seenFilterKey, "parent-agent/") {
		t.Fatalf("expected isolated child key starting with %q, got %q", sa.name+"-", sa.seenFilterKey)
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

func TestTool_SkipSummarization(t *testing.T) {
	a := &mockAgent{name: "test", description: "test"}

	// Test default (false)
	at1 := NewTool(a)
	if at1.SkipSummarization() {
		t.Errorf("Expected SkipSummarization to be false by default")
	}

	// Test with true
	at2 := NewTool(a, WithSkipSummarization(true))
	if !at2.SkipSummarization() {
		t.Errorf("Expected SkipSummarization to be true")
	}

	// Test with false explicitly
	at3 := NewTool(a, WithSkipSummarization(false))
	if at3.SkipSummarization() {
		t.Errorf("Expected SkipSummarization to be false")
	}
}

// eventErrorMockAgent returns an event with error
type eventErrorMockAgent struct{ name string }

func (m *eventErrorMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "event error occurred"},
		},
	}
	close(ch)
	return ch, nil
}
func (m *eventErrorMockAgent) Tools() []tool.Tool              { return nil }
func (m *eventErrorMockAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "err"} }
func (m *eventErrorMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *eventErrorMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_Call_EventError(t *testing.T) {
	at := NewTool(&eventErrorMockAgent{name: "err-event-agent"})
	_, err := at.Call(context.Background(), []byte(`{"request":"x"}`))
	if err == nil {
		t.Fatalf("expected error from Call when event contains error")
	}
	if !strings.Contains(err.Error(), "event error occurred") {
		t.Fatalf("expected error message to contain 'event error occurred', got: %v", err)
	}
}

func TestTool_Call_WithParentInvocation_EventError(t *testing.T) {
	at := NewTool(&eventErrorMockAgent{name: "err-event-agent"})

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"x"}`))
	if err == nil {
		t.Fatalf("expected error from Call when event contains error")
	}
	if !strings.Contains(err.Error(), "event error occurred") {
		t.Fatalf("expected error message to contain 'event error occurred', got: %v", err)
	}
}

func TestTool_StreamableCall_WithParentInvocation_RunError(t *testing.T) {
	at := NewTool(&errorMockAgent{name: "err-agent"}, WithStreamInner(true))

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	r, err := at.StreamableCall(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected StreamableCall error: %v", err)
	}
	defer r.Close()

	// First chunk might be the tool input event, second should be error
	var foundError bool
	for i := 0; i < 2; i++ {
		ch, err := r.Recv()
		if err != nil {
			break
		}
		if s, ok := ch.Content.(string); ok && strings.Contains(s, "agent tool run error") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Fatalf("expected to find error chunk in stream")
	}
}

func TestTool_StreamableCall_EmptyMessage(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true))

	sess := &session.Session{}
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Call with empty message content
	r, err := at.StreamableCall(ctx, []byte(``))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()

	// Should still receive events (3 from streaming mock)
	for i := 0; i < 3; i++ {
		if _, err := r.Recv(); err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
}

func TestConvertMapToToolSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected *tool.Schema
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name: "invalid JSON - channel type",
			input: map[string]any{
				"invalid": make(chan int), // channels cannot be marshaled to JSON
			},
			expected: nil,
		},
		{
			name: "valid schema",
			input: map[string]any{
				"type":        "object",
				"description": "test schema",
				"properties": map[string]any{
					"field1": map[string]any{"type": "string"},
				},
			},
			expected: &tool.Schema{
				Type:        "object",
				Description: "test schema",
				Properties: map[string]*tool.Schema{
					"field1": {Type: "string"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertMapToToolSchema(tt.input)
			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil result, got: %#v", result)
				}
			} else {
				if result == nil {
					t.Errorf("Expected non-nil result, got nil")
				} else if result.Type != tt.expected.Type || result.Description != tt.expected.Description {
					t.Errorf("Expected %+v, got %+v", tt.expected, result)
				}
			}
		})
	}
}

func TestTool_Call_WithParentInvocation_NoSession(t *testing.T) {
	a := &mockAgent{name: "test", description: "test"}
	at := NewTool(a)

	// Create parent invocation without session
	parent := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Should fall back to isolated runner
	result, err := at.Call(ctx, []byte(`{"request":"test"}`))
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}
	if result == nil {
		t.Fatalf("Expected non-nil result")
	}
}

// nilEventMockAgent sends nil event in stream
type nilEventMockAgent struct{ name string }

func (m *nilEventMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		ch <- nil // Send nil event
		ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}}}
		close(ch)
	}()
	return ch, nil
}
func (m *nilEventMockAgent) Tools() []tool.Tool              { return nil }
func (m *nilEventMockAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "nil"} }
func (m *nilEventMockAgent) SubAgents() []agent.Agent        { return nil }
func (m *nilEventMockAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_StreamableCall_NilEvent(t *testing.T) {
	at := NewTool(&nilEventMockAgent{name: "nil-agent"}, WithStreamInner(true))

	r, err := at.StreamableCall(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("StreamableCall error: %v", err)
	}
	defer r.Close()

	// Should receive the non-nil event (nil event is skipped in fallback path)
	ch, err := r.Recv()
	if err != nil {
		t.Fatalf("unexpected stream read error: %v", err)
	}
	if ch.Content == nil {
		t.Fatalf("expected non-nil content")
	}
}
