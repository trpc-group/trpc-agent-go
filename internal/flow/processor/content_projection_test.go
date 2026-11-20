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
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestEffectiveEventForMessagePreservesMetadata(t *testing.T) {
	fingerprint, finishReason := "fingerprint", "stop"
	evt := event.Event{
		ID: "event", RequestID: "request", InvocationID: "invocation",
		Author: "agent", Branch: "agent", FilterKey: "agent", Tag: "tag",
		Timestamp: time.Unix(100, 0), Version: event.CurrentVersion,
		LongRunningToolIDs: map[string]struct{}{"call": {}},
		StateDelta:         map[string][]byte{"state": []byte("value")},
		Actions:            &event.EventActions{SkipSummarization: true},
		Extensions:         map[string]json.RawMessage{"custom": json.RawMessage(`{"key":"value"}`)},
		Response: &model.Response{
			ID: "response", Object: model.ObjectTypeToolResponse,
			Created: 100, Model: "model", Timestamp: time.Unix(101, 0), Done: true,
			SystemFingerprint: &fingerprint,
			Usage: &model.Usage{
				PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30,
				TimingInfo: &model.TimingInfo{FirstTokenDuration: time.Second},
			},
			Error: &model.ResponseError{Type: "test", Message: "original"},
			Choices: []model.Choice{
				{Index: 1, Message: model.NewAssistantMessage("first"), FinishReason: &finishReason},
				{Index: 2, Message: model.NewAssistantMessage("second")},
			},
		},
	}
	projected := model.Message{Role: model.RoleTool, ToolID: "call", ToolName: "lookup", Content: "projected result"}
	before, err := json.Marshal(evt)
	require.NoError(t, err)

	got := effectiveEventForMessage(evt, projected)
	want := evt
	wantResponse := *evt.Response
	wantResponse.Choices = []model.Choice{{Message: projected}}
	want.Response = &wantResponse
	require.Equal(t, want, got)

	// A projected event must retain the snapshot isolation of metadata, and
	// replacing its single choice must not overwrite the stored batch.
	got.Choices[0].Message.Content = "changed"
	got.Usage.PromptTokens++
	got.Usage.TimingInfo.FirstTokenDuration++
	got.Error.Message = "changed"
	*got.SystemFingerprint = "changed"
	got.LongRunningToolIDs["another"] = struct{}{}
	got.StateDelta["state"][0] = 'x'
	got.Actions.SkipSummarization = false
	after, err := json.Marshal(evt)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestEffectiveEventForMessageEmptyResponse(t *testing.T) {
	msg := model.NewUserMessage("projected")
	for _, response := range []*model.Response{nil, {}, {Choices: []model.Choice{}}} {
		got := effectiveEventForMessage(event.Event{ID: "event", Response: response}, msg)
		require.NotNil(t, got.Response)
		assert.Equal(t, "event", got.ID)
		assert.Equal(t, []model.Choice{{Message: msg}}, got.Choices)
	}
}

func TestEffectiveEventForMessageDoesNotCloneDiscardedChoices(t *testing.T) {
	// Discarded choice payloads must not add allocations to the projection
	// of one message. Logprobs make those otherwise invisible copies countable.
	measure := func(count int) float64 {
		choices := make([]model.Choice, count)
		for i := range choices {
			choices[i].Logprobs = &model.Logprobs{Content: []model.TokenLogprob{{Token: "unused", Bytes: []int{1}}}}
		}
		evt := event.Event{Response: &model.Response{Choices: choices}}
		var projected event.Event
		allocs := testing.AllocsPerRun(10, func() {
			projected = effectiveEventForMessage(evt, model.NewUserMessage("selected"))
		})
		runtime.KeepAlive(projected)
		return allocs
	}
	small, large := measure(1), measure(128)
	assert.LessOrEqual(t, large, small+1, "discarded choices must not increase projection allocations")
}

func TestAppendProjectedToolResults(t *testing.T) {
	evt, messages := projectedToolResultsFixture(3)
	boundary := summaryview.Boundary{EventID: evt.ID, Timestamp: evt.Timestamp}
	var history projectedHistory
	appendProjectedHistory(&history, evt, boundary, messages, false)
	require.Equal(t, messages, history.messages)
	require.Len(t, history.items, len(messages))
	for i, item := range history.items {
		assert.Equal(t, boundary, item.Boundary)
		assert.Equal(t, messages[i], item.Message)
		assert.Equal(t, []model.Choice{{Message: messages[i]}}, item.EffectiveEvent.Choices)
		assert.Equal(t, evt.Extensions, item.EffectiveEvent.Extensions)
	}
	assert.Len(t, evt.Choices, len(messages))
}

var projectedToolResultsSink projectedHistory

func BenchmarkAppendProjectedToolResults(b *testing.B) {
	for _, count := range []int{1, 100, 200, 400, 800} {
		b.Run(fmt.Sprintf("tools=%d", count), func(b *testing.B) {
			evt, messages := projectedToolResultsFixture(count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var history projectedHistory
				appendProjectedHistory(&history, evt, summaryview.Boundary{}, messages, false)
				projectedToolResultsSink = history
			}
		})
	}
}

func BenchmarkContentRequestProcessorParallelToolResults(b *testing.B) {
	for _, count := range []int{100, 200, 400, 800} {
		b.Run(fmt.Sprintf("tools=%d", count), func(b *testing.B) {
			evt, _ := projectedToolResultsFixture(count)
			calls := make([]model.ToolCall, count)
			for i, choice := range evt.Choices {
				calls[i] = model.ToolCall{ID: choice.Message.ToolID, Type: "function",
					Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{}`)}}
			}
			callEvent := evt
			callEvent.ID = "calls"
			callEvent.Extensions = nil
			callEvent.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleAssistant, ToolCalls: calls,
			}}}}
			inv := agent.NewInvocation(
				agent.WithInvocationSession(&session.Session{Events: []event.Event{callEvent, evt}}),
				agent.WithInvocationBranch("agent"), agent.WithInvocationEventFilterKey("agent"),
			)
			inv.AgentName = "agent"
			processor := NewContentRequestProcessor(WithAddSessionSummary(true))
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				request := &model.Request{}
				processor.ProcessRequest(ctx, inv, request, nil)
				contentRequestMessageCountSink = len(request.Messages)
				if len(request.Messages) != count+1 {
					b.Fatalf("projected messages = %d, want %d", len(request.Messages), count+1)
				}
			}
		})
	}
}

func projectedToolResultsFixture(count int) (event.Event, []model.Message) {
	messages := make([]model.Message, count)
	choices := make([]model.Choice, count)
	args := make(map[string]string, count)
	for i := range choices {
		id := fmt.Sprintf("call-%d", i)
		messages[i] = model.Message{Role: model.RoleTool, ToolID: id, ToolName: "lookup", Content: "result"}
		choices[i] = model.Choice{Index: i, Message: messages[i]}
		args[id] = `{}`
	}
	payload, _ := json.Marshal(args)
	return event.Event{
		ID: "results", InvocationID: "invocation", RequestID: "request",
		Author: "agent", Branch: "agent", FilterKey: "agent", Timestamp: time.Unix(100, 0),
		Response:   &model.Response{Object: model.ObjectTypeToolResponse, Choices: choices},
		Extensions: map[string]json.RawMessage{event.ToolCallArgsExtensionKey: payload},
	}, messages
}
