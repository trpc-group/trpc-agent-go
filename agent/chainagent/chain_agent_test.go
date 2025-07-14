//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package chainagent

import (
	"context"
	"errors"
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
	name           string
	shouldError    bool
	eventCount     int
	eventContent   string
	executionOrder *[]string // Track execution order
	tools          []tool.Tool
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

		// Record execution order if tracking is enabled.
		if m.executionOrder != nil {
			*m.executionOrder = append(*m.executionOrder, m.name)
		}

		// Generate the specified number of events.
		for i := 0; i < m.eventCount; i++ {
			evt := event.New(invocation.InvocationID, m.name)
			evt.Object = "test.completion"

			// Add some content to simulate real events.
			choice := model.Choice{
				Index: i,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: m.eventContent,
				},
			}
			evt.Choices = []model.Choice{choice}
			evt.Done = i == m.eventCount-1 // Mark last event as done.

			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return
			}

			// Small delay to simulate processing time.
			time.Sleep(1 * time.Millisecond)
		}
	}()

	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func TestChainAgent_Sequential(t *testing.T) {
	// Track execution order.
	var executionOrder []string

	// Create mock sub-agents.
	subAgent1 := &mockAgent{
		name:           "agent-1",
		eventCount:     2,
		eventContent:   "Response from agent 1",
		executionOrder: &executionOrder,
	}
	subAgent2 := &mockAgent{
		name:           "agent-2",
		eventCount:     1,
		eventContent:   "Response from agent 2",
		executionOrder: &executionOrder,
	}
	subAgent3 := &mockAgent{
		name:           "agent-3",
		eventCount:     1,
		eventContent:   "Response from agent 3",
		executionOrder: &executionOrder,
	}

	// Create ChainAgent.
	chainAgent := New(
		"test-chain",
		WithSubAgents([]agent.Agent{subAgent1, subAgent2, subAgent3}),
		WithChannelBufferSize(20),
	)

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-chain",
		InvocationID: "test-invocation-001",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventChan, err := chainAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect all events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Verify events count (2 + 1 + 1 = 4 events).
	expectedEventCount := 4
	require.Equal(t, expectedEventCount, len(events))

	// Verify execution order (agents should run sequentially).
	expectedOrder := []string{"agent-1", "agent-2", "agent-3"}
	require.Equal(t, len(expectedOrder), len(executionOrder))
	for i, expected := range expectedOrder {
		require.Equal(t, expected, executionOrder[i])
	}

	// Verify event authors match execution order.
	agentEventCounts := map[string]int{
		"agent-1": 0,
		"agent-2": 0,
		"agent-3": 0,
	}
	for _, evt := range events {
		agentEventCounts[evt.Author]++
	}

	require.Equal(t, 2, agentEventCounts["agent-1"])
	require.Equal(t, 1, agentEventCounts["agent-2"])
	require.Equal(t, 1, agentEventCounts["agent-3"])
}

func TestChainAgent_SubAgentError(t *testing.T) {
	// Create mock sub-agents with one that errors.
	subAgent1 := &mockAgent{
		name:         "agent-1",
		eventCount:   1,
		eventContent: "Response from agent 1",
	}
	subAgent2 := &mockAgent{
		name:        "agent-2",
		shouldError: true, // This agent will error.
	}
	subAgent3 := &mockAgent{
		name:         "agent-3",
		eventCount:   1,
		eventContent: "Response from agent 3",
	}

	// Create ChainAgent.
	chainAgent := New(
		"test-chain",
		WithSubAgents([]agent.Agent{subAgent1, subAgent2, subAgent3}),
	)

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-chain",
		InvocationID: "test-invocation-002",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventChan, err := chainAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect all events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have 1 event from agent-1 + 1 error event = 2 events.
	// agent-3 should not execute because agent-2 errored.
	expectedEventCount := 2
	require.Equal(t, expectedEventCount, len(events))

	// Last event should be an error event.
	lastEvent := events[len(events)-1]
	require.NotNil(t, lastEvent.Error)
	require.Equal(t, model.ErrorTypeFlowError, lastEvent.Error.Type)
}

