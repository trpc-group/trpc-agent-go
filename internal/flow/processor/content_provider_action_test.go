//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// nativeActionRecord is a native record whose only action is answered by a
// native tool result rather than a projected tool call.
func nativeActionRecord() model.Message {
	msg := model.NewAssistantMessage("")
	msg.ProviderData = &model.ProviderData{
		Protocol: testProviderDataProvider,
		Model:    testProviderDataModel,
		Payload:  json.RawMessage(`[{"type":"tool_search_call","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"orders"}}]`),
	}
	return msg
}

func nativeActionResult() model.Message {
	msg := model.NewToolMessage("search_1", "tool_search", `{"loaded_tools":["crm_orders"]}`)
	msg.ProviderData = &model.ProviderData{
		Protocol: testProviderDataProvider,
		Model:    testProviderDataModel,
		Payload:  json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"search_1","status":"completed","tools":[]}]`),
	}
	return msg
}

func nativeActionRoundEvents(t *testing.T) []event.Event {
	return []event.Event{
		toolTranscriptTestEvent("req1", "inv1", model.NewUserMessage("find orders"), ""),
		toolTranscriptTestEvent("req1", "inv1", nativeActionRecord(), ""),
		toolTranscriptTestEvent("req1", "inv1", nativeActionResult(), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, true, "reasoning", "call_1"), ""),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_1", "lookup", "result"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1", testTextMessage(t, true, "answer"), ""),
	}
}

func TestContentRequestProcessor_ProviderActionResultKeepsItsPosition(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := model.NewUserMessage("again")
	events := timedTestEvents(base, append(nativeActionRoundEvents(t),
		toolTranscriptTestEvent("req2", "inv2", current, ""))...)
	processor := NewContentRequestProcessor()
	for name, history := range map[string][]event.Event{
		"live":      events,
		"persisted": persistTestEvents(t, events),
	} {
		t.Run(name, func(t *testing.T) {
			inv := testInvocation(history, "inv2", "req2", current)
			got := processor.getIncrementMessagesAfterCutoff(inv, nil, summaryHistoryCutoff{})
			require.Len(t, got, 7)
			require.Equal(t, model.RoleUser, got[0].Role)
			require.NotNil(t, got[1].ProviderData)
			require.Empty(t, got[1].ToolCalls)
			require.Equal(t, model.RoleTool, got[2].Role)
			require.Equal(t, "search_1", got[2].ToolID)
			require.NotNil(t, got[2].ProviderData, "the native result keeps its record")
			require.Equal(t, "call_1", got[3].ToolCalls[0].ID)
			require.Equal(t, "call_1", got[4].ToolID)
			require.Equal(t, "answer", got[5].Content)
			require.Equal(t, "again", got[6].Content)
		})
	}
}

func TestContentRequestProcessor_MaxHistoryRunsWithProviderActions(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("pending action stays with its record", func(t *testing.T) {
		events := timedTestEvents(base, nativeActionRoundEvents(t)[:3]...)
		processor := NewContentRequestProcessor(WithMaxHistoryRuns(1))
		inv := testInvocation(events, "inv1", "req1", model.NewUserMessage("find orders"))
		got := processor.getIncrementMessagesAfterCutoff(inv, nil, summaryHistoryCutoff{})
		require.GreaterOrEqual(t, len(got), 2)
		record, result := got[len(got)-2], got[len(got)-1]
		require.NotNil(t, record.ProviderData)
		require.Equal(t, model.RoleAssistant, record.Role)
		require.Equal(t, "search_1", result.ToolID)
	})

	t.Run("a cut completed action drops its result", func(t *testing.T) {
		current := model.NewUserMessage("again")
		events := timedTestEvents(base, append(nativeActionRoundEvents(t),
			toolTranscriptTestEvent("req2", "inv2", current, ""))...)
		processor := NewContentRequestProcessor(WithMaxHistoryRuns(5))
		inv := testInvocation(events, "inv2", "req2", current)
		got := processor.getIncrementMessagesAfterCutoff(inv, nil, summaryHistoryCutoff{})
		require.NotEmpty(t, got)
		require.NotEqual(t, model.RoleTool, got[0].Role,
			"a native result whose record was cut must not lead the history")
		require.Equal(t, "call_1", got[0].ToolCalls[0].ID)
	})
}

func TestPendingNativeToolRoundStartWithProviderActions(t *testing.T) {
	user := model.NewUserMessage("question")
	record := nativeActionRecord()
	result := nativeActionResult()
	require.Equal(t, 1, pendingNativeToolRoundStart([]model.Message{user, record, result}, 3),
		"a native record answered only by native results is pending")
	require.Equal(t, 2, pendingNativeToolRoundStart([]model.Message{user, record}, 2),
		"a native record without results or calls is a completed answer")
}
