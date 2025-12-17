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
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
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

type completionWaitAgent struct {
	name string
}

func (m *completionWaitAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)

		barrier := event.New(inv.InvocationID, m.name)
		barrier.RequiresCompletion = true
		completionID := agent.GetAppendEventNoticeKey(barrier.ID)
		_ = inv.AddNoticeChannel(ctx, completionID)
		_ = agent.EmitEvent(ctx, inv, ch, barrier)

		if err := inv.AddNoticeChannelAndWait(ctx, completionID, 500*time.Millisecond); err != nil {
			errEvt := event.NewErrorEvent(inv.InvocationID, m.name, model.ErrorTypeFlowError, err.Error())
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}

		done := event.NewResponseEvent(inv.InvocationID, m.name, &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}},
		})
		_ = agent.EmitEvent(ctx, inv, ch, done)
	}()
	return ch, nil
}

func (m *completionWaitAgent) Tools() []tool.Tool { return nil }
func (m *completionWaitAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "wait completion"}
}
func (m *completionWaitAgent) SubAgents() []agent.Agent        { return nil }
func (m *completionWaitAgent) FindSubAgent(string) agent.Agent { return nil }

type sessionMirrorAgent struct {
	name string
	inv  string
}

const (
	graphCompletionMsg   = "graph-done"
	graphCompletionAgent = "graph-completion"
	graphStateKey        = "graph_key"
	graphStateValue      = "graph_value"
)

type graphCompletionMockAgent struct {
	name string
}

func (m *graphCompletionMockAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)

		evt := event.NewResponseEvent(
			inv.InvocationID,
			m.name,
			&model.Response{
				Object: graph.ObjectTypeGraphExecution,
				Done:   true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage(
						graphCompletionMsg,
					),
				}},
			},
		)
		evt.StateDelta = map[string][]byte{
			graphStateKey: []byte(graphStateValue),
		}
		_ = agent.EmitEvent(ctx, inv, ch, evt)
	}()
	return ch, nil
}

func (m *graphCompletionMockAgent) Tools() []tool.Tool { return nil }
func (m *graphCompletionMockAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "graph completion"}
}
func (m *graphCompletionMockAgent) SubAgents() []agent.Agent {
	return nil
}
func (m *graphCompletionMockAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (m *sessionMirrorAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	m.inv = inv.InvocationID
	ch := make(chan *event.Event, 3)
	go func() {
		defer close(ch)

		const toolID = "tool-call-1"
		toolResult := event.NewResponseEvent(
			inv.InvocationID,
			m.name,
			&model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  toolID,
						Content: "ok",
					},
				}},
			},
		)
		_ = agent.EmitEvent(ctx, inv, ch, toolResult)

		barrier := event.New(inv.InvocationID, m.name)
		barrier.RequiresCompletion = true
		completionID := agent.GetAppendEventNoticeKey(barrier.ID)
		_ = inv.AddNoticeChannel(ctx, completionID)
		_ = agent.EmitEvent(ctx, inv, ch, barrier)

		if err := inv.AddNoticeChannelAndWait(
			ctx, completionID, 500*time.Millisecond,
		); err != nil {
			errEvt := event.NewErrorEvent(
				inv.InvocationID,
				m.name,
				model.ErrorTypeFlowError,
				err.Error(),
			)
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}

		if !sessionHasToolResult(inv.Session, inv.InvocationID, toolID) {
			errEvt := event.NewErrorEvent(
				inv.InvocationID,
				m.name,
				model.ErrorTypeFlowError,
				"tool result not mirrored to session",
			)
			_ = agent.EmitEvent(ctx, inv, ch, errEvt)
			return
		}

		done := event.NewResponseEvent(inv.InvocationID, m.name, &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}},
		})
		_ = agent.EmitEvent(ctx, inv, ch, done)
	}()
	return ch, nil
}

func (m *sessionMirrorAgent) Tools() []tool.Tool { return nil }
func (m *sessionMirrorAgent) Info() agent.Info {
	return agent.Info{Name: m.name, Description: "mirror session"}
}
func (m *sessionMirrorAgent) SubAgents() []agent.Agent        { return nil }
func (m *sessionMirrorAgent) FindSubAgent(string) agent.Agent { return nil }

func sessionHasToolResult(
	sess *session.Session,
	invocationID string,
	toolID string,
) bool {
	if sess == nil {
		return false
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.InvocationID != invocationID || evt.Response == nil {
			continue
		}
		if !evt.Response.IsToolResultResponse() {
			continue
		}
		for _, id := range evt.Response.GetToolResultIDs() {
			if id == toolID {
				return true
			}
		}
	}
	return false
}

