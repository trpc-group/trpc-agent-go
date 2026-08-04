//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestContentRequestProcessorCapturesProjectedSummaryView(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID: "session",
		Events: []event.Event{
			{
				ID:        "event-1",
				Author:    "user",
				Timestamp: now,
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewUserMessage("goal"),
				}}},
			},
			{
				ID:        "event-2",
				Author:    "assistant",
				Timestamp: now.Add(time.Second),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID: "call-1",
							Function: model.FunctionDefinitionParam{
								Name: "lookup",
							},
						}},
					},
				}}},
			},
			{
				ID:        "event-3",
				Author:    "tool",
				Timestamp: now.Add(2 * time.Second),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewToolMessage(
						"call-1",
						"lookup",
						"large raw result",
					),
				}}},
			},
		},
	}
	processor := NewContentRequestProcessor(
		WithEventMessageProjector(func(
			_ *agent.Invocation,
			_ event.Event,
			message model.Message,
		) model.Message {
			if message.ToolID != "" {
				message.Content = "projected tool result"
			}
			return message
		}),
	)
	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
	)
	request := &model.Request{}

	processor.ProcessRequest(
		context.Background(),
		invocation,
		request,
		nil,
	)

	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.Equal(t, sess.ID, view.SessionID)
	require.Len(t, view.Items, 3)
	require.Equal(t, "event-1", view.Items[0].Boundary.EventID)
	require.Equal(t, "event-2", view.Items[1].Boundary.EventID)
	require.Equal(t, "event-3", view.Items[2].Boundary.EventID)
	require.Equal(t, "projected tool result", view.Items[2].Message.Content)
	require.Equal(
		t,
		"projected tool result",
		view.Items[2].EffectiveEvent.Response.Choices[0].Message.Content,
	)
	require.Equal(t, request.Messages, []model.Message{
		view.Items[0].Message,
		view.Items[1].Message,
		view.Items[2].Message,
	})
}

func TestContentRequestProcessorSummaryViewFollowsMaxHistoryRuns(t *testing.T) {
	now := time.Now()
	sess := &session.Session{ID: "session"}
	for i, content := range []string{"old", "middle", "recent"} {
		sess.Events = append(sess.Events, event.Event{
			ID:        "event-" + content,
			Author:    "user",
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage(content),
			}}},
		})
	}
	processor := NewContentRequestProcessor(
		WithAddContextPrefix(false),
		WithMaxHistoryRuns(2),
	)
	invocation := agent.NewInvocation(agent.WithInvocationSession(sess))
	request := &model.Request{}

	processor.ProcessRequest(context.Background(), invocation, request, nil)

	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.Len(t, view.Items, 2)
	require.Equal(t, "event-middle", view.Items[0].Boundary.EventID)
	require.Equal(t, "event-recent", view.Items[1].Boundary.EventID)
	require.Equal(t, "middle", view.Items[0].Message.Content)
	require.Equal(t, "recent", view.Items[1].Message.Content)
}

func TestContentRequestProcessorSummaryViewSkipsOrphanedToolResult(t *testing.T) {
	now := time.Now()
	sess := &session.Session{ID: "session", Events: []event.Event{
		{
			ID:        "event-call",
			Author:    "assistant",
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID: "call-1",
						Function: model.FunctionDefinitionParam{
							Name: "lookup",
						},
					}},
				},
			}}},
		},
		{
			ID:        "event-result",
			Author:    "tool",
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewToolMessage("call-1", "lookup", "orphaned"),
			}}},
		},
		{
			ID:        "event-user",
			Author:    "user",
			Timestamp: now.Add(2 * time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("retained"),
			}}},
		},
	}}
	processor := NewContentRequestProcessor(
		WithAddContextPrefix(false),
		WithMaxHistoryRuns(2),
	)
	invocation := agent.NewInvocation(agent.WithInvocationSession(sess))
	request := &model.Request{}

	processor.ProcessRequest(context.Background(), invocation, request, nil)

	require.Len(t, request.Messages, 1)
	require.Equal(t, "retained", request.Messages[0].Content)
	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.Len(t, view.Items, 1)
	require.Equal(t, "event-user", view.Items[0].Boundary.EventID)
	require.Equal(t, request.Messages[0], view.Items[0].Message)
}

func TestContentRequestProcessorSummaryViewSupportsIsolatedHistory(t *testing.T) {
	now := time.Now()
	sess := &session.Session{ID: "session"}
	invocation := agent.NewInvocation(agent.WithInvocationSession(sess))
	invocation.RunOptions.RuntimeState = map[string]any{
		"include_contents": "none",
	}
	sess.Events = []event.Event{
		{
			ID:           "event-user",
			InvocationID: invocation.InvocationID,
			Author:       "user",
			Timestamp:    now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("isolated goal"),
			}}},
		},
		{
			ID:           "event-answer",
			InvocationID: invocation.InvocationID,
			Author:       "assistant",
			Timestamp:    now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewAssistantMessage("isolated answer"),
			}}},
		},
	}
	request := &model.Request{}

	NewContentRequestProcessor().ProcessRequest(
		context.Background(),
		invocation,
		request,
		nil,
	)

	view, ok := summaryview.Snapshot(invocation)
	require.True(t, ok)
	require.Len(t, view.Items, 2)
	require.Equal(t, "event-user", view.Items[0].Boundary.EventID)
	require.Equal(t, "event-answer", view.Items[1].Boundary.EventID)
	require.Equal(t, request.Messages[0], view.Items[0].Message)
	require.Equal(t, request.Messages[1], view.Items[1].Message)
}
