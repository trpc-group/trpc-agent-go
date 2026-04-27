//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/eventcontrol"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestProcessSingleAgentEventSkipsPersistenceAndCapturesOutputOnlyCompletion(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	rootKey := session.Key{AppName: "app", UserID: "user", SessionID: "root-session"}
	rootSession, err := service.CreateSession(ctx, rootKey, session.StateMap{})
	require.NoError(t, err)
	root := agent.NewInvocation(agent.WithInvocationID("root"))
	loop := &eventLoopContext{
		sess:             rootSession,
		invocation:       root,
		processedEventCh: make(chan *event.Event, 1),
	}
	childEvent := event.NewResponseEvent(
		"child-invocation",
		"child",
		&model.Response{
			ID:     "child-response",
			Done:   true,
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "child final answer",
				},
			}},
		},
	)
	eventcontrol.MarkSkipPersistence(root, childEvent)
	r := &runner{sessionService: service}
	require.True(t, r.shouldPersistEvent(childEvent))
	require.NoError(t, r.processSingleAgentEvent(ctx, loop, childEvent))
	require.Empty(t, loop.fallbackChoices)
	require.Equal(t, "child final answer", assistantChoicePrimaryContent(loop.outputOnlyCompletionChoices))
	require.False(t, loop.freshAssistantContentProduced)
	rootGot, err := service.GetSession(ctx, rootKey)
	require.NoError(t, err)
	require.Empty(t, rootGot.Events)
	emitted := <-loop.processedEventCh
	require.Same(t, childEvent, emitted)
	require.Nil(t, emitted.Extensions)
}

func TestProcessSingleAgentEventKeepsOutputOnlyAfterPluginReplacement(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	rootKey := session.Key{AppName: "app", UserID: "user", SessionID: "root-session"}
	rootSession, err := service.CreateSession(ctx, rootKey, session.StateMap{})
	require.NoError(t, err)
	replacementPlugin := &testPlugin{
		name: "replace-event",
		reg: func(r *plugin.Registry) {
			r.OnEvent(func(
				_ context.Context,
				_ *agent.Invocation,
				_ *event.Event,
			) (*event.Event, error) {
				return event.NewResponseEvent(
					"",
					"child",
					&model.Response{
						ID:   "replacement",
						Done: true,
						Choices: []model.Choice{{
							Message: model.NewAssistantMessage("redacted child answer"),
						}},
					},
				), nil
			})
		},
	}
	root := agent.NewInvocation(
		agent.WithInvocationID("root"),
		agent.WithInvocationPlugins(plugin.MustNewManager(replacementPlugin)),
	)
	loop := &eventLoopContext{
		sess:             rootSession,
		invocation:       root,
		processedEventCh: make(chan *event.Event, 1),
	}
	childEvent := event.NewResponseEvent(
		"child-invocation",
		"child",
		&model.Response{
			ID:   "child-response",
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("raw child answer"),
			}},
		},
	)
	eventcontrol.MarkSkipPersistence(root, childEvent)
	r := &runner{sessionService: service}
	require.NoError(t, r.processSingleAgentEvent(ctx, loop, childEvent))
	require.Equal(t, "redacted child answer", assistantChoicePrimaryContent(loop.outputOnlyCompletionChoices))
	rootGot, err := service.GetSession(ctx, rootKey)
	require.NoError(t, err)
	require.Empty(t, rootGot.Events)
	emitted := <-loop.processedEventCh
	require.Equal(t, "child-invocation", emitted.InvocationID)
	require.Equal(t, "redacted child answer", assistantChoicePrimaryContent(emitted.Response.Choices))
	require.Nil(t, emitted.Extensions)
}

func TestEmitRunnerCompletionEmitsOutputOnlyChoicesWithoutPersistingToRoot(t *testing.T) {
	ctx := context.Background()
	service := &mockSessionService{}
	rootSession := &session.Session{
		AppName: "app",
		UserID:  "user",
		ID:      "root-session",
	}
	root := agent.NewInvocation(
		agent.WithInvocationID("root"),
		agent.WithInvocationSession(rootSession),
		agent.WithInvocationRunOptions(agent.RunOptions{ExecutionTraceEnabled: true}),
	)
	loop := &eventLoopContext{
		sess:             rootSession,
		invocation:       root,
		processedEventCh: make(chan *event.Event, 1),
		outputOnlyCompletionChoices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "child final answer",
			},
		}},
	}
	r := &runner{
		sessionService: service,
		appName:        "app",
	}
	r.emitRunnerCompletion(ctx, loop)
	emitted := <-loop.processedEventCh
	require.Equal(t, model.ObjectTypeRunnerCompletion, emitted.Object)
	require.Equal(t, "child final answer", assistantChoicePrimaryContent(emitted.Response.Choices))
	require.Len(t, service.appendEventCalls, 1)
	persisted := service.appendEventCalls[0].event
	require.Equal(t, model.ObjectTypeRunnerCompletion, persisted.Object)
	require.Empty(t, persisted.Response.Choices)
	require.NotNil(t, persisted.ExecutionTrace)
}
