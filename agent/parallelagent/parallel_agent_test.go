//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package parallelagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockAgent is a test implementation of agent.Agent.
type mockAgent struct {
	name         string
	shouldError  bool
	eventCount   int
	eventContent string
	delay        time.Duration
	tools        []tool.Tool
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

// SubAgents implements the agent.Agent interface for testing.
func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements the agent.Agent interface for testing.
func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.shouldError {
		return nil, errors.New("mock agent error")
	}

	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Add delay to simulate processing time.
		if m.delay > 0 {
			select {
			case <-time.After(m.delay):
			case <-ctx.Done():
				return
			}
		}

		// Generate events.
		for i := 0; i < m.eventCount; i++ {
			evt := event.New(invocation.InvocationID, m.name)
			evt.Object = "test.completion"
			evt.Done = i == m.eventCount-1

			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

// legacyOptions mirrors old Options struct for tests.
type legacyOptions struct {
	Name              string
	SubAgents         []agent.Agent
	ChannelBufferSize int
	AgentCallbacks    *agent.Callbacks
}

func newFromLegacy(o legacyOptions) *ParallelAgent {
	opts := []Option{}
	if len(o.SubAgents) > 0 {
		opts = append(opts, WithSubAgents(o.SubAgents))
	}
	if o.ChannelBufferSize > 0 {
		opts = append(opts, WithChannelBufferSize(o.ChannelBufferSize))
	}
	if o.AgentCallbacks != nil {
		opts = append(opts, WithAgentCallbacks(o.AgentCallbacks))
	}
	return New(o.Name, opts...)
}

func TestParallelAgent_Basic(t *testing.T) {
	// Create mock sub-agents.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 2, delay: 10 * time.Millisecond}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, delay: 5 * time.Millisecond}

	// Create ParallelAgent.
	parallelAgent := newFromLegacy(legacyOptions{
		Name:      "test-parallel",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "test-001",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventChan, err := parallelAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Verify events count (2 + 1 = 3 events).
	require.Equal(t, 3, len(events))

	// Verify both agents contributed.
	agentCounts := make(map[string]int)
	for _, evt := range events {
		agentCounts[evt.Author]++
	}

	require.Equal(t, 2, agentCounts["agent-1"])
	require.Equal(t, 1, agentCounts["agent-2"])
}

func TestParallelAgent_WithError(t *testing.T) {
	// Create agents, one with error.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1}
	subAgent2 := &mockAgent{name: "agent-2", shouldError: true}

	parallelAgent := newFromLegacy(legacyOptions{
		Name:      "test-parallel",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	invocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "test-002",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	eventChan, err := parallelAgent.Run(ctx, invocation)
	require.NoError(t, err)

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have events from successful agent and error event.
	require.Greater(t, len(events), 0)

	// Check for error event.
	hasError := false
	for _, evt := range events {
		if evt.Error != nil {
			hasError = true
		}
	}
	require.True(t, hasError)
}

func TestParallelAgent_BranchInvoke(t *testing.T) {
	parallelAgent := newFromLegacy(legacyOptions{Name: "test-parallel"})

	baseInvocation := agent.NewInvocation(
		agent.WithInvocationAgent(parallelAgent),
	)

	subAgent := &mockAgent{name: "agent-1", eventCount: 1}
	branchInvocation := parallelAgent.createBranchInvocation(subAgent, baseInvocation)

	require.Equal(t, "test-parallel", baseInvocation.GetEventFilterKey())
	// Verify branch has different ID.
	require.NotEqual(t, branchInvocation.InvocationID, baseInvocation.InvocationID)
	// Verify agent is set.
	require.NotNil(t, branchInvocation.Agent)
	require.Equal(t, subAgent.Info().Name, branchInvocation.Agent.Info().Name)
	require.Equal(t, "test-parallel/agent-1", branchInvocation.GetEventFilterKey())
}

func TestParallelAgent_ChannelBufferSize(t *testing.T) {
	// Test default.
	agent1 := newFromLegacy(legacyOptions{Name: "test1"})
	require.Equal(t, defaultChannelBufferSize, agent1.channelBufferSize)

	// Test custom.
	agent2 := newFromLegacy(legacyOptions{Name: "test2", ChannelBufferSize: 100})
	require.Equal(t, 100, agent2.channelBufferSize)
}

func TestParallelAgent_WithCallbacks(t *testing.T) {
	// Create agent callbacks.
	callbacks := agent.NewCallbacks()

	// Test before agent callback that skips execution.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		if invocation.Message.Content == "skip" {
			return nil, nil
		}
		return nil, nil
	})

	// Create parallel agent with callbacks.
	parallelAgent := newFromLegacy(legacyOptions{
		Name:           "test-parallel",
		SubAgents:      []agent.Agent{&mockAgent{name: "agent1"}, &mockAgent{name: "agent2"}},
		AgentCallbacks: callbacks,
	})

	// Test skip execution.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-skip",
		AgentName:    "test-parallel",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "skip",
		},
	}

	ctx := context.Background()
	eventChan, err := parallelAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Should not receive any events since execution was skipped.
	// Wait a bit to ensure no events are sent.
	time.Sleep(50 * time.Millisecond)

	// Check if channel is closed (no events sent).
	select {
	case evt, ok := <-eventChan:
		require.False(t, ok, "Expected no events, but received: %v", evt)
		// If ok is false, channel is closed which is expected.
	default:
		// Channel is still open, which means no events were sent (expected).
	}
}

// silentAgent emits zero events and returns immediately.
type silentAgent struct{ name string }

func (s *silentAgent) Info() agent.Info                { return agent.Info{Name: s.name} }
func (s *silentAgent) SubAgents() []agent.Agent        { return nil }
func (s *silentAgent) FindSubAgent(string) agent.Agent { return nil }
func (s *silentAgent) Tools() []tool.Tool              { return nil }
func (s *silentAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	close(ch)
	return ch, nil
}

type failAgent struct{ name string }

func (f *failAgent) Info() agent.Info                { return agent.Info{Name: f.name} }
func (f *failAgent) SubAgents() []agent.Agent        { return nil }
func (f *failAgent) FindSubAgent(string) agent.Agent { return nil }
func (f *failAgent) Tools() []tool.Tool              { return nil }
func (f *failAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, errors.New("boom")
}

func TestParallelAgent_BeforeErr(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("bad before")
	})

	pa := newFromLegacy(legacyOptions{
		Name:           "parallel",
		SubAgents:      []agent.Agent{&silentAgent{"a"}},
		AgentCallbacks: cb,
	})

	events, err := pa.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "parallel"})
	require.NoError(t, err)

	var evt *event.Event
	for e := range events {
		evt = e
	}
	require.NotNil(t, evt)
	require.NotNil(t, evt.Error)
	require.Equal(t, agent.ErrorTypeAgentCallbackError, evt.Error.Type)
}