func TestChainAgent_EmptySubAgents(t *testing.T) {
	// Create ChainAgent with no sub-agents.
	chainAgent := New(
		"test-chain",
		WithSubAgents([]agent.Agent{}),
	)

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-chain",
		InvocationID: "test-invocation-003",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	eventChan, err := chainAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect all events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have no events.
	require.Equal(t, 0, len(events))
}

func TestChainAgent_Tools(t *testing.T) {
	// Create some mock tools.
	tools := []tool.Tool{} // Empty for now since we don't have concrete tool implementations.

	chainAgent := New(
		"test-chain",
		WithTools(tools),
	)

	require.Equal(t, len(tools), len(chainAgent.Tools()))
}

func TestChainAgent_ChannelBufferSize(t *testing.T) {
	// Test default buffer size.
	chainAgent1 := New(
		"test-chain-1",
	)
	require.Equal(t, defaultChannelBufferSize, chainAgent1.channelBufferSize)

	// Test custom buffer size.
	customSize := 100
	chainAgent2 := New(
		"test-chain-2",
		WithChannelBufferSize(customSize),
	)
	require.Equal(t, customSize, chainAgent2.channelBufferSize)
}

func TestChainAgent_WithCallbacks(t *testing.T) {
	// Create agent callbacks.
	callbacks := agent.NewAgentCallbacks()

	// Test before agent callback that skips execution
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		if invocation.Message.Content == "skip" {
			return nil, nil
		}
		return nil, nil
	})

	// Create chain agent with callbacks.
	chainAgent := New(
		"test-chain-agent",
		WithSubAgents([]agent.Agent{&mockAgent{name: "agent1"}, &mockAgent{name: "agent2"}}),
		WithAgentCallbacks(callbacks),
	)

	// Test skip execution.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-skip",
		AgentName:    "test-chain-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "skip",
		},
	}

	ctx := context.Background()
	eventChan, err := chainAgent.Run(ctx, invocation)
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

// mockMinimalAgent is a lightweight agent used for invocation tests.
type mockMinimalAgent struct {
	name string
}

func (m *mockMinimalAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *mockMinimalAgent) SubAgents() []agent.Agent             { return nil }
func (m *mockMinimalAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *mockMinimalAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	close(ch)
	return ch, nil
}
func (m *mockMinimalAgent) Tools() []tool.Tool { return nil }

func TestCreateSubAgentInvocation(t *testing.T) {
	parent := New(
		"parent",
	)
	base := &agent.Invocation{
		InvocationID: "inv-1",
		AgentName:    "parent",
		Message:      model.Message{Role: model.RoleUser, Content: "hi"},
		Branch:       "root",
	}

	sub := &mockMinimalAgent{name: "child"}
	inv := parent.createSubAgentInvocation(sub, base)

	require.Equal(t, "child", inv.AgentName)
	require.Equal(t, "root.child", inv.Branch)
	// Ensure original invocation not mutated.
	require.Equal(t, "parent", base.AgentName)
	require.Equal(t, "root", base.Branch)
}

func TestCreateSubAgentInvokeNoBranch(t *testing.T) {
	parent := New(
		"parent",
	)
	base := &agent.Invocation{InvocationID: "id", AgentName: "parent"}
	sub := &mockMinimalAgent{name: "child"}

	inv := parent.createSubAgentInvocation(sub, base)

	require.Equal(t, "child", inv.AgentName)
	require.Equal(t, "parent.child", inv.Branch)
}

func TestChainAgent_FindSubAgentAndInfo(t *testing.T) {
	a1 := &mockMinimalAgent{name: "a1"}
	a2 := &mockMinimalAgent{name: "a2"}
	chain := New(
		"root",
		WithSubAgents([]agent.Agent{a1, a2}),
	)

	require.Equal(t, "root", chain.Info().Name)

	found := chain.FindSubAgent("a2")
	require.NotNil(t, found)
	require.Equal(t, "a2", found.Info().Name)

	notFound := chain.FindSubAgent("missing")
	require.Nil(t, notFound)
}

