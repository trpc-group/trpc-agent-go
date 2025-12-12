//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cycleagent

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
	name               string
	shouldError        bool
	eventCount         int
	eventContent       string
	shouldTriggerError bool // Generate error event to trigger escalation
	executionCount     *int // Track how many times this agent runs
	tools              []tool.Tool
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

		// Track execution count.
		if m.executionCount != nil {
			*m.executionCount++
		}

		// Generate events.
		for i := 0; i < m.eventCount; i++ {
			var evt *event.Event

			// Generate error event if requested.
			if m.shouldTriggerError && i == m.eventCount-1 {
				evt = event.NewErrorEvent(invocation.InvocationID, m.name, model.ErrorTypeFlowError, "mock escalation")
			} else {
				evt = event.New(invocation.InvocationID, m.name)
				evt.Object = "test.completion"
				evt.Done = i == m.eventCount-1
			}

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

// legacyOptions mirrors old Options struct for test compatibility.
type legacyOptions struct {
	Name              string
	SubAgents         []agent.Agent
	MaxIterations     *int
	ChannelBufferSize int
	AgentCallbacks    *agent.Callbacks
	EscalationFunc    EscalationFunc
}

// newFromLegacy converts legacyOptions to functional options constructor.
func newFromLegacy(o legacyOptions) *CycleAgent {
	opts := []Option{WithSubAgents(o.SubAgents)}
	if o.MaxIterations != nil {
		opts = append(opts, WithMaxIterations(*o.MaxIterations))
	}
	if o.ChannelBufferSize > 0 {
		opts = append(opts, WithChannelBufferSize(o.ChannelBufferSize))
	}
	if o.AgentCallbacks != nil {
		opts = append(opts, WithAgentCallbacks(o.AgentCallbacks))
	}
	if o.EscalationFunc != nil {
		opts = append(opts, WithEscalationFunc(o.EscalationFunc))
	}
	return New(o.Name, opts...)
}

func TestCycleAgent_Run_WithMaxIter(t *testing.T) {
	// Track execution counts.
	agent1Count := 0
	agent2Count := 0

	// Create mock sub-agents.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, executionCount: &agent1Count}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, executionCount: &agent2Count}

	// Create CycleAgent with max iterations.
	maxIter := 2
	cycleAgent := newFromLegacy(legacyOptions{
		Name:          "test-cycle",
		SubAgents:     []agent.Agent{subAgent1, subAgent2},
		MaxIterations: &maxIter,
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-001",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should run 2 iterations * 2 agents = 4 events.
	require.Equal(t, 4, len(events))

	// Each agent should run exactly maxIter times.
	require.Equal(t, maxIter, agent1Count)
	require.Equal(t, maxIter, agent2Count)
}

func TestCycleAgent_Run_WithEscalation(t *testing.T) {
	// Track execution counts.
	agent1Count := 0
	agent2Count := 0

	// Create mock sub-agents - agent2 will trigger escalation.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, executionCount: &agent1Count}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, shouldTriggerError: true, executionCount: &agent2Count}

	// Create CycleAgent with high max iterations (should stop due to escalation).
	maxIter := 10
	cycleAgent := newFromLegacy(legacyOptions{
		Name:          "test-cycle",
		SubAgents:     []agent.Agent{subAgent1, subAgent2},
		MaxIterations: &maxIter,
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-002",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have events from both agents in first iteration (escalation stops loop).
	require.Equal(t, 2, len(events))

	// Should run only 1 iteration due to escalation.
	require.Equal(t, 1, agent1Count)
	require.Equal(t, 1, agent2Count)

	// Last event should be error event.
	lastEvent := events[len(events)-1]
	require.NotNil(t, lastEvent.Error)
}

func TestCycleAgent_ErrorEventStopsFollowingAgents(t *testing.T) {
	agent1Count := 0
	agent2Count := 0

	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, shouldTriggerError: true, executionCount: &agent1Count}
	subAgent2 := &mockAgent{name: "agent-2", eventCount: 1, executionCount: &agent2Count}

	cycleAgent := newFromLegacy(legacyOptions{
		Name:      "test-cycle",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "inv-error-event",
	}

	ch, err := cycleAgent.Run(ctx, invocation)
	require.NoError(t, err)

	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}

	require.Equal(t, 1, agent1Count)
	require.Equal(t, 0, agent2Count)
	require.NotEmpty(t, events)
	require.NotNil(t, events[len(events)-1].Error)
}

func TestCycleAgent_Run_NoMaxIterations(t *testing.T) {
	// Track execution counts.
	agent1Count := 0
	agent2Count := 0

	// Create mock sub-agents - agent2 will trigger escalation after 3 iterations.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1, executionCount: &agent1Count}

	// Custom mock that escalates after 3 executions.
	subAgent2 := &conditionalMockAgent{
		name:           "agent-2",
		eventCount:     1,
		executionCount: &agent2Count,
		triggerAfter:   3,
		trackCount:     &agent1Count, // Track agent1 count to decide when to escalate.
	}

	// Create CycleAgent without max iterations.
	cycleAgent := newFromLegacy(legacyOptions{
		Name:      "test-cycle",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	// Create invocation.
	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-003",
	}

	// Run the agent.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should run until escalation (3 complete iterations).
	// Each iteration has 2 events (agent1 + agent2).
	// On 3rd iteration, agent2 will escalate instead of normal event.
	triggerAfter := 3
	expectedEvents := triggerAfter * 2 // 3 iterations * 2 agents = 6 events total.
	require.Equal(t, expectedEvents, len(events))

	// agent1 should run triggerAfter times.
	require.Equal(t, triggerAfter, agent1Count)
}

// conditionalMockAgent escalates when a condition is met.
type conditionalMockAgent struct {
	name           string
	eventCount     int
	executionCount *int
	triggerAfter   int
	trackCount     *int // External counter to track
}

func (m *conditionalMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Conditional mock agent for testing",
	}
}

// SubAgents implements the agent.Agent interface for testing.
func (m *conditionalMockAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements the agent.Agent interface for testing.
func (m *conditionalMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

func (m *conditionalMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 10)

	go func() {
		defer close(eventChan)

		// Track execution count.
		if m.executionCount != nil {
			*m.executionCount++
		}

		// Check if we should escalate.
		shouldEscalate := m.trackCount != nil && *m.trackCount >= m.triggerAfter

		for i := 0; i < m.eventCount; i++ {
			var evt *event.Event

			// Generate error event if we should escalate.
			if shouldEscalate && i == m.eventCount-1 {
				evt = event.NewErrorEvent(invocation.InvocationID, m.name, model.ErrorTypeFlowError, "escalation")
			} else {
				evt = event.New(invocation.InvocationID, m.name)
				evt.Object = "test.completion"
				evt.Done = i == m.eventCount-1
			}

			select {
			case eventChan <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

func (m *conditionalMockAgent) Tools() []tool.Tool {
	return nil
}

func TestCycleAgent_Run_SubAgentError(t *testing.T) {
	// Create agent that returns error.
	subAgent1 := &mockAgent{name: "agent-1", eventCount: 1}
	subAgent2 := &mockAgent{name: "agent-2", shouldError: true}

	cycleAgent := newFromLegacy(legacyOptions{
		Name:      "test-cycle",
		SubAgents: []agent.Agent{subAgent1, subAgent2},
	})

	invocation := &agent.Invocation{
		AgentName:    "test-cycle",
		InvocationID: "test-004",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	eventChan, err := cycleAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("CycleAgent.Run() failed: %v", err)
	}

	var events []*event.Event
	for evt := range eventChan {
		events = append(events, evt)
	}

	// Should have event from agent1 + error event.
	require.Equal(t, 2, len(events))

	// Last event should be error.
	lastEvent := events[len(events)-1]
	require.NotNil(t, lastEvent.Error)
}

func TestCycleAgent_ShouldEscalate(t *testing.T) {
	cycleAgent := newFromLegacy(legacyOptions{Name: "test"})

	// Test nil event.
	require.False(t, cycleAgent.shouldEscalate(nil))

	// Test normal event.
	normalEvent := event.New("test", "test")
	require.False(t, cycleAgent.shouldEscalate(normalEvent))

	// Test error event.
	errorEvent := event.NewErrorEvent("test", "test", model.ErrorTypeFlowError, "test error")
	require.True(t, cycleAgent.shouldEscalate(errorEvent))

	// Test done error event.
	doneErrorEvent := event.New("test", "test")
	doneErrorEvent.Object = model.ObjectTypeError
	doneErrorEvent.Done = true
	require.True(t, cycleAgent.shouldEscalate(doneErrorEvent))
}

func TestCycleAgent_ChannelBufferSize(t *testing.T) {
	// Test default.
	agent1 := newFromLegacy(legacyOptions{Name: "test1"})
	require.Equal(t, defaultChannelBufferSize, agent1.channelBufferSize)

	// Test custom.
	agent2 := newFromLegacy(legacyOptions{Name: "test2", ChannelBufferSize: 100})
	require.Equal(t, 100, agent2.channelBufferSize)
}

func TestCycleAgent_WithCallbacks(t *testing.T) {
	// Create agent callbacks.
	callbacks := agent.NewCallbacks()

	// Test before agent callback that skips execution.
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		if invocation.Message.Content == "skip" {
			return nil, nil
		}
		return nil, nil
	})

	// Create cycle agent with callbacks.
	maxIterations := 3
	cycleAgent := newFromLegacy(legacyOptions{
		Name:           "test-cycle-agent",
		SubAgents:      []agent.Agent{&mockAgent{name: "agent1"}, &mockAgent{name: "agent2"}},
		MaxIterations:  &maxIterations,
		AgentCallbacks: callbacks,
	})

	// Test skip execution.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-skip",
		AgentName:    "test-cycle-agent",
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "skip",
		},
	}

	ctx := context.Background()
	eventChan, err := cycleAgent.Run(ctx, invocation)
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

// noopAgent returns no events.
type noopAgent struct{ name string }

func (n *noopAgent) Info() agent.Info                { return agent.Info{Name: n.name} }
func (n *noopAgent) SubAgents() []agent.Agent        { return nil }
func (n *noopAgent) FindSubAgent(string) agent.Agent { return nil }
func (n *noopAgent) Tools() []tool.Tool              { return nil }
func (n *noopAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	close(ch)
	return ch, nil
}

func TestCycleAgent_BeforeCallbackResp(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{Object: "custom", Done: true}, nil
	})

	ca := newFromLegacy(legacyOptions{Name: "loop", SubAgents: []agent.Agent{&noopAgent{"a"}}, AgentCallbacks: cb})

	events, err := ca.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "loop"})
	require.NoError(t, err)

	cnt := 0
	for e := range events {
		cnt++
		require.Equal(t, "custom", e.Object)
	}
	require.Equal(t, 1, cnt)
}

func TestCycleAgent_BeforeCallbackError(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("boom")
	})

	ca := newFromLegacy(legacyOptions{Name: "loop", SubAgents: []agent.Agent{&noopAgent{"a"}}, AgentCallbacks: cb})

	events, err := ca.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "loop"})
	require.NoError(t, err)

	cnt := 0
	for e := range events {
		cnt++
		require.NotNil(t, e.Error)
		require.Equal(t, agent.ErrorTypeAgentCallbackError, e.Error.Type)
	}
	require.Equal(t, 1, cnt)
}

