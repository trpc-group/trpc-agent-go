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
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	out, err := tool.Call(ctx, []byte(`{"limit":2}`))
	require.NoError(t, err)

	result := out.(map[string]any)
	require.Equal(t, "session-1", result["session_id"])
	require.Equal(t, "scope", result["session_user"])
	require.Equal(t, 2, result["turn_count"])
	require.Contains(
		t,
		result["transcript"],
		"Alice: hello",
	)
	require.Contains(
		t,
		result["transcript"],
		"Assistant: hi",
	)
	turns := result["turns"].([]conversation.Turn)
	require.Len(t, turns, 2)
	require.Equal(t, "Alice", turns[0].Speaker)
}

func TestToolCallWithoutInvocation(t *testing.T) {
	tool := NewTool()

	_, err := tool.Call(context.Background(), nil)
	require.ErrorIs(t, err, errToolNotInInvocation)
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