type filterKeyAgent struct {
	name string
	seen string
}

func (m *filterKeyAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	m.seen = inv.GetEventFilterKey()
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage(m.seen)}}}}
	close(ch)
	return ch, nil
}
func (m *filterKeyAgent) Tools() []tool.Tool              { return nil }
func (m *filterKeyAgent) Info() agent.Info                { return agent.Info{Name: m.name, Description: "fk"} }
func (m *filterKeyAgent) SubAgents() []agent.Agent        { return nil }
func (m *filterKeyAgent) FindSubAgent(string) agent.Agent { return nil }

func TestTool_Call_MirrorsChildEventsToSession(t *testing.T) {
	sa := &sessionMirrorAgent{name: "session-mirror"}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "done", got)
	require.NotEmpty(t, sa.inv)
	require.True(t, sessionHasToolResult(sess, sa.inv, "tool-call-1"))
}

func TestTool_Call_UsesSessionAppender(t *testing.T) {
	sa := &sessionMirrorAgent{name: "session-mirror"}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)

	var appendCount int
	appender.Attach(parent, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		appendCount++
		sess.UpdateUserSession(evt)
		return nil
	})

	ctx := agent.NewInvocationContext(context.Background(), parent)
	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "done", got)
	require.NotEmpty(t, sa.inv)
	require.Greater(t, appendCount, 0)
	require.True(t, sessionHasToolResult(sess, sa.inv, "tool-call-1"))
}

func TestTool_Call_AppenderError_NoDuplicateEvents(t *testing.T) {
	sa := &sessionMirrorAgent{name: "session-mirror"}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	appender.Attach(parent, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		sess.UpdateUserSession(evt)
		return errors.New("append failed")
	})

	ctx := agent.NewInvocationContext(context.Background(), parent)
	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "done", got)
	require.NotEmpty(t, sa.inv)

	const toolID = "tool-call-1"
	require.Equal(t, 1, countToolResultEvents(sess, sa.inv, toolID))
	require.Equal(t, 3, sess.GetEventCount())
}

func TestTool_Call_GraphCompletion_StripsChoicesForPersistence(t *testing.T) {
	sa := &graphCompletionMockAgent{name: graphCompletionAgent}
	at := NewTool(sa)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)

	var persistedGraph *event.Event
	appender.Attach(parent, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		if evt != nil && evt.Done &&
			evt.Object == graph.ObjectTypeGraphExecution {
			copyEvt := *evt
			if evt.Response != nil {
				copyEvt.Response = evt.Response.Clone()
			}
			persistedGraph = &copyEvt
		}
		sess.UpdateUserSession(evt)
		return nil
	})

	ctx := agent.NewInvocationContext(context.Background(), parent)
	got, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, graphCompletionMsg, got)

	require.NotNil(t, persistedGraph)
	require.NotNil(t, persistedGraph.Response)
	require.Len(t, persistedGraph.Response.Choices, 0)
	require.Contains(t, persistedGraph.StateDelta, graphStateKey)
	require.Equal(
		t,
		[]byte(graphStateValue),
		persistedGraph.StateDelta[graphStateKey],
	)
	require.Contains(t, sess.State, graphStateKey)
	require.Equal(t, []byte(graphStateValue), sess.State[graphStateKey])
}

func TestTool_shouldMirrorEventToSession_Cases(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		require.False(t, shouldMirrorEventToSession(nil))
	})

	t.Run("state delta", func(t *testing.T) {
		evt := event.New("inv", "author")
		evt.StateDelta = map[string][]byte{"k": []byte("v")}
		require.True(t, shouldMirrorEventToSession(evt))
	})

	t.Run("no response", func(t *testing.T) {
		evt := &event.Event{}
		require.False(t, shouldMirrorEventToSession(evt))
	})

	t.Run("partial response", func(t *testing.T) {
		evt := event.NewResponseEvent("inv", "author", &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Content: "x"},
			}},
		})
		require.False(t, shouldMirrorEventToSession(evt))
	})

	t.Run("invalid content", func(t *testing.T) {
		evt := event.NewResponseEvent("inv", "author", &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(""),
			}},
		})
		require.False(t, shouldMirrorEventToSession(evt))
	})
}

