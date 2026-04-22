//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package errormessage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/errormessage"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stopAgent emits a single stop_agent_error event and closes, matching the
// shape llmflow produces when StopError propagates out of a tool or callback.
type stopAgent struct {
	name string
	msg  string
}

func (a *stopAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *stopAgent) Tools() []tool.Tool              { return nil }
func (a *stopAgent) SubAgents() []agent.Agent        { return nil }
func (a *stopAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *stopAgent) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- event.NewErrorEvent(
		inv.InvocationID,
		a.name,
		agent.ErrorTypeStopAgentError,
		a.msg,
	)
	close(ch)
	return ch, nil
}

// TestPlugin_RunnerIntegration_RewritesPersistedErrorEventContent exercises
// the full runner event pipeline. It verifies that, for events emitted on
// the runner's normal event channel (the path llmflow uses for
// stop_agent_error and for any agent that returns event.NewErrorEvent),
// the plugin's resolver output ends up in the session, replacing the
// built-in generic fallback message.
func TestPlugin_RunnerIntegration_RewritesPersistedErrorEventContent(
	t *testing.T,
) {
	const (
		appName     = "error-message-test"
		userID      = "user"
		sessionID   = "session"
		customReply = "custom stop message"
		stopReason  = "max iterations reached"
	)
	svc := sessioninmemory.NewSessionService()

	rewriter := errormessage.New(
		errormessage.WithResolver(func(
			_ context.Context,
			_ *agent.Invocation,
			e *event.Event,
		) (string, bool) {
			if e == nil || e.Response == nil || e.Response.Error == nil {
				return "", false
			}
			if e.Response.Error.Type != agent.ErrorTypeStopAgentError {
				return "", false
			}
			return customReply, true
		}),
	)

	r := runner.NewRunner(
		appName,
		&stopAgent{name: "stop-agent", msg: stopReason},
		runner.WithSessionService(svc),
		runner.WithPlugins(rewriter),
	)
	defer r.Close()

	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("trigger"),
	)
	require.NoError(t, err)
	for range ch {
	}

	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	var errorEvent *event.Event
	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.Response != nil && evt.Response.Error != nil {
			errorEvent = &sess.Events[i]
			break
		}
	}
	require.NotNil(t, errorEvent, "expected an error event in session")

	// Resolver output is persisted as the assistant-visible content.
	require.Len(t, errorEvent.Response.Choices, 1)
	require.Equal(
		t,
		model.RoleAssistant,
		errorEvent.Response.Choices[0].Message.Role,
	)
	require.Equal(
		t,
		customReply,
		errorEvent.Response.Choices[0].Message.Content,
	)

	// Structured Response.Error is left intact for debugging consumers.
	require.Equal(
		t,
		agent.ErrorTypeStopAgentError,
		errorEvent.Response.Error.Type,
	)
	require.Equal(t, stopReason, errorEvent.Response.Error.Message)
}

// TestPlugin_RunnerIntegration_WithoutPluginUsesDefaultFallback pins the
// framework's built-in fallback so regressions in runner behaviour are
// caught alongside plugin changes.
func TestPlugin_RunnerIntegration_WithoutPluginUsesDefaultFallback(
	t *testing.T,
) {
	const (
		appName   = "error-message-test-default"
		userID    = "user"
		sessionID = "session"
	)
	svc := sessioninmemory.NewSessionService()

	r := runner.NewRunner(
		appName,
		&stopAgent{name: "stop-agent", msg: "max iterations reached"},
		runner.WithSessionService(svc),
	)
	defer r.Close()

	ch, err := r.Run(
		context.Background(),
		userID,
		sessionID,
		model.NewUserMessage("trigger"),
	)
	require.NoError(t, err)
	for range ch {
	}

	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	var errorEvent *event.Event
	for i := range sess.Events {
		evt := sess.Events[i]
		if evt.Response != nil && evt.Response.Error != nil {
			errorEvent = &sess.Events[i]
			break
		}
	}
	require.NotNil(t, errorEvent)
	require.Len(t, errorEvent.Response.Choices, 1)
	require.Equal(
		t,
		"An error occurred during execution. Please contact the service provider.",
		errorEvent.Response.Choices[0].Message.Content,
	)
}