func TestCycleAgent_SubAgentErrProp(t *testing.T) {
	errAgent := &errorAgent{name: "bad"}
	ca := newFromLegacy(legacyOptions{Name: "loop", SubAgents: []agent.Agent{errAgent}})

	events, err := ca.Run(context.Background(), &agent.Invocation{InvocationID: "id", AgentName: "loop"})
	require.NoError(t, err)

	cnt := 0
	for e := range events {
		cnt++
		require.NotNil(t, e.Error)
		require.Equal(t, model.ErrorTypeFlowError, e.Error.Type)
	}
	require.Equal(t, 1, cnt)
}

type errorAgent struct{ name string }

func (e *errorAgent) Info() agent.Info                { return agent.Info{Name: e.name} }
func (e *errorAgent) SubAgents() []agent.Agent        { return nil }
func (e *errorAgent) FindSubAgent(string) agent.Agent { return nil }
func (e *errorAgent) Tools() []tool.Tool              { return nil }
func (e *errorAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, errors.New("failed")
}

func TestCycleAgent_CreateSubAgentInvoke(t *testing.T) {
	parent := newFromLegacy(legacyOptions{Name: "parent"})
	base := agent.NewInvocation(agent.WithInvocationAgent(parent))
	child := &noopAgent{name: "child"}

	inv := parent.createSubAgentInvocation(child, base)

	require.Equal(t, "child", inv.AgentName)
	// Branch should stay unchanged when base.Branch non-empty.
	require.Equal(t, "parent"+agent.BranchDelimiter+"child", inv.Branch)
	// Ensure TransferInfo cleared.
	require.Nil(t, inv.TransferInfo)
}