func TestChainAgent_AfterCallback(t *testing.T) {
	// Prepare mock agents – none produce events.
	minimal := &mockMinimalAgent{name: "child"}

	// Prepare callbacks with after agent producing custom response.
	callbacks := agent.NewAgentCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, _ error) (*model.Response, error) {
		return &model.Response{
			Object: "test.response",
			Done:   true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "done",
				},
			}},
		}, nil
	})

	chain := New(
		"root",
		WithSubAgents([]agent.Agent{minimal}),
		WithAgentCallbacks(callbacks),
	)

	inv := &agent.Invocation{
		InvocationID: "inv-2",
		AgentName:    "root",
	}

	ctx := context.Background()
	events, err := chain.Run(ctx, inv)
	require.NoError(t, err)

	// Expect exactly one event produced by after-agent callback.
	count := 0
	for e := range events {
		count++
		require.Equal(t, "root", e.Author)
		require.Equal(t, "test.response", e.Object)
		require.True(t, e.Done)
	}
	require.Equal(t, 1, count)
}

// mockNoEventAgent is a sub-agent that never produces events (used to
// verify short-circuit behaviour when a before-callback returns).
type mockNoEventAgent struct{ name string }

func (m *mockNoEventAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockNoEventAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockNoEventAgent) FindSubAgent(string) agent.Agent { return nil }
func (m *mockNoEventAgent) Tools() []tool.Tool              { return nil }
func (m *mockNoEventAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	close(ch)
	return ch, nil
}

func TestChainAgent_BeforeCallbackResp(t *testing.T) {
	// Sub-agent should never run.
	sub := &mockNoEventAgent{name: "child"}

	callbacks := agent.NewAgentCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{
			Object: "test.before",
			Done:   true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "skipped",
				},
			}},
		}, nil
	})

	chain := New(
		"main",
		WithSubAgents([]agent.Agent{sub}),
		WithAgentCallbacks(callbacks),
	)

	ctx := context.Background()
	events, err := chain.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "main"})
	require.NoError(t, err)

	// Collect events.
	collected := []*event.Event{}
	for e := range events {
		collected = append(collected, e)
	}

	require.Len(t, collected, 1)
	require.Equal(t, "main", collected[0].Author)
	require.Equal(t, "test.before", collected[0].Object)
}

func TestChainAgent_BeforeCallbackError(t *testing.T) {
	sub := &mockNoEventAgent{name: "child"}

	callbacks := agent.NewAgentCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("failure in before")
	})

	chain := New(
		"main",
		WithSubAgents([]agent.Agent{sub}),
		WithAgentCallbacks(callbacks),
	)

	ctx := context.Background()
	events, err := chain.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "main"})
	require.NoError(t, err)

	// Expect exactly one error event.
	cnt := 0
	for e := range events {
		cnt++
		require.NotNil(t, e.Error)
		require.Equal(t, agent.ErrorTypeAgentCallbackError, e.Error.Type)
	}
	require.Equal(t, 1, cnt)
}

// legacyOptions mirrors the old Options struct used before refactor.
type legacyOptions struct {
	Name              string
	SubAgents         []agent.Agent
	Tools             []tool.Tool
	ChannelBufferSize int
	AgentCallbacks    *agent.AgentCallbacks
}

// newFromLegacy adapts legacyOptions to the new functional-option constructor.
func newFromLegacy(o legacyOptions) *ChainAgent {
	opts := []option{WithSubAgents(o.SubAgents)}
	if len(o.Tools) > 0 {
		opts = append(opts, WithTools(o.Tools))
	}
	if o.ChannelBufferSize > 0 {
		opts = append(opts, WithChannelBufferSize(o.ChannelBufferSize))
	}
	if o.AgentCallbacks != nil {
		opts = append(opts, WithAgentCallbacks(o.AgentCallbacks))
	}
	return New(o.Name, opts...)
}
