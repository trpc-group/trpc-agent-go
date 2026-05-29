//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionService_GetEventWindow(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	svc := NewSessionService()
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	for _, evt := range []event.Event{
		windowTestEvent("u1", model.RoleUser, "one"),
		windowTestToolCallEvent("call-only"),
		windowTestEvent("a1", model.RoleAssistant, "two"),
		windowTestToolEvent("t1", "calc", "three"),
		windowTestEvent("u2", model.RoleUser, "four"),
	} {
		evt := evt
		require.NoError(t, svc.AppendEvent(ctx, sess, &evt))
	}

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "t1",
		Before:        2,
		After:         1,
		Roles: []model.Role{
			model.RoleUser,
			model.RoleAssistant,
			model.RoleTool,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "a1", "t1", "u2"}, windowTestIDs(got))
}

func TestSessionService_GetEventWindowUsesRawSessionEvents(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "assistant-only"}
	svc := NewSessionService()
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	evt := windowTestEvent("a1", model.RoleAssistant, "leading assistant")
	require.NoError(t, svc.AppendEvent(ctx, sess, &evt))

	got, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "a1",
		Roles:         []model.Role{model.RoleAssistant},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"a1"}, windowTestIDs(got))
}

func TestSessionService_GetEventWindowValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewSessionService()
	_, err := svc.GetEventWindow(ctx, session.EventWindowRequest{})
	require.Error(t, err)

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           session.Key{UserID: "user", SessionID: "sess"},
		AnchorEventID: "anchor",
	})
	require.Error(t, err)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "missing",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func windowTestEvent(id string, role model.Role, content string) event.Event {
	return event.Event{
		ID:        id,
		Timestamp: time.Unix(int64(len(id)), 0).UTC(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    role,
					Content: content,
				},
			}},
		},
	}
}

func windowTestToolEvent(id, name, content string) event.Event {
	evt := windowTestEvent(id, model.RoleTool, content)
	evt.Response.Choices[0].Message.ToolID = "call-" + id
	evt.Response.Choices[0].Message.ToolName = name
	return evt
}

func windowTestToolCallEvent(id string) event.Event {
	evt := windowTestEvent(id, model.RoleAssistant, "")
	evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
		ID: "call-" + id,
	}}
	return evt
}

func windowTestIDs(window *session.EventWindow) []string {
	ids := make([]string, 0, len(window.Entries))
	for _, entry := range window.Entries {
		ids = append(ids, entry.Event.ID)
	}
	return ids
}