func TestTool_sessionHasEventID_Cases(t *testing.T) {
	require.False(t, sessionHasEventID(nil, "id"))

	inv := agent.NewInvocation()
	require.False(t, sessionHasEventID(inv, "id"))
	require.False(t, sessionHasEventID(inv, ""))

	sess := session.NewSession("app", "user", "session")
	inv.Session = sess

	const (
		existsID  = "exists"
		missingID = "missing"
	)
	evt := event.NewResponseEvent(inv.InvocationID, "a", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewUserMessage("seed"),
		}},
	})
	evt.ID = existsID
	sess.UpdateUserSession(evt)

	require.True(t, sessionHasEventID(inv, existsID))
	require.False(t, sessionHasEventID(inv, missingID))
}

func TestTool_appendEvent_AppenderError_FallbackUpdatesSession(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))

	const appendErrMsg = "append failed"
	appender.Attach(inv, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		return errors.New(appendErrMsg)
	})

	evt := event.NewResponseEvent(inv.InvocationID, "a", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	})

	sess.UpdateUserSession(event.NewResponseEvent(
		inv.InvocationID,
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("seed"),
			}},
		},
	))
	at.appendEvent(context.Background(), inv, evt)

	require.Equal(t, 2, sess.GetEventCount())
}

func TestTool_appendEvent_AppenderError_EmptyIDUpdatesSession(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))

	appender.Attach(inv, func(
		ctx context.Context,
		evt *event.Event,
	) error {
		return errors.New("append failed")
	})

	evt := event.NewResponseEvent(inv.InvocationID, "a", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	})
	evt.ID = ""

	sess.UpdateUserSession(event.NewResponseEvent(
		inv.InvocationID,
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("seed"),
			}},
		},
	))
	at.appendEvent(context.Background(), inv, evt)

	require.Equal(t, 2, sess.GetEventCount())
}

func TestTool_ensureUserMessageForCall_EarlyReturns(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")

	t.Run("non user role", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationMessage(model.NewAssistantMessage("x")),
		)
		at.ensureUserMessageForCall(context.Background(), inv)
		require.Equal(t, 0, sess.GetEventCount())
	})

	t.Run("empty content", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationMessage(model.NewUserMessage("")),
		)
		at.ensureUserMessageForCall(context.Background(), inv)
		require.Equal(t, 0, sess.GetEventCount())
	})
}

func TestTool_ensureUserMessageForCall_SkipsWhenUserExists(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
	)

	userEvt := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewUserMessage("seed"),
		}},
	})
	sess.UpdateUserSession(userEvt)

	at.ensureUserMessageForCall(context.Background(), inv)
	require.Equal(t, 1, sess.GetEventCount())
}

func TestTool_ensureUserMessageForCall_NilCases(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	at.ensureUserMessageForCall(context.Background(), nil)

	inv := agent.NewInvocation()
	at.ensureUserMessageForCall(context.Background(), inv)
}

func TestTool_wrapWithCompletion_NotifyCompletionError(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})

	src := make(chan *event.Event, 1)
	evt := event.New("inv", "author")
	evt.RequiresCompletion = true
	src <- evt
	close(src)

	badInv := &agent.Invocation{}
	out := at.wrapWithCompletion(context.Background(), badInv, src)
	got, ok := <-out
	require.True(t, ok)
	require.Same(t, evt, got)

	_, ok = <-out
	require.False(t, ok)
}

func TestTool_wrapWithCallSemantics_NilInvocationReturnsSrc(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})

	src := make(chan *event.Event, 1)
	src <- event.New("inv", "author")
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), nil, src)
	require.Equal(
		t,
		reflect.ValueOf(src).Pointer(),
		reflect.ValueOf(out).Pointer(),
	)
}

func TestTool_wrapWithCallSemantics_NotifyCompletionError(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})

	sess := session.NewSession("app", "user", "session")
	badInv := &agent.Invocation{
		Session: sess,
		Message: model.NewAssistantMessage("x"),
	}

	src := make(chan *event.Event, 1)
	evt := event.New("inv", "author")
	evt.RequiresCompletion = true
	src <- evt
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), badInv, src)
	_, ok := <-out
	require.True(t, ok)
	_, ok = <-out
	require.False(t, ok)
}

func TestTool_wrapWithCallSemantics_ForwardsNilEvents(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	sess := session.NewSession("app", "user", "session")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewAssistantMessage("x")),
	)

	src := make(chan *event.Event, 1)
	src <- nil
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), inv, src)
	got, ok := <-out
	require.True(t, ok)
	require.Nil(t, got)
	_, ok = <-out
	require.False(t, ok)
}

