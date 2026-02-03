//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package history

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newTestSessionWithEvents(events []event.Event) *session.Session {
	return &session.Session{ID: "sess-1", AppName: "app", UserID: "u", Events: events}
}

func msgEvent(id string, role model.Role, content string) event.Event {
	return event.Event{
		ID:        id,
		Author:    string(role),
		Timestamp: time.Now(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: role, Content: content},
			}},
		},
	}
}

func toolResultEvent(id string, content string, toolID string) event.Event {
	return event.Event{
		ID:        id,
		Author:    "tool",
		Timestamp: time.Now(),
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleTool, Content: content, ToolID: toolID},
			}},
		},
	}
}

func TestHistoryTools_SearchAndGet_WithBudgetAndTruncation(t *testing.T) {
	large := strings.Repeat("x", 10000)
	sess := newTestSessionWithEvents([]event.Event{
		msgEvent("e1", model.RoleUser, "hello world"),
		toolResultEvent("e2", large, "tool-1"),
		msgEvent("e3", model.RoleAssistant, "done"),
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	search := NewSearchTool()
	args, _ := json.Marshal(map[string]any{"query": "hello", "limit": 10, "maxChars": 50})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(SearchResult)
	require.True(t, res.Success)
	require.Len(t, res.Items, 1)
	require.Equal(t, "e1", res.Items[0].EventID)
	require.NotNil(t, res.BudgetRemaining)
	require.Equal(t, 2, res.BudgetRemaining.SearchCallsRemaining)

	// Search tool should not blow up on huge tool output, and should return snippets.
	args2, _ := json.Marshal(map[string]any{"query": "x", "limit": 1, "maxChars": 100})
	resAny2, err := search.Call(ctx, args2)
	require.NoError(t, err)
	res2 := resAny2.(SearchResult)
	require.True(t, res2.Success)
	require.Len(t, res2.Items, 1)
	require.Equal(t, "e2", res2.Items[0].EventID)
	require.True(t, res2.Items[0].Truncated)
	require.LessOrEqual(t, len(res2.Items[0].Snippet), 100)
	// budget decremented again
	require.Equal(t, 1, res2.BudgetRemaining.SearchCallsRemaining)

	get := NewGetEventsTool()
	getArgs, _ := json.Marshal(map[string]any{"eventIds": []string{"e2"}, "maxChars": 2000})
	getAny, err := get.Call(ctx, getArgs)
	require.NoError(t, err)
	getRes := getAny.(GetEventsResult)
	require.True(t, getRes.Success)
	require.Len(t, getRes.Items, 1)
	require.Equal(t, "e2", getRes.Items[0].EventID)
	require.True(t, getRes.Items[0].Truncated)
	require.LessOrEqual(t, len(getRes.Items[0].Content), 2000)
	require.Equal(t, 1, getRes.BudgetRemaining.GetCallsRemaining)
}

func TestHistoryTools_BudgetExhausted(t *testing.T) {
	sess := newTestSessionWithEvents([]event.Event{msgEvent("e1", model.RoleUser, "hello")})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	b := getOrInitBudget(inv)
	b.SearchCallsRemaining = 0

	search := NewSearchTool()
	args, _ := json.Marshal(map[string]any{"query": "hello"})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(SearchResult)
	require.False(t, res.Success)
	require.Contains(t, res.Message, "budget")
}
