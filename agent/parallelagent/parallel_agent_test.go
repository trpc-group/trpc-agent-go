//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	Tools             []tool.Tool
	ChannelBufferSize int
	AgentCallbacks    *agent.AgentCallbacks
}

func newFromLegacy(o legacyOptions) *ParallelAgent {
	opts := []option{}
	if len(o.SubAgents) > 0 {
		opts = append(opts, WithSubAgents(o.SubAgents))
	}
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
	subAgent := &mockAgent{name: "agent-1", eventCount: 1}
	parallelAgent := newFromLegacy(legacyOptions{Name: "test-parallel"})

	baseInvocation := &agent.Invocation{
		AgentName:    "test-parallel",
		InvocationID: "base-001",
	}

	branchInvocation := parallelAgent.createBranchInvocationForSubAgent(subAgent, baseInvocation)

	// Verify branch has different ID.
	require.NotEqual(t, branchInvocation.InvocationID, baseInvocation.InvocationID)
	// Verify branch contains base ID.
	require.True(t, strings.Contains(branchInvocation.InvocationID, baseInvocation.InvocationID))
	// Verify agent is set.
	require.NotNil(t, branchInvocation.Agent)
	require.Equal(t, subAgent.Info().Name, branchInvocation.Agent.Info().Name)
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
	callbacks := agent.NewAgentCallbacks()

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
	cb := agent.NewAgentCallbacks()
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
	cb := agent.NewAgentCallbacks()
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
	cb := agent.NewAgentCallbacks()
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
