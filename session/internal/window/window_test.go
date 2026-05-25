//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package window

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestEventWindowFromOrderedEvents(t *testing.T) {
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	events := []event.Event{
		testEvent("u1", model.RoleUser, "one"),
		testToolCallEvent("tool-call-only"),
		testEvent("a1", model.RoleAssistant, "two"),
		testPartialEvent("partial"),
		testToolEvent("t1", "calc", "three"),
		testEvent("u2", model.RoleUser, "four"),
	}

	got, err := EventWindowFromOrderedEvents(
		key,
		events,
		session.EventWindowRequest{
			Key:           key,
			AnchorEventID: "t1",
			Before:        2,
			After:         1,
			Roles: []model.Role{
				model.RoleUser,
				model.RoleAssistant,
				model.RoleTool,
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "a1", "t1", "u2"}, windowEventIDs(got))
	require.Equal(t, "t1", got.AnchorEventID)
}

func TestEventWindowFromOrderedEventsRoleFilter(t *testing.T) {
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	events := []event.Event{
		testEvent("u1", model.RoleUser, "one"),
		testEvent("a1", model.RoleAssistant, "two"),
		testToolEvent("t1", "calc", "three"),
		testEvent("u2", model.RoleUser, "four"),
	}

	got, err := EventWindowFromOrderedEvents(
		key,
		events,
		session.EventWindowRequest{
			Key:           key,
			AnchorEventID: "u2",
			Before:        2,
			Roles:         []model.Role{model.RoleUser},
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2"}, windowEventIDs(got))

	_, err = EventWindowFromOrderedEvents(
		key,
		events,
		session.EventWindowRequest{
			Key:           key,
			AnchorEventID: "t1",
			Roles:         []model.Role{model.RoleUser},
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event not found")
}

func TestEventWindowFromOrderedEventsValidation(t *testing.T) {
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}

	_, err := EventWindowFromOrderedEvents(
		key,
		nil,
		session.EventWindowRequest{Key: key},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchor event id is required")

	_, err = EventWindowFromOrderedEvents(
		key,
		nil,
		session.EventWindowRequest{
			Key:           key,
			AnchorEventID: "missing",
			Before:        -1,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "before >= 0")
}

func testEvent(id string, role model.Role, content string) event.Event {
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

func testToolEvent(id, name, content string) event.Event {
	evt := testEvent(id, model.RoleTool, content)
	evt.Response.Choices[0].Message.ToolID = "call-" + id
	evt.Response.Choices[0].Message.ToolName = name
	return evt
}

func testToolCallEvent(id string) event.Event {
	evt := testEvent(id, model.RoleAssistant, "")
	evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
		ID: "call-" + id,
	}}
	return evt
}

func testPartialEvent(id string) event.Event {
	evt := testEvent(id, model.RoleAssistant, "partial")
	evt.Response.IsPartial = true
	return evt
}

func windowEventIDs(window *session.EventWindow) []string {
	ids := make([]string, 0, len(window.Entries))
	for _, entry := range window.Entries {
		ids = append(ids, entry.Event.ID)
	}
	return ids
}
