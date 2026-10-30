//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	testReplayProvider = "openai.responses"
	testReplayModel    = "test-model"
)

func bindTestReplay(t *testing.T, msg *model.Message) {
	t.Helper()
	require.NoError(t, msg.SetReplayData(
		testReplayProvider,
		testReplayModel,
		json.RawMessage(`[{"type":"reasoning","id":"r1","encrypted_content":"opaque"}]`),
	))
}

func testToolCalls(ids ...string) []model.ToolCall {
	calls := make([]model.ToolCall, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, model.ToolCall{
			Type: "function",
			ID:   id,
			Function: model.FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: []byte(`{"q":"` + id + `"}`),
			},
		})
	}
	return calls
}

// testCallMessage returns an assistant tool-call message. A native message
// carries Replay; the legacy twin has the same visible projection without it.
func testCallMessage(t *testing.T, native bool, reasoning string, ids ...string) model.Message {
	msg := model.Message{
		Role:             model.RoleAssistant,
		ReasoningContent: reasoning,
		ToolCalls:        testToolCalls(ids...),
	}
	if native {
		bindTestReplay(t, &msg)
	}
	return msg
}

func testTextMessage(t *testing.T, native bool, content string) model.Message {
	msg := model.NewAssistantMessage(content)
	if native {
		bindTestReplay(t, &msg)
	}
	return msg
}

func timedTestEvents(base time.Time, events ...event.Event) []event.Event {
	for i := range events {
		events[i].ID = fmt.Sprintf("event-%d", i)
		events[i].Timestamp = base.Add(time.Duration(i+1) * time.Second)
	}
	return events
}

// persistTestEvents round-trips events through their JSON persistence form.
func persistTestEvents(t *testing.T, events []event.Event) []event.Event {
	t.Helper()
	data, err := json.Marshal(events)
	require.NoError(t, err)
	var restored []event.Event
	require.NoError(t, json.Unmarshal(data, &restored))
	return restored
}

func testInvocation(
	events []event.Event,
	invocationID string,
	requestID string,
	message model.Message,
) *agent.Invocation {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{Events: events}),
		agent.WithInvocationID(invocationID),
		agent.WithInvocationMessage(message),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: requestID}),
	)
	inv.AgentName = "test-agent"
	return inv
}

func requireTestReplay(t *testing.T, msg model.Message) {
	t.Helper()
	require.NotNil(t, msg.Replay)
	payload, err := msg.ReplayDataFor(testReplayProvider, testReplayModel)
	require.NoError(t, err)
	require.NotEmpty(t, payload)
}

// withoutReplay clears native records so a native projection can be compared
// with the projection of the same legacy history.
func withoutReplay(messages []model.Message) []model.Message {
	out := make([]model.Message, len(messages))
	for i, msg := range messages {
		msg.Replay = nil
		out[i] = msg
	}
	return out
}

func toolCallIDOccurrences(messages []model.Message) map[string]int {
	counts := make(map[string]int)
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			counts[call.ID]++
		}
	}
	return counts
}

func TestContentRequestProcessor_NativeRecordProjectionIsReadOnly(t *testing.T) {
	textMessage := testTextMessage(t, true, "checking")
	textMessage.ReasoningContent = "opaque reasoning"
	textMessage.ReasoningSignature = "signature"
	bindTestReplay(t, &textMessage)

	processor := NewContentRequestProcessor(
		WithReasoningContentMode(ReasoningContentModeDiscardAll),
	)
	got := processor.processReasoningContent(textMessage, "old", "new", false)
	require.Equal(t, textMessage.ReasoningContent, got.ReasoningContent)
	require.Equal(t, textMessage.ReasoningSignature, got.ReasoningSignature)
	requireTestReplay(t, got)

	projector := NewContentRequestProcessor(
		WithEventMessageProjector(func(
			_ *agent.Invocation,
			_ event.Event,
			msg model.Message,
		) model.Message {
			msg.Content = "rewritten"
			return msg
		}),
	)
	projected := projector.projectEventMessage(nil, event.Event{}, textMessage)
	_, err := projected.ReplayDataFor(testReplayProvider, testReplayModel)
	require.ErrorContains(t, err, "projection changed",
		"a projector rewrite of a native record must be rejected, not replayed")

	replayOnly := testTextMessage(t, true, "")
	inv := agent.NewInvocation(agent.WithInvocationID("inv1"))
	inv.AgentName = "test-agent"
	projectedMessages := processor.projectMessagesForEvent(
		inv,
		toolTranscriptTestEvent("req1", "inv1", replayOnly, ""),
		"req2",
		nil,
	)
	require.Len(t, projectedMessages, 1)
	requireTestReplay(t, projectedMessages[0])

	_, ok := mergeProjectedAssistantToolCallMessages(
		[]model.Message{textMessage},
		[]model.Message{toolTranscriptTestCall("call_1", "")},
	)
	require.False(t, ok, "native output records must keep their message boundary")
}

