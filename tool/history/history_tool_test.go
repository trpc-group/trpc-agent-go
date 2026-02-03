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

	search := newSearchTool()
	args, _ := json.Marshal(map[string]any{"query": "hello", "limit": 10, "maxChars": 50})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.True(t, res.Success)
	require.Len(t, res.Items, 1)
	require.Equal(t, "e1", res.Items[0].EventID)
	require.NotNil(t, res.BudgetRemaining)
	require.Equal(t, 2, res.BudgetRemaining.SearchCallsRemaining)

	// Search tool should not blow up on huge tool output, and should return snippets.
	args2, _ := json.Marshal(map[string]any{"query": "x", "limit": 1, "maxChars": 100})
	resAny2, err := search.Call(ctx, args2)
	require.NoError(t, err)
	res2 := resAny2.(searchResult)
	require.True(t, res2.Success)
	require.Len(t, res2.Items, 1)
	require.Equal(t, "e2", res2.Items[0].EventID)
	require.True(t, res2.Items[0].Truncated)
	require.LessOrEqual(t, len(res2.Items[0].Snippet), 100)
	// budget decremented again
	require.Equal(t, 1, res2.BudgetRemaining.SearchCallsRemaining)

	get := newGetEventsTool()
	getArgs, _ := json.Marshal(map[string]any{"eventIds": []string{"e2"}, "maxChars": 2000})
	getAny, err := get.Call(ctx, getArgs)
	require.NoError(t, err)
	getRes := getAny.(getEventsResult)
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

	search := newSearchTool()
	args, _ := json.Marshal(map[string]any{"query": "hello"})
	resAny, err := search.Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.False(t, res.Success)
	require.Contains(t, res.Message, "budget")
}

func TestToolSet_Basics(t *testing.T) {
	ts := NewToolSet()
	require.Equal(t, "history", ts.Name())
	require.Len(t, ts.Tools(context.Background()), 2)
	require.NoError(t, ts.Close())
}

func TestSearchTool_DeclarationAndCursorParsing(t *testing.T) {
	decl := newSearchTool().Declaration()
	require.NotNil(t, decl)
	require.Equal(t, SearchToolName, decl.Name)
	require.NotNil(t, decl.InputSchema)
	require.Equal(t, "object", decl.InputSchema.Type)

	// base64("2")
	require.Equal(t, 2, parseSearchCursor("Mg=="))
	require.Equal(t, 3, parseSearchCursor("3"))
	require.Equal(t, 0, parseSearchCursor("-10"))
	require.Equal(t, 0, parseSearchCursor("not-a-number"))
}

func TestSearchTool_RolesAndTimeFiltering(t *testing.T) {
	now := time.Now()
	sess := newTestSessionWithEvents([]event.Event{
		{ID: "e1", Timestamp: now.Add(-2 * time.Minute), Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello world"}}}}},
		{ID: "e2", Timestamp: now.Add(-1 * time.Minute), Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "HELLO there"}}}}},
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	since := toUnixMs(now.Add(-90 * time.Second))
	args, _ := json.Marshal(map[string]any{
		"query":    "hello",
		"roles":    []string{" assistant ", ""},
		"sinceMs":  since,
		"limit":    10,
		"maxChars": 80,
	})
	resAny, err := newSearchTool().Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(searchResult)
	require.True(t, res.Success)
	require.Len(t, res.Items, 1)
	require.Equal(t, "e2", res.Items[0].EventID)
}

func TestEventMessageText_ToolCallsFormatting(t *testing.T) {
	e := event.Event{ID: "e1", Timestamp: time.Now(), Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{Type: "function", Function: model.FunctionDefinitionParam{Name: "search_history", Arguments: []byte("{\"query\":\"x\"}")}},
			{Type: "function", Function: model.FunctionDefinitionParam{Name: "get_history_events"}},
		},
	}}}}}
	role, txt := eventMessageText(e)
	require.Equal(t, string(model.RoleAssistant), role)
	require.Contains(t, txt, "search_history({\"query\":\"x\"})")
	require.Contains(t, txt, "get_history_events()")
}

func TestBudgetHelpers_StateAndSpend(t *testing.T) {
	inv := &agent.Invocation{}
	inv.SetState(invStateKeyBudget, "not-a-budget")
	b := getOrInitBudget(inv)
	require.NotNil(t, b)
	require.Greater(t, b.CharsRemaining, 0)

	require.NoError(t, spendChars(nil, 10))
	require.NoError(t, spendChars(b, 0))

	b2 := &budget{CharsRemaining: 3}
	require.Error(t, spendChars(b2, 10))
}

func TestGetEventsTool_DeclarationAndValidation(t *testing.T) {
	decl := newGetEventsTool().Declaration()
	require.NotNil(t, decl)
	require.Equal(t, GetEventsToolName, decl.Name)
	require.NotNil(t, decl.InputSchema)
	require.Contains(t, decl.InputSchema.Required, "eventIds")

	sess := newTestSessionWithEvents([]event.Event{msgEvent("e1", model.RoleUser, "hello")})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	resAny, err := newGetEventsTool().Call(ctx, []byte(`{"eventIds":[]}`))
	require.NoError(t, err)
	res := resAny.(getEventsResult)
	require.False(t, res.Success)
	require.Contains(t, res.Message, "empty")
}

func TestGetEventsTool_DedupeClampAndBudgetChars(t *testing.T) {
	large := strings.Repeat("y", 500)
	sess := newTestSessionWithEvents([]event.Event{
		toolResultEvent("e1", large, "tool-1"),
		toolResultEvent("e2", large, "tool-2"),
		toolResultEvent("e3", large, "tool-3"),
		toolResultEvent("e4", large, "tool-4"),
	})
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	// Force a tiny remaining budget so spending will fail.
	b := getOrInitBudget(inv)
	b.CharsRemaining = 10

	args, _ := json.Marshal(map[string]any{
		"eventIds": []string{" e1 ", "e1", "e2", "e3", "e4"},
		"maxChars": 200,
	})
	resAny, err := newGetEventsTool().Call(ctx, args)
	require.NoError(t, err)
	res := resAny.(getEventsResult)
	require.False(t, res.Success)
	require.Contains(t, res.Message, "budget")
}