func TestParallelAgent_AfterResp(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return &model.Response{Object: "after", Done: true}, nil
	})

	pa := newFromLegacy(legacyOptions{
		Name:           "parallel",
		SubAgents:      []agent.Agent{&silentAgent{"a"}},
		AgentCallbacks: cb,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	events, err := pa.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "parallel"})
	require.NoError(t, err)

	var last *event.Event
	for e := range events {
		last = e
	}
	require.NotNil(t, last)
	require.Equal(t, "after", last.Object)
}

func TestParallelAgent_BeforeResp(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{Object: "before", Done: true}, nil
	})

	pa := newFromLegacy(legacyOptions{Name: "parallel", SubAgents: []agent.Agent{&silentAgent{"a"}}, AgentCallbacks: cb})

	events, err := pa.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "parallel"})
	require.NoError(t, err)

	var first *event.Event
	for e := range events {
		first = e
	}
	require.NotNil(t, first)
	require.Equal(t, "before", first.Object)
}

// panicTestAgent is a mock agent that panics during execution.
type panicTestAgent struct {
	name string
}

func (p *panicTestAgent) Info() agent.Info {
	return agent.Info{Name: p.name, Description: "Panic test agent"}
}

func (p *panicTestAgent) SubAgents() []agent.Agent {
	return nil
}

func (p *panicTestAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (p *panicTestAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Simulate panic - like the user mentioned Rerank method panic
	panic("test panic in custom method (similar to Rerank panic)")
}

func (p *panicTestAgent) Tools() []tool.Tool {
	return nil
}

// TestParallelAgent_PanicRecovery tests that ParallelAgent properly recovers from panics in sub-agents.
func TestParallelAgent_PanicRecovery(t *testing.T) {
	// Create a normal agent and a panic agent
	normalAgent := &mockAgent{name: "normal-agent", eventCount: 1}
	panickyAgent := &panicTestAgent{name: "panic-test-agent"}

	// Create parallel agent
	parallelAgent := newFromLegacy(legacyOptions{
		Name:      "test-parallel",
		SubAgents: []agent.Agent{normalAgent, panickyAgent},
	})

	// Create invocation
	invocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "test-panic-recovery",
	}

	// Set timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run the parallel agent
	eventChan, err := parallelAgent.Run(ctx, invocation)
	require.NoError(t, err, "ParallelAgent.Run should not return error even when sub-agent panics")

	// Collect events
	var events []*event.Event
	var errorEvents []*event.Event
	var normalEvents []*event.Event

	for evt := range eventChan {
		events = append(events, evt)
		if evt.Error != nil {
			errorEvents = append(errorEvents, evt)
			t.Logf("Received error event: %v", evt.Error.Message)
		} else {
			normalEvents = append(normalEvents, evt)
		}
	}

	// Verify we received events
	require.Greater(t, len(events), 0, "Should have received some events")

	// Verify we got an error event about the panic
	require.Greater(t, len(errorEvents), 0, "Should have received at least one error event from panic")

	// Check that the error event contains panic information
	foundPanicError := false
	for _, evt := range errorEvents {
		if evt.Error != nil && strings.Contains(evt.Error.Message, "panic") {
			foundPanicError = true
			break
		}
	}
	require.True(t, foundPanicError, "Should have received an error event describing the panic")

	// Verify that the normal agent still ran successfully
	require.Greater(t, len(normalEvents), 0, "Normal agent should have produced events despite panic in other agent")
}