func TestContentRequestProcessor_ForeignNativeEventBecomesNewUserContext(t *testing.T) {
	native := testTextMessage(t, true, "native answer")
	native.ReasoningContent = "opaque reasoning"
	bindTestReplay(t, &native)
	foreign := toolTranscriptTestEvent("req1", "inv1", native, "")
	foreign.Author = "native-agent"

	converted := NewContentRequestProcessor().convertForeignEvent(&foreign)
	require.Len(t, converted.Response.Choices, 1)
	quoted := converted.Response.Choices[0].Message
	require.Equal(t, model.RoleUser, quoted.Role)
	require.Nil(t, quoted.Replay)
	require.Contains(t, quoted.Content, "native answer")
	require.NotContains(t, quoted.Content, "opaque reasoning")

	legacyMessage := native
	legacyMessage.Replay = nil
	legacy := toolTranscriptTestEvent("req1", "inv1", legacyMessage, "")
	legacy.Author = "native-agent"
	legacyConverted := NewContentRequestProcessor().convertForeignEvent(&legacy)
	require.Equal(t, legacyConverted.Response.Choices[0].Message, quoted,
		"a quotation of a native record must match the legacy quotation")
}

func TestContentRequestProcessor_ToolTranscriptModeRebasesCompletedNativeRound(t *testing.T) {
	callMessage := testCallMessage(t, true, "opaque reasoning", "call_1")
	callMessage.Content = "checked status"
	callMessage.ReasoningSignature = "signature"
	bindTestReplay(t, &callMessage)
	silentCall := testCallMessage(t, true, "silent reasoning", "call_2")
	pendingCall := testCallMessage(t, true, "pending reasoning", "call_3")
	events := []event.Event{
		toolTranscriptTestEvent("req1", "inv1", callMessage, ""),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_1", "lookup", "result"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1", silentCall, ""),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_2", "lookup", "result"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req2", "inv2", pendingCall, ""),
		toolTranscriptTestEvent("req2", "inv2",
			model.NewToolMessage("call_3", "lookup", "result"), model.ObjectTypeToolResponse),
	}
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv2"),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req2"}),
	)

	got := NewContentRequestProcessor(
		WithToolTranscriptMode(ToolTranscriptModeOmitPreviousCompleted),
	).applyToolTranscriptMode(events, inv)

	require.Len(t, got, 3)
	rebased := got[0].Response.Choices[0].Message
	require.Equal(t, "checked status", rebased.Content)
	require.Empty(t, rebased.ToolCalls)
	require.Nil(t, rebased.Replay,
		"a completed previous native round is rebased to its visible text")
	require.Empty(t, rebased.ReasoningContent)
	require.Empty(t, rebased.ReasoningSignature)

	current := got[1].Response.Choices[0].Message
	require.Len(t, current.ToolCalls, 1)
	require.Equal(t, "call_3", current.ToolCalls[0].ID)
	require.Equal(t, "pending reasoning", current.ReasoningContent)
	requireTestReplay(t, current)
	require.Equal(t, "call_3", got[2].Response.Choices[0].Message.ToolID)
}

// nativeLongInvocationEvents builds one invocation with two completed tool
// rounds followed by the pending round the model is currently answering.
func nativeLongInvocationEvents(t *testing.T, base time.Time, native bool) []event.Event {
	user := model.NewUserMessage("run every lookup")
	return timedTestEvents(base,
		toolTranscriptTestEvent("req1", "inv1", user, ""),
		toolTranscriptTestEvent("req1", "inv1",
			testCallMessage(t, native, "first reasoning", "call_1"), ""),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_1", "lookup", "one"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1",
			testCallMessage(t, native, "second reasoning", "call_2"), ""),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_2", "lookup", "two"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1",
			testCallMessage(t, native, "pending reasoning", "call_3", "call_4"), ""),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_3", "lookup", "three"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_4", "lookup", "four"), model.ObjectTypeToolResponse),
	)
}