func TestCycleAgent_AfterCallback(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return &model.Response{Object: "after", Done: true}, nil
	})

	one := 1
	ca := newFromLegacy(legacyOptions{Name: "loop", SubAgents: []agent.Agent{&noopAgent{"a"}}, AgentCallbacks: cb, MaxIterations: &one})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	events, err := ca.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "loop"})
	require.NoError(t, err)

	var last *event.Event
	for e := range events {
		last = e
	}
	require.NotNil(t, last)
	require.Equal(t, "after", last.Object)
}

// simpleAgent emits exactly one event with the supplied configuration.
type simpleAgent struct {
	name      string
	object    string
	done      bool
	withError bool
	content   string
}

func (s *simpleAgent) Info() agent.Info                { return agent.Info{Name: s.name} }
func (s *simpleAgent) SubAgents() []agent.Agent        { return nil }
func (s *simpleAgent) FindSubAgent(string) agent.Agent { return nil }
func (s *simpleAgent) Tools() []tool.Tool              { return nil }

func (s *simpleAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		e := event.New(inv.InvocationID, s.name)
		e.Object = s.object
		e.Done = s.done
		e.Choices = []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: s.content},
		}}
		if s.withError {
			e.Error = &model.ResponseError{Type: model.ErrorTypeAPIError, Message: "fail"}
		}
		ch <- e
	}()
	return ch, nil
}

