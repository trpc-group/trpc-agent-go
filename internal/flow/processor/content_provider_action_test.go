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

func TestRearrangeMixedNativeActionResults(t *testing.T) {
	call := testCallMessage(t, true, "reasoning", "call_1")
	searchResult := toolTranscriptTestEvent("req1", "inv1", nativeActionResult(), model.ObjectTypeToolResponse)
	functionResult := toolTranscriptTestEvent("req1", "inv1",
		model.NewToolMessage("call_1", "lookup", "result"), model.ObjectTypeToolResponse)
	for name, results := range map[string][]event.Event{
		"search then function": {searchResult, functionResult},
		"function then search": {functionResult, searchResult},
	} {
		t.Run(name, func(t *testing.T) {
			events := append([]event.Event{
				toolTranscriptTestEvent("req1", "inv1", model.NewUserMessage("find orders"), ""),
				toolTranscriptTestEvent("req1", "inv1", call, ""),
			}, results...)
			for _, history := range [][]event.Event{events, persistTestEvents(t, events)} {
				p := NewContentRequestProcessor()
				got := p.rearrangeLatestFuncResp(history)
				require.Equal(t, history, got, "latest-result handling must retain the complete native round")
				got = p.rearrangeAsyncFuncRespHist(got)
				require.Equal(t, history, got, "history preparation must preserve result order")
			}
		})
	}
}

func TestRearrangeSharedLegacyAndNativeResultEvent(t *testing.T) {
	legacy := toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, false, "", "legacy"), "")
	native := toolTranscriptTestEvent("req2", "inv2", testCallMessage(t, true, "reasoning", "native"), "")
	result := toolTranscriptTestEvent("req2", "inv2",
		model.NewToolMessage("native", "lookup", "native result"), model.ObjectTypeToolResponse)
	result.Choices = append(result.Choices,
		model.Choice{Message: model.NewToolMessage("legacy", "lookup", "legacy result")},
		model.Choice{Message: nativeActionResult()})
	events := []event.Event{legacy, native, result}
	got := NewContentRequestProcessor().rearrangeAsyncFuncRespHist(events)
	require.Len(t, got, 4)
	require.Equal(t, legacy, got[0])
	require.Equal(t, []model.Choice{result.Choices[1]}, got[1].Choices,
		"ordinary results retain their existing placement next to the call")
	require.Equal(t, native, got[2])
	require.Equal(t, []model.Choice{result.Choices[0], result.Choices[2]}, got[3].Choices)
	require.Len(t, events[2].Choices, 3, "history preparation does not mutate the stored event")
}

func TestMixedNativeActionSourceSurvivesCloneAndRestoresCurrentTail(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	makeRound := func(callID string) []event.Event {
		return []event.Event{
			toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, true, "reasoning", callID), ""),
			toolTranscriptTestEvent("req1", "inv1", nativeActionResult(), model.ObjectTypeToolResponse),
			toolTranscriptTestEvent("req1", "inv1", model.NewToolMessage(callID, "lookup", "result"), model.ObjectTypeToolResponse),
		}
	}
	events := timedTestEvents(base, append(makeRound("old"), makeRound("new")...)...)
	for _, i := range []int{0, 3} {
		MarkProviderActionSource(&events[i])
		require.NoError(t, event.SetExtension(&events[i+1], providerActionSourceExtension, ProviderActionSource(&events[i])))
	}
	// Cloning changes event IDs, but the shared source marker remains stable.
	for i := range events {
		events[i] = *events[i].Clone()
	}
	events = persistTestEvents(t, events)
	inv := testInvocation(events, "inv1", "req1", model.Message{})
	cutoff := newEventHistoryCutoff(events, summaryHistoryCutoff{
		at: events[5].Timestamp, lastEventID: events[5].ID,
	})
	tail := NewContentRequestProcessor().latestCompleteToolRoundBeforeCutoff(events, inv, nil, "", cutoff)
	require.Len(t, tail, 3)
	require.NotContains(t, tail, 0, "the older covered round must not be revived")
	require.NotContains(t, tail, 1)
	require.NotContains(t, tail, 2)
	require.Contains(t, tail, 3)
	require.Contains(t, tail, 4)
	require.Contains(t, tail, 5)
	require.Equal(t, "search_1", tail[4].Choices[0].Message.ToolID)
}