func TestContentRequestProcessor_SummaryCutoffSummarizesCompletedNativePrefix(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	user := model.NewUserMessage("run every lookup")
	// The summary covers both completed rounds; the pending round follows it.
	cutoff := summaryHistoryCutoffFromTime(base.Add(5500 * time.Millisecond))
	project := func(native bool) []model.Message {
		events := persistTestEvents(t, nativeLongInvocationEvents(t, base, native))
		inv := testInvocation(events, "inv1", "req1", user)
		return NewContentRequestProcessor().getIncrementMessagesAfterCutoff(inv, nil, cutoff)
	}

	native := project(true)
	require.Equal(t, project(false), withoutReplay(native),
		"a cutoff outside native rounds must keep the legacy projection")

	counts := toolCallIDOccurrences(native)
	require.Zero(t, counts["call_1"], "the summarized prefix must not be replayed")
	require.LessOrEqual(t, counts["call_2"], 1)
	var pending *model.Message
	for i := range native {
		if len(native[i].ToolCalls) == 2 {
			pending = &native[i]
		}
	}
	require.NotNil(t, pending, "the pending native round must stay in the request")
	require.Equal(t, "pending reasoning", pending.ReasoningContent)
	require.Equal(t, []string{"call_3", "call_4"},
		[]string{pending.ToolCalls[0].ID, pending.ToolCalls[1].ID})
	requireTestReplay(t, *pending)
	last := native[len(native)-2:]
	require.Equal(t, "call_3", last[0].ToolID)
	require.Equal(t, "call_4", last[1].ToolID)
}

func TestContentRequestProcessor_SummaryCutoffRestoresOriginalNativeRecord(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	user := model.NewUserMessage("run both lookups")
	history := func(native bool) []event.Event {
		return timedTestEvents(base,
			toolTranscriptTestEvent("req1", "inv1", user, ""),
			toolTranscriptTestEvent("req1", "inv1",
				testCallMessage(t, native, "pending reasoning", "call_1", "call_2"), ""),
			toolTranscriptTestEvent("req1", "inv1",
				model.NewToolMessage("call_1", "lookup", "first result"), model.ObjectTypeToolResponse),
			toolTranscriptTestEvent("req1", "inv1",
				model.NewToolMessage("call_2", "lookup", "second result"), model.ObjectTypeToolResponse),
		)
	}
	project := func(native bool, cutoff time.Duration) []model.Message {
		events := persistTestEvents(t, history(native))
		inv := testInvocation(events, "inv1", "req1", user)
		return NewContentRequestProcessor().getIncrementMessagesAfterCutoff(
			inv, nil, summaryHistoryCutoffFromTime(base.Add(cutoff)),
		)
	}

	t.Run("cutoff after the call", func(t *testing.T) {
		legacy := project(false, 2500*time.Millisecond)
		require.Len(t, legacy, 4)
		require.Len(t, legacy[1].ToolCalls, 2)
		require.Empty(t, legacy[1].ReasoningContent,
			"legacy history restores the call without its reasoning")

		requireNativeRoundIntact(t, project(true, 2500*time.Millisecond))
	})

	t.Run("cutoff inside the results", func(t *testing.T) {
		legacy := project(false, 3500*time.Millisecond)
		require.Len(t, legacy, 3, "legacy history restores a minimal call")
		require.Len(t, legacy[1].ToolCalls, 1)
		require.Equal(t, "call_2", legacy[1].ToolCalls[0].ID)

		native := project(true, 3500*time.Millisecond)
		requireNativeRoundIntact(t, native)
	})
}

// requireNativeRoundIntact checks that the original two-call native record is
// followed by both of its results.
func requireNativeRoundIntact(t *testing.T, messages []model.Message) {
	t.Helper()
	require.Len(t, messages, 4)
	require.Equal(t, model.RoleUser, messages[0].Role)
	record := messages[1]
	require.Len(t, record.ToolCalls, 2)
	require.Equal(t, "call_1", record.ToolCalls[0].ID)
	require.Equal(t, "call_2", record.ToolCalls[1].ID)
	require.Equal(t, "pending reasoning", record.ReasoningContent)
	requireTestReplay(t, record)
	require.Equal(t, "call_1", messages[2].ToolID)
	require.Equal(t, "first result", messages[2].Content)
	require.Equal(t, "call_2", messages[3].ToolID)
	require.Equal(t, "second result", messages[3].Content)
}

