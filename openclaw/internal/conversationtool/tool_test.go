//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationtool

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestToolCall(t *testing.T) {
	tool := NewTool()
	sess := session.NewSession("app", "scope", "session-1")
	sess.Events = []event.Event{
		userEvent(t, "u1", "Alice", "hello", "", time.Now()),
		assistantEvent("hi", time.Now().Add(time.Second)),
		userEvent(
			t,
			"u2",
			"Bob",
			"what did we decide?",
			"hello",
			time.Now().Add(2*time.Second),
		),
		assistantEvent("ship it", time.Now().Add(3*time.Second)),
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
	)
	inv.RunOptions.RuntimeState = conversation.RuntimeState(
		conversation.Annotation{
			ActorLabels: map[string]string{
				"u1": "alice.dev",
				"u2": "bob.dev",
			},
		},
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	out, err := tool.Call(ctx, []byte(`{"limit":4}`))
	require.NoError(t, err)

	result := out.(map[string]any)
	require.Equal(t, "session-1", result["session_id"])
	require.Equal(t, "scope", result["session_user"])
	require.Equal(t, 4, result["turn_count"])
	require.Contains(
		t,
		result["transcript"],
		"alice.dev: hello",
	)
	require.Contains(
		t,
		result["transcript"],
		"bob.dev (replying to: hello): what did we "+
			"decide?",
	)
	require.Contains(
		t,
		result["transcript"],
		"Assistant: ship it",
	)
	turns := result["turns"].([]conversation.Turn)
	require.Len(t, turns, 4)
	require.Equal(t, "alice.dev", turns[0].Speaker)
	require.Equal(t, "Assistant", turns[1].Speaker)
	require.Equal(t, "bob.dev", turns[2].Speaker)
	require.Equal(t, "Assistant", turns[3].Speaker)
}

func TestToolCallWithoutInvocation(t *testing.T) {
	tool := NewTool()

	_, err := tool.Call(context.Background(), nil)
	require.ErrorIs(t, err, errToolNotInInvocation)
}

func TestToolCall_EdgeCases(t *testing.T) {
	t.Parallel()

	tool := NewTool()

	_, err := tool.Call(context.Background(), []byte("{"))
	require.ErrorContains(t, err, "invalid args")

	require.Equal(t, defaultTurnLimit, normalizeLimit(nil))
	require.Equal(t, defaultTurnLimit, normalizeLimit(intPointer(0)))
	require.Equal(t, maxTurnLimit, normalizeLimit(intPointer(maxTurnLimit+1)))
}

func TestToolCall_IncludeSystemAndLimit(t *testing.T) {
	t.Parallel()

	historyTool := NewTool()
	sess := session.NewSession("app", "scope", "session-2")
	sess.Events = []event.Event{
		{
			Author: "system",
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.NewSystemMessage("summary"),
				}},
			},
		},
		userEvent(t, "u1", "Alice", "hello", "", time.Now()),
		assistantEvent("hi", time.Now().Add(time.Second)),
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	out, err := historyTool.Call(
		ctx,
		[]byte(`{"limit":1,"include_system":true}`),
	)
	require.NoError(t, err)

	result := out.(map[string]any)
	require.Equal(t, 1, result["turn_count"])
	require.Equal(t, "1. Assistant: hi", result["transcript"])
}

func TestToolDeclaration(t *testing.T) {
	t.Parallel()

	decl := NewTool().Declaration()
	require.Equal(t, toolConversationHistory, decl.Name)
	require.Contains(t, decl.Description, "speaker attribution")
	require.Equal(t, "object", decl.InputSchema.Type)
	require.Contains(t, decl.InputSchema.Properties, "limit")
	require.Contains(t, decl.InputSchema.Properties, "include_system")
}

func intPointer(v int) *int {
	return &v
}

func userEvent(
	t *testing.T,
	actorID string,
	actorLabel string,
	content string,
	quote string,
	ts time.Time,
) event.Event {
	t.Helper()

	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewUserMessage(content),
		}},
	})
	evt.Timestamp = ts
	require.NoError(t, conversation.SetEventAnnotation(
		evt,
		conversation.Annotation{
			ActorID:    actorID,
			ActorLabel: actorLabel,
			QuoteText:  quote,
		},
	))
	return *evt
}

func assistantEvent(content string, ts time.Time) event.Event {
	evt := event.NewResponseEvent("inv", "assistant", &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(content),
		}},
	})
	evt.Timestamp = ts
	return *evt
}