func TestTool_wrapWithCallSemantics_SessionNilUsesCompletion(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	inv := agent.NewInvocation()

	src := make(chan *event.Event, 1)
	evt := event.New(inv.InvocationID, "author")
	evt.RequiresCompletion = true
	completionID := agent.GetAppendEventNoticeKey(evt.ID)
	require.NotNil(t, inv.AddNoticeChannel(context.Background(), completionID))
	src <- evt
	close(src)

	out := at.wrapWithCallSemantics(context.Background(), inv, src)
	_, ok := <-out
	require.True(t, ok)
	_, ok = <-out
	require.False(t, ok)

	require.NoError(t, inv.AddNoticeChannelAndWait(
		context.Background(), completionID, time.Second,
	))
}

func TestTool_isGraphCompletionEvent_NilCases(t *testing.T) {
	require.False(t, isGraphCompletionEvent(nil))
	require.False(t, isGraphCompletionEvent(&event.Event{}))
}

func TestTool_appendEvent_NilCases(t *testing.T) {
	at := NewTool(&mockAgent{name: "x", description: "x"})
	at.appendEvent(context.Background(), nil, nil)

	inv := agent.NewInvocation()
	at.appendEvent(context.Background(), inv, nil)
	at.appendEvent(context.Background(), inv, event.New("inv", "author"))
}

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
	for i := 0; i < 3; i++ { // Now expecting 4 events: tool input + original 3 events
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
	if got[0] != "hello" || got[1] != " world" || got[2] != "ignored full" {
		t.Fatalf("unexpected forwarded contents: %#v", got)
	}

	// Assert the sub agent saw a filter key starting with its own name (now includes UUID suffix).
	expectedPrefix := sa.name + "-"
	if !strings.HasPrefix(sa.seenFilterKey, expectedPrefix) {
		t.Fatalf("expected sub agent filter key to start with %q, got %q", expectedPrefix, sa.seenFilterKey)
	}
}

func countToolResultEvents(
	sess *session.Session,
	invocationID string,
	toolID string,
) int {
	if sess == nil {
		return 0
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	var count int
	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.InvocationID != invocationID || evt.Response == nil {
			continue
		}
		if !evt.Response.IsToolResultResponse() {
			continue
		}
		for _, id := range evt.Response.GetToolResultIDs() {
			if id == toolID {
				count++
				break
			}
		}
	}
	return count
}

func TestTool_HistoryScope_ParentBranch_Streamable_FilterKeyPrefix(t *testing.T) {
	sa := &streamingMockAgent{name: "stream-agent"}
	at := NewTool(sa, WithStreamInner(true), WithHistoryScope(HistoryScopeParentBranch))

	// Parent invocation with base filter key.
	sess := session.NewSession("app", "user", "session")
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
	for i := 0; i < 3; i++ {
		if _, err := r.Recv(); err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}

	// Expect child filter key prefixed by parent key.
	if !strings.HasPrefix(sa.seenFilterKey, "parent-agent/"+sa.name+"-") {
		t.Fatalf("expected child filter key to start with %q, got %q", "parent-agent/"+sa.name+"-", sa.seenFilterKey)
	}
}

func TestTool_StreamableCall_FlushesParentSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	flushCh := make(chan *flush.FlushRequest, 1)
	flush.Attach(ctx, parent, flushCh)

	at := NewTool(&streamingMockAgent{name: "stream-agent"}, WithStreamInner(true))
	toolCtx := agent.NewInvocationContext(ctx, parent)

	acked := make(chan struct{}, 1)
	go func() {
		select {
		case req := <-flushCh:
			require.NotNil(t, req)
			require.NotNil(t, req.ACK)
			close(req.ACK)
			acked <- struct{}{}
		case <-ctx.Done():
		}
	}()

	reader, err := at.StreamableCall(toolCtx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	defer reader.Close()

	recvCount := 0
	for {
		_, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		recvCount++
	}
	require.Equal(t, 3, recvCount)

	select {
	case <-acked:
	default:
		t.Fatalf("expected flush request to be handled")
	}
}

func TestTool_StreamableCall_NotifiesCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	at := NewTool(&completionWaitAgent{name: "waiter"}, WithStreamInner(true))
	toolCtx := agent.NewInvocationContext(ctx, parent)

	reader, err := at.StreamableCall(toolCtx, []byte(`{"request":"payload"}`))
	require.NoError(t, err)
	defer reader.Close()

	var contents []string
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		ev, ok := chunk.Content.(*event.Event)
		require.True(t, ok)
		require.Nil(t, ev.Error)
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			msg := ev.Response.Choices[0].Message
			if msg.Content != "" {
				contents = append(contents, msg.Content)
			}
		}
	}

	require.Contains(t, contents, "done")
}