func TestPendingNativeToolRoundStart(t *testing.T) {
	native := testCallMessage(t, true, "reasoning", "call_1", "call_2")
	legacy := testCallMessage(t, false, "reasoning", "call_1", "call_2")
	result1 := model.NewToolMessage("call_1", "lookup", "one")
	result2 := model.NewToolMessage("call_2", "lookup", "two")
	user := model.NewUserMessage("question")

	pending := []model.Message{user, native, result1, result2}
	require.Equal(t, 1, pendingNativeToolRoundStart(pending, 3),
		"a limit inside the pending native round keeps the whole record")
	require.Equal(t, 1, pendingNativeToolRoundStart(pending, 4),
		"a limit after skipped orphan results keeps the pending record")
	require.Equal(t, 0, pendingNativeToolRoundStart(pending, 0))

	legacyPending := []model.Message{user, legacy, result1, result2}
	require.Equal(t, 3, pendingNativeToolRoundStart(legacyPending, 3),
		"legacy history keeps the ordinary limit")

	completed := []model.Message{user, native, result1, result2, model.NewAssistantMessage("done")}
	require.Equal(t, 3, pendingNativeToolRoundStart(completed, 3),
		"a completed native round stays subject to the ordinary limit")

	require.Equal(t, 1, pendingNativeToolRoundStart([]model.Message{user, native}, 2),
		"a native call without results yet is still pending")
	require.Equal(t, 1, pendingNativeToolRoundStart([]model.Message{result1}, 1))
	require.Zero(t, pendingNativeToolRoundStart(nil, 0))
}

func TestContentRequestProcessor_MaxHistoryRunsBoundsCompletedNativeRounds(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var events []event.Event
	for i := 1; i <= 20; i++ {
		requestID := fmt.Sprintf("req%d", i)
		invocationID := fmt.Sprintf("inv%d", i)
		callID := fmt.Sprintf("call_%d", i)
		events = append(events,
			toolTranscriptTestEvent(requestID, invocationID,
				model.NewUserMessage(fmt.Sprintf("question %d", i)), ""),
			toolTranscriptTestEvent(requestID, invocationID,
				testCallMessage(t, true, "reasoning", callID), ""),
			toolTranscriptTestEvent(requestID, invocationID,
				model.NewToolMessage(callID, "lookup", "result"), model.ObjectTypeToolResponse),
			toolTranscriptTestEvent(requestID, invocationID,
				testTextMessage(t, true, fmt.Sprintf("answer %d", i)), ""),
		)
	}
	current := model.NewUserMessage("question 21")
	events = append(events,
		toolTranscriptTestEvent("req21", "inv21", current, ""),
		toolTranscriptTestEvent("req21", "inv21",
			testCallMessage(t, true, "pending reasoning", "call_21a", "call_21b"), ""),
		toolTranscriptTestEvent("req21", "inv21",
			model.NewToolMessage("call_21a", "lookup", "a"), model.ObjectTypeToolResponse),
		toolTranscriptTestEvent("req21", "inv21",
			model.NewToolMessage("call_21b", "lookup", "b"), model.ObjectTypeToolResponse),
	)
	events = timedTestEvents(base, events...)

	for _, maxRuns := range []int{1, 2, 3, 6} {
		t.Run(fmt.Sprintf("max %d", maxRuns), func(t *testing.T) {
			processor := NewContentRequestProcessor(WithMaxHistoryRuns(maxRuns))
			inv := testInvocation(events, "inv21", "req21", current)
			got := processor.getIncrementMessagesAfterCutoff(inv, nil, summaryHistoryCutoff{})

			require.LessOrEqual(t, len(got), max(maxRuns, 3),
				"only the pending native round may extend the limit")
			pendingIndex := len(got) - 3
			record := got[pendingIndex]
			require.Len(t, record.ToolCalls, 2)
			require.Equal(t, "pending reasoning", record.ReasoningContent)
			requireTestReplay(t, record)
			require.Equal(t, "call_21a", got[pendingIndex+1].ToolID)
			require.Equal(t, "call_21b", got[pendingIndex+2].ToolID)
			for _, msg := range got[:pendingIndex] {
				require.NotEqual(t, model.RoleTool, msg.Role,
					"completed rounds must not leave orphaned results")
			}
		})
	}
}

