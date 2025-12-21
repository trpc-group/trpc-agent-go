// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// streamingErrorAgent emits an error event using event.NewErrorEvent (which has no content by default).
type streamingErrorAgent struct {
	name      string
	errorMsg  string
	errorType string
}

func (m *streamingErrorAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *streamingErrorAgent) SubAgents() []agent.Agent             { return nil }
func (m *streamingErrorAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *streamingErrorAgent) Tools() []tool.Tool                   { return nil }
func (m *streamingErrorAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	// Create an error event. By default, NewErrorEvent does NOT populate Choices/Content.
	// This simulates the behavior of llmflow/other components emitting raw error events.
	ev := event.NewErrorEvent(inv.InvocationID, m.name, m.errorType, m.errorMsg)
	ch <- ev
	close(ch)
	return ch, nil
}

// TestRunner_FixesStreamingErrorEvent verifies that the runner populates content for error events in the stream.
func TestRunner_FixesStreamingErrorEvent(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	errorMsg := "something went wrong in stream"
	ag := &streamingErrorAgent{
		name:      "stream-error-agent",
		errorMsg:  errorMsg,
		errorType: "stream_error",
	}

	r := NewRunner("test-app", ag, WithSessionService(svc))
	ctx := context.Background()
	userID := "user-1"
	sessionID := "session-1"

	// Run the agent.
	ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("start"))
	require.NoError(t, err)

	// Drain channel and collect events.
	var eventsFromCh []*event.Event
	for e := range ch {
		eventsFromCh = append(eventsFromCh, e)
	}

	// Expect Agent Error + Runner Completion (User message is not in output channel)
	// Wait, does user message go to output channel? No, usually not unless echoed?
	// The runner output channel contains events emitted by agent + runner completion.
	// So we expect 2 events here: Agent Error, Runner Completion.
	require.Len(t, eventsFromCh, 2, "Channel should output agent error and runner completion")
	assert.Equal(t, model.ObjectTypeError, eventsFromCh[0].Response.Object)
	assert.True(t, eventsFromCh[1].IsRunnerCompletion(), "Last event should be runner completion")

	// Verify session.
	sess, err := svc.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Events: User Msg, Agent Error Event.
	// Note: RunnerCompletion event has no content and is not persisted by session service.
	require.Len(t, sess.Events, 2)

	// Check the agent error event.
	errorEvent := sess.Events[1]
	assert.Equal(t, "stream-error-agent", errorEvent.Author)
	assert.Equal(t, model.ObjectTypeError, errorEvent.Response.Object)

	// Crucial assertion: Runner should have populated Choices and Content.
	require.NotEmpty(t, errorEvent.Response.Choices)
	assert.Equal(t, "An error occurred during execution. Please contact the service provider.",
		errorEvent.Response.Choices[0].Message.Content)
	assert.Equal(t, "error", *errorEvent.Response.Choices[0].FinishReason)
}

// TestRunner_FixesDirectRunError verifies that the runner populates content for errors returned by agent.Run().
func TestRunner_FixesDirectRunError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	runErrorMsg := "agent run failed directly"

	// failingAgent is defined in runner_test.go, but we can define a specific one here if needed
	// or rely on the one in the package. Let's define a specific one to be safe and clear.
	ag := &specificFailingAgent{name: "fail-agent", err: errors.New(runErrorMsg)}

	r := NewRunner("test-app", ag, WithSessionService(svc))
	ctx := context.Background()
	userID := "user-2"
	sessionID := "session-2"

	// Run the agent. Should return error.
	ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("start"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), runErrorMsg)
	assert.Nil(t, ch)

	// Verify session.
	sess, err := svc.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Events: User Msg, Agent Error Event (created by runner from error).
	// Note: No runner completion because it failed.
	require.Len(t, sess.Events, 2)

	// Check the error event.
	errorEvent := sess.Events[1]
	assert.Equal(t, "fail-agent", errorEvent.Author)
	assert.Equal(t, model.ObjectTypeError, errorEvent.Response.Object) // NewErrorEvent uses ObjectTypeError

	// Crucial assertion: Runner should have populated Choices and Content.
	require.NotEmpty(t, errorEvent.Response.Choices)
	assert.Equal(t, "An error occurred during execution. Please contact the service provider.",
		errorEvent.Response.Choices[0].Message.Content)
	assert.Equal(t, "error", *errorEvent.Response.Choices[0].FinishReason)
}

// streamingSuccessThenErrorAgent emits a normal response event followed by an error event.
type streamingSuccessThenErrorAgent struct {
	name      string
	content   string
	errorMsg  string
	errorType string
}

func (m *streamingSuccessThenErrorAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *streamingSuccessThenErrorAgent) SubAgents() []agent.Agent             { return nil }
func (m *streamingSuccessThenErrorAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *streamingSuccessThenErrorAgent) Tools() []tool.Tool                   { return nil }
func (m *streamingSuccessThenErrorAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)

	// 1. Emit a normal success response
	respEv := event.NewResponseEvent(inv.InvocationID, m.name, &model.Response{
		Done: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: m.content,
			},
		}},
	})
	ch <- respEv

	// 2. Emit an error event
	errEv := event.NewErrorEvent(inv.InvocationID, m.name, m.errorType, m.errorMsg)
	ch <- errEv

	close(ch)
	return ch, nil
}

// TestRunner_StreamingSuccessThenError verifies that the runner persists both the valid content and the subsequent error.
func TestRunner_StreamingSuccessThenError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	content := "Partial success"
	errorMsg := "Stream cut off"
	ag := &streamingSuccessThenErrorAgent{
		name:      "mixed-agent",
		content:   content,
		errorMsg:  errorMsg,
		errorType: "stream_error",
	}

	r := NewRunner("test-app", ag, WithSessionService(svc))
	ctx := context.Background()
	userID := "user-mixed"
	sessionID := "session-mixed"

	// Run the agent.
	ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("start"))
	require.NoError(t, err)

	// Drain channel.
	for range ch {
	}

	// Verify session.
	sess, err := svc.GetSession(ctx, session.Key{AppName: "test-app", UserID: userID, SessionID: sessionID})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Events: User Msg, Partial Success Event, Error Event.
	// Note: In real streaming, the partial events might be merged or stored individually depending on implementation.
	// Here we expect both to be appended because they are separate events sent to runner.
	require.Len(t, sess.Events, 3)

	// Check success event
	successEvent := sess.Events[1]
	assert.Equal(t, "mixed-agent", successEvent.Author)
	assert.Equal(t, content, successEvent.Response.Choices[0].Message.Content)

	// Check error event
	errorEvent := sess.Events[2]
	assert.Equal(t, "mixed-agent", errorEvent.Author)
	assert.Equal(t, model.ObjectTypeError, errorEvent.Response.Object)

	// Crucial assertion: Runner should have populated Choices and Content.
	require.NotEmpty(t, errorEvent.Response.Choices)
	assert.Equal(t, "An error occurred during execution. Please contact the service provider.",
		errorEvent.Response.Choices[0].Message.Content)
	assert.Equal(t, "error", *errorEvent.Response.Choices[0].FinishReason)
}

type specificFailingAgent struct {
	name string
	err  error
}

func (m *specificFailingAgent) Info() agent.Info                     { return agent.Info{Name: m.name} }
func (m *specificFailingAgent) SubAgents() []agent.Agent             { return nil }
func (m *specificFailingAgent) FindSubAgent(name string) agent.Agent { return nil }
func (m *specificFailingAgent) Tools() []tool.Tool                   { return nil }
func (m *specificFailingAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	return nil, m.err
}