func TestCycleAgent_ShouldEscalateDef(t *testing.T) {
	ca := newFromLegacy(legacyOptions{Name: "loop"})

	// Error event should escalate.
	errEvt := event.New("id", "loop")
	errEvt.Response.Error = &model.ResponseError{Type: model.ErrorTypeAPIError, Message: "x"}
	errEvt.Object = model.ObjectTypeError
	errEvt.Done = true
	require.True(t, ca.shouldEscalate(errEvt))

	// Non-error, done=false should not escalate.
	normal := event.New("id2", "loop")
	require.False(t, ca.shouldEscalate(normal))
}

func TestCycleAgent_CustomEscalationFunc(t *testing.T) {
	// Custom escalation when content contains "STOP".
	escalate := func(evt *event.Event) bool {
		if len(evt.Choices) > 0 && evt.Choices[0].Message.Content == "STOP" {
			return true
		}
		return false
	}

	agent1 := &simpleAgent{name: "worker", content: "RUN", object: model.ObjectTypeToolResponse, done: true}
	stopAgent := &simpleAgent{name: "stopper", content: "STOP", object: model.ObjectTypeToolResponse, done: true}

	ca := newFromLegacy(legacyOptions{
		Name:           "loop",
		SubAgents:      []agent.Agent{agent1, stopAgent},
		EscalationFunc: escalate,
		MaxIterations:  ptrInt(5),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := ca.Run(ctx, &agent.Invocation{InvocationID: "inv", AgentName: "loop"})
	require.NoError(t, err)

	cnt := 0
	for range events {
		cnt++
	}
	// Expected two events: one from worker, one from stopper, then escalation.
	require.Equal(t, 2, cnt)
}

func TestCycleAgent_MaxIterations(t *testing.T) {
	max := 3
	worker := &simpleAgent{name: "w", content: "tick"}
	ca := newFromLegacy(legacyOptions{
		Name:          "loop",
		SubAgents:     []agent.Agent{worker},
		MaxIterations: &max,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := ca.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "loop"})
	require.NoError(t, err)

	cnt := 0
	for range events {
		cnt++
	}
	// One event per iteration.
	require.Equal(t, max, cnt)
}

func TestCycleAgent_AfterCallbackError(t *testing.T) {
	cb := agent.NewCallbacks()
	cb.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return nil, errors.New("after failed")
	})

	one := 1
	ca := newFromLegacy(legacyOptions{
		Name:           "loop",
		SubAgents:      []agent.Agent{&noopAgent{"a"}},
		AgentCallbacks: cb,
		MaxIterations:  &one,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	events, err := ca.Run(ctx, &agent.Invocation{InvocationID: "id", AgentName: "loop"})
	require.NoError(t, err)

	var last *event.Event
	for e := range events {
		last = e
	}
	require.NotNil(t, last)
	require.NotNil(t, last.Error)
	require.Equal(t, agent.ErrorTypeAgentCallbackError, last.Error.Type)
}

func TestCycleAgent_SubAgentsAndInfo(t *testing.T) {
	sub1 := &noopAgent{name: "sub1"}
	sub2 := &noopAgent{name: "sub2"}

	ca := newFromLegacy(legacyOptions{
		Name:      "loop",
		SubAgents: []agent.Agent{sub1, sub2},
	})

	// Test SubAgents.
	subs := ca.SubAgents()
	require.Len(t, subs, 2)

	// Test FindSubAgent.
	found := ca.FindSubAgent("sub1")
	require.NotNil(t, found)
	require.Equal(t, "sub1", found.Info().Name)

	notFound := ca.FindSubAgent("missing")
	require.Nil(t, notFound)

	// Test Info.
	info := ca.Info()
	require.Equal(t, "loop", info.Name)

	// Test Tools.
	tools := ca.Tools()
	require.Empty(t, tools)
}

// TestCycleAgent_CallbackContextPropagation tests that context values set in
// BeforeAgent callback can be retrieved in AfterAgent callback.
func TestCycleAgent_CallbackContextPropagation(t *testing.T) {
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

	// Create cycle agent with callbacks.
	subAgent := &noopAgent{name: "child"}
	cycleAgent := newFromLegacy(legacyOptions{
		Name:           "test-cycle",
		SubAgents:      []agent.Agent{subAgent},
		AgentCallbacks: callbacks,
		MaxIterations:  ptrInt(1), // Run only one iteration.
	})

	// Run the agent.
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-cycle",
	}

	events, err := cycleAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Consume all events to ensure callbacks are executed.
	for range events {
	}

	// Verify that the context value was captured in AfterAgent callback.
	require.Equal(t, testValue, capturedValue, "context value should be propagated from BeforeAgent to AfterAgent")
}

func ptrInt(i int) *int { return &i }