// TestParallelAgent_MultiplePanics tests recovery from multiple simultaneous panics.
func TestParallelAgent_MultiplePanics(t *testing.T) {
	// Create multiple panic agents.
	panicAgent1 := &panicTestAgent{name: "panic-agent-1"}
	panicAgent2 := &panicTestAgent{name: "panic-agent-2"}
	normalAgent := &mockAgent{name: "normal-agent", eventCount: 1}

	// Create parallel agent.
	parallelAgent := newFromLegacy(legacyOptions{
		Name:      "test-multi-panic",
		SubAgents: []agent.Agent{panicAgent1, normalAgent, panicAgent2},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-multi-panic",
		InvocationID: "test-multiple-panics",
	}

	// Set timeout context.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run the parallel agent.
	eventChan, err := parallelAgent.Run(ctx, invocation)
	require.NoError(t, err, "Should handle multiple panics gracefully")

	// Collect events.
	var errorEvents []*event.Event
	for evt := range eventChan {
		if evt.Error != nil {
			errorEvents = append(errorEvents, evt)
		}
	}

	// Should have received error events for both panics.
	require.GreaterOrEqual(t, len(errorEvents), 2, "Should have error events for multiple panics")
}

func TestParallelAgent_AfterCallbackError(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return nil, errors.New("after callback failed")
	})

	pa := newFromLegacy(legacyOptions{
		Name:           "parallel",
		SubAgents:      []agent.Agent{&silentAgent{"a"}},
		AgentCallbacks: cb,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	events, err := pa.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "parallel"})
	require.NoError(t, err)

	var last *event.Event
	for e := range events {
		last = e
	}
	require.NotNil(t, last)
	require.NotNil(t, last.Error)
	require.Equal(t, agent.ErrorTypeAgentCallbackError, last.Error.Type)
}

func TestParallelAgent_ToolsAndSubAgents(t *testing.T) {
	sub1 := &silentAgent{name: "sub1"}
	sub2 := &silentAgent{name: "sub2"}

	pa := newFromLegacy(legacyOptions{
		Name:      "parallel",
		SubAgents: []agent.Agent{sub1, sub2},
	})

	// Test Tools.
	tools := pa.Tools()
	require.Empty(t, tools)

	// Test SubAgents.
	subs := pa.SubAgents()
	require.Len(t, subs, 2)

	// Test FindSubAgent.
	found := pa.FindSubAgent("sub1")
	require.NotNil(t, found)
	require.Equal(t, "sub1", found.Info().Name)

	notFound := pa.FindSubAgent("nonexistent")
	require.Nil(t, notFound)
}

// TestParallelAgent_CallbackContextPropagation tests that context values set in
// BeforeAgent callback can be retrieved in AfterAgent callback.
func TestParallelAgent_CallbackContextPropagation(t *testing.T) {
	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value-from-before"

	// Create callbacks that set and read context values.
	callbacks := agent.NewCallbacks()
	var capturedValue any

	// BeforeAgent callback sets a context value.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		ctxWithValue := context.WithValue(ctx, testKey, testValue)
		return &agent.BeforeAgentResult{
			Context: ctxWithValue,
		}, nil
	})

	// AfterAgent callback reads the context value.
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		capturedValue = ctx.Value(testKey)
		return nil, nil
	})

	// Create parallel agent with callbacks.
	subAgent := &silentAgent{name: "child"}
	parallelAgent := newFromLegacy(legacyOptions{
		Name:           "test-parallel",
		SubAgents:      []agent.Agent{subAgent},
		AgentCallbacks: callbacks,
	})

	// Run the agent.
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-parallel",
	}

	events, err := parallelAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Consume all events to ensure callbacks are executed.
	for range events {
	}

	// Verify that the context value was captured in AfterAgent callback.
	require.Equal(t, testValue, capturedValue, "context value should be propagated from BeforeAgent to AfterAgent")
}