func TestTool_wrapWithCompletion_NilInvocation(t *testing.T) {
	at := NewTool(&mockAgent{name: "wrap", description: "wrap"})
	src := make(chan *event.Event, 1)
	src <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}}}
	close(src)

	out := at.wrapWithCompletion(context.Background(), nil, src)
	require.Equal(t, reflect.ValueOf(src).Pointer(), reflect.ValueOf(out).Pointer())

	evt, ok := <-out
	require.True(t, ok)
	require.NotNil(t, evt)

	_, ok = <-out
	require.False(t, ok)
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

	// Build parent session with a prior user event under parent branch so that
	// session filtering preserves it when seeding the snapshot.
	sess := session.NewSession("parent-app", "parent-user", "parent-session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent-branch"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Append a parent user event (content "PARENT") so that snapshot/session
	// filtering retains it as part of history.
	parentEvt := event.NewResponseEvent(parent.InvocationID, "parent", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage("PARENT")}},
	})
	agent.InjectIntoEvent(parent, parentEvt)
	sess.Events = append(sess.Events, *parentEvt)

	// Call the tool with child input.
	out, err := at.Call(ctx, []byte(`{"request":"CHILD"}`))
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	s, _ := out.(string)
	// Expect both parent content and tool input to be visible via filter inheritance.
	if !strings.Contains(s, "PARENT") || strings.Contains(s, `{"request":"CHILD"}`) {
		t.Fatalf("expected output to contain parent content (not raw child request), got: %q", s)
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
	for i := 0; i < 3; i++ { // drain
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

	sess := &session.Session{
		ID:     "s",
		UserID: "u",
	}
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

func TestTool_callWithParentInvocation_NoSessionFallback(t *testing.T) {
	at := NewTool(&mockAgent{name: "test", description: "test"})
	parent := agent.NewInvocation()

	res, err := at.callWithParentInvocation(context.Background(), parent, model.NewUserMessage("hi"))
	require.NoError(t, err)
	require.Equal(t, "Hello from mock agent!", res)
}

func TestTool_Call_WithParentInvocation_FlushError(t *testing.T) {
	at := NewTool(&mockAgent{name: "test-agent", description: "desc"})

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent"),
	)
	flush.Attach(context.Background(), parent, make(chan *flush.FlushRequest))

	baseCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := agent.NewInvocationContext(baseCtx, parent)

	result, err := at.Call(ctx, []byte(`{"request":"hello"}`))
	require.Error(t, err)
	require.Empty(t, result)
	require.Contains(t, err.Error(), "flush parent invocation session")
}

func TestTool_Call_WithParentInvocation_RunError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent"),
	)
	flushCh := make(chan *flush.FlushRequest, 1)
	flush.Attach(ctx, parent, flushCh)

	flushed := make(chan struct{}, 1)
	go func() {
		select {
		case req := <-flushCh:
			if req != nil && req.ACK != nil {
				close(req.ACK)
			}
			flushed <- struct{}{}
		case <-ctx.Done():
		}
	}()

	at := NewTool(&errorMockAgent{name: "err-agent"})
	_, err := at.Call(agent.NewInvocationContext(ctx, parent), []byte(`{"request":"x"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to run agent")

	select {
	case <-flushed:
	default:
		t.Fatalf("expected flush to be triggered")
	}
}

func TestTool_Call_WithParentInvocation_FlushesAndCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "user", "session")),
		agent.WithInvocationEventFilterKey("parent-agent"),
	)
	flushCh := make(chan *flush.FlushRequest, 1)
	flush.Attach(ctx, parent, flushCh)

	flushed := make(chan struct{}, 1)
	go func() {
		select {
		case req := <-flushCh:
			if req != nil && req.ACK != nil {
				close(req.ACK)
			}
			flushed <- struct{}{}
		case <-ctx.Done():
		}
	}()

	a := &filterKeyAgent{name: "child-agent"}
	at := NewTool(a, WithHistoryScope(HistoryScopeParentBranch))
	res, err := at.Call(agent.NewInvocationContext(ctx, parent), []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	resStr, ok := res.(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(resStr, "parent-agent/"+a.name+"-"))
	require.Equal(t, a.seen, resStr)

	select {
	case <-flushed:
	default:
		t.Fatalf("expected flush to be triggered")
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