func TestMixedNativeActionResultsMatchOnlyTheirSource(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	call1 := toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, true, "", "one"), "")
	call2 := toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, true, "", "two"), "")
	result := toolTranscriptTestEvent("req1", "inv1", nativeActionResult(), model.ObjectTypeToolResponse)
	events := timedTestEvents(base, call1, call2, result)
	MarkProviderActionSource(&events[0])
	MarkProviderActionSource(&events[1])
	require.NoError(t, event.SetExtension(&events[2], providerActionSourceExtension, ProviderActionSource(&events[0])))
	matches := toolResponseMatchesByCallEvent(events)
	require.Equal(t, []matchedToolResponseEvent{{eventIndex: 2, choiceIndices: []int{0}}}, matches[0])
	require.Empty(t, matches[1], "the nearest unrelated native response must not claim the result")
}

func TestMixedNativeActionRestoresWhenSearchResultFollowsCutoff(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := model.NewUserMessage("next")
	events := timedTestEvents(base,
		toolTranscriptTestEvent("req1", "inv1", model.NewUserMessage("question"), ""),
		toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, true, "", "weather"), ""),
		toolTranscriptTestEvent("req1", "inv1", model.NewToolMessage("weather", "weather", "sunny"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1", nativeActionResult(), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1", model.NewAssistantMessage("done"), ""),
		toolTranscriptTestEvent("req2", "inv2", current, ""),
	)
	MarkProviderActionSource(&events[1])
	require.NoError(t, event.SetExtension(&events[3], providerActionSourceExtension, ProviderActionSource(&events[1])))
	events = persistTestEvents(t, events)
	inv := testInvocation(events, "inv2", "req2", current)
	got := NewContentRequestProcessor().getIncrementMessagesAfterCutoff(inv, nil,
		summaryHistoryCutoff{at: events[2].Timestamp, lastEventID: events[2].ID})
	require.Len(t, got, 6)
	require.Equal(t, "question", got[0].Content)
	require.Equal(t, "weather", got[1].ToolCalls[0].ID)
	require.Equal(t, "weather", got[2].ToolID)
	require.Equal(t, "search_1", got[3].ToolID)
	require.Equal(t, nativeActionResult().ProviderData, got[3].ProviderData)
	require.Equal(t, "done", got[4].Content)
	require.Equal(t, "next", got[5].Content)
}

func TestMixedNativeLegacySourceWithResponseIDSurvivesClone(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	call := toolTranscriptTestEvent("req1", "inv1", testCallMessage(t, true, "", "weather"), "")
	call.Response.ID = "model-response"
	result := toolTranscriptTestEvent("req2", "inv2", nativeActionResult(), model.ObjectTypeToolResponse)
	events := timedTestEvents(base, call, result)
	source := ProviderActionSource(&events[0])
	require.NoError(t, event.SetExtension(&events[1], providerActionSourceExtension, source))
	events[0] = *events[0].Clone()
	require.Equal(t, source, ProviderActionSource(&events[0]))
	matches := toolResponseMatchesByCallEvent(persistTestEvents(t, events))
	require.Equal(t, []matchedToolResponseEvent{{eventIndex: 1, choiceIndices: []int{0}}}, matches[0],
		"a resumed result still references the older source after cloning")
}

func TestNativeActionOnlyRestoresWhenResultFollowsCutoff(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := model.NewUserMessage("next")
	events := timedTestEvents(base,
		toolTranscriptTestEvent("req1", "inv1", model.NewUserMessage("find orders"), ""),
		toolTranscriptTestEvent("req1", "inv1", nativeActionRecord(), ""),
		toolTranscriptTestEvent("req1", "inv1", nativeActionResult(), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1", model.NewAssistantMessage("done"), ""),
		toolTranscriptTestEvent("req2", "inv2", current, ""),
	)
	MarkProviderActionSource(&events[1])
	require.NoError(t, event.SetExtension(&events[2], providerActionSourceExtension, ProviderActionSource(&events[1])))
	events = persistTestEvents(t, events)
	inv := testInvocation(events, "inv2", "req2", current)
	got := NewContentRequestProcessor().getIncrementMessagesAfterCutoff(inv, nil,
		summaryHistoryCutoff{at: events[1].Timestamp, lastEventID: events[1].ID})
	require.Len(t, got, 5)
	require.Equal(t, "find orders", got[0].Content)
	require.Equal(t, nativeActionRecord().ProviderData, got[1].ProviderData)
	require.Equal(t, "search_1", got[2].ToolID)
	require.Equal(t, nativeActionResult().ProviderData, got[2].ProviderData)
	require.Equal(t, "done", got[3].Content)
	require.Equal(t, "next", got[4].Content)
}