func TestContentRequestProcessor_IncompletePerCallNativeRoundStaysIntact(t *testing.T) {
	newEvents := func(native bool) []event.Event {
		call := toolTranscriptTestEvent("req1", "inv1",
			testCallMessage(t, native, "reasoning", "call_1", "call_2"), "")
		result := toolTranscriptTestEvent("req1", "inv1",
			model.NewToolMessage("call_1", "lookup", "one"), model.ObjectTypeToolResponse)
		toolresultround.Mark(&result, true)
		return []event.Event{call, result}
	}
	processor := NewContentRequestProcessor()

	legacy := processor.rearrangeAsyncFuncRespHist(newEvents(false))
	for _, evt := range legacy {
		require.Empty(t, evt.Response.Choices[0].Message.ToolCalls,
			"legacy history strips an incomplete per-call round")
	}

	native := processor.rearrangeAsyncFuncRespHist(newEvents(true))
	require.Len(t, native, 2)
	record := native[0].Response.Choices[0].Message
	require.Len(t, record.ToolCalls, 2,
		"a native record keeps every call so the adapter can reject the gap")
	requireTestReplay(t, record)
	require.Equal(t, "call_1", native[1].Response.Choices[0].Message.ToolID)
}

func TestContentRequestProcessor_NativeHistoryKeepsLegacyCompaction(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	large := strings.Repeat("large tool output ", 200)
	history := func(native bool) []event.Event {
		return timedTestEvents(base,
			toolTranscriptTestEvent("req1", "inv1", model.NewUserMessage("first"), ""),
			toolTranscriptTestEvent("req1", "inv1",
				testCallMessage(t, native, "first reasoning", "call_1"), ""),
			toolTranscriptTestEvent("req1", "inv1",
				model.NewToolMessage("call_1", "lookup", large), model.ObjectTypeToolResponse),
			toolTranscriptTestEvent("req1", "inv1", testTextMessage(t, native, "first answer"), ""),
			toolTranscriptTestEvent("req2", "inv2", model.NewUserMessage("second"), ""),
			toolTranscriptTestEvent("req2", "inv2",
				testCallMessage(t, native, "second reasoning", "call_2"), ""),
			toolTranscriptTestEvent("req2", "inv2",
				model.NewToolMessage("call_2", "lookup", large), model.ObjectTypeToolResponse),
		)
	}
	project := func(native bool) []model.Message {
		processor := NewContentRequestProcessor(
			WithEnableContextCompaction(true),
			WithContextCompactionKeepRecentRequests(0),
			WithContextCompactionToolResultMaxTokens(10),
			WithContextCompactionOversizedToolResultMaxTokens(50),
		)
		inv := testInvocation(history(native), "inv2", "req2", model.NewUserMessage("second"))
		return processor.getIncrementMessagesAfterCutoff(inv, nil, summaryHistoryCutoff{})
	}

	legacy := project(false)
	native := project(true)
	require.Equal(t, legacy, withoutReplay(native))
	var compacted int
	for _, msg := range native {
		if msg.Role == model.RoleTool && msg.Content != large {
			compacted++
		}
		if msg.Role == model.RoleAssistant {
			requireTestReplay(t, msg)
		}
	}
	require.Positive(t, compacted, "native histories must still compact tool results")
}

func TestCompactResumeToolRoundKeepsNativeArguments(t *testing.T) {
	arguments := []byte(`{"q":"` + strings.Repeat("argument ", 200) + `"}`)
	native := testCallMessage(t, true, "reasoning", "call_1")
	native.ToolCalls[0].Function.Arguments = arguments
	bindTestReplay(t, &native)
	legacy := testCallMessage(t, false, "reasoning", "call_1")
	legacy.ToolCalls[0].Function.Arguments = arguments
	large := strings.Repeat("large tool output ", 200)
	tail := func(call model.Message) map[int]event.Event {
		return map[int]event.Event{
			0: toolTranscriptTestEvent("req1", "inv1", call, ""),
			1: toolTranscriptTestEvent("req1", "inv1",
				model.NewToolMessage("call_1", "lookup", large), model.ObjectTypeToolResponse),
		}
	}
	cfg := normalizeContextCompactionConfig(ContextCompactionConfig{
		Enabled:             true,
		ToolResultMaxTokens: 10,
	})

	legacyTail := compactResumeToolRound(context.Background(), tail(legacy), cfg)
	require.NotEqual(t, arguments,
		[]byte(legacyTail[0].Response.Choices[0].Message.ToolCalls[0].Function.Arguments),
		"legacy resume tails compact oversized arguments")

	nativeTail := compactResumeToolRound(context.Background(), tail(native), cfg)
	record := nativeTail[0].Response.Choices[0].Message
	require.Equal(t, arguments, []byte(record.ToolCalls[0].Function.Arguments))
	requireTestReplay(t, record)
	require.NotEqual(t, large, nativeTail[1].Response.Choices[0].Message.Content,
		"native resume tails still compact tool results")
}
