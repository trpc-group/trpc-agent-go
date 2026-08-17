//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"bytes"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryfork"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	parallelInvocationViewSink  *agent.Invocation
	parallelInvocationBatchSink []*agent.Invocation
)

func BenchmarkParallelInvocationView(b *testing.B) {
	for _, historySize := range []int{256, 1024} {
		b.Run(fmt.Sprintf("history=%d", historySize), func(b *testing.B) {
			invocation := parallelInvocationBenchmarkInput(historySize)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				parallelInvocationViewSink = newParallelInvocationView(invocation)
			}
		})
	}
}

func BenchmarkParallelInvocationViewWithSession(b *testing.B) {
	for _, tc := range []struct {
		historySize   int
		sessionEvents int
		stateBytes    int
	}{
		{historySize: 256},
		{historySize: 256, sessionEvents: 256, stateBytes: 1024},
		{historySize: 1024, sessionEvents: 1024, stateBytes: 64 * 1024},
	} {
		name := fmt.Sprintf(
			"history=%d/session_events=%d/state_bytes=%d",
			tc.historySize,
			tc.sessionEvents,
			tc.stateBytes,
		)
		b.Run(name, func(b *testing.B) {
			invocation := parallelInvocationBenchmarkInputWithSession(
				tc.historySize,
				tc.sessionEvents,
				tc.stateBytes,
			)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				parallelInvocationViewSink = newParallelInvocationView(invocation)
			}
		})
	}
}

func BenchmarkParallelInvocationViewBatch(b *testing.B) {
	for _, fanout := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("history=256/fanout=%d", fanout), func(b *testing.B) {
			invocation := parallelInvocationBenchmarkInput(256)
			views := make([]*agent.Invocation, fanout)
			b.ReportAllocs()
			b.ReportMetric(float64(fanout), "views/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := range views {
					views[j] = newParallelInvocationView(invocation)
				}
				parallelInvocationBatchSink = views
			}
		})
	}
}

func parallelInvocationBenchmarkInput(historySize int) *agent.Invocation {
	invocation := agent.NewInvocation(agent.WithInvocationSession(&session.Session{}))
	messages := make([]model.Message, historySize)
	items := make([]summaryview.Item, historySize)
	for i := 0; i < historySize; i++ {
		arguments := bytes.Repeat([]byte{'a' + byte(i%26)}, 256)
		message := model.Message{
			Role:    model.RoleAssistant,
			Content: "parallel tool call history",
			ToolCalls: []model.ToolCall{{
				ID: fmt.Sprintf("call-%d", i),
				Function: model.FunctionDefinitionParam{
					Name:      "write_file",
					Arguments: arguments,
				},
			}},
		}
		messages[i] = message
		items[i] = summaryview.Item{
			Message: message,
			EffectiveEvent: event.Event{
				Response: &model.Response{Choices: []model.Choice{{Message: message}}},
				StateDelta: map[string][]byte{
					"history": bytes.Repeat([]byte{'v'}, 256),
				},
			},
		}
	}
	summaryview.AttachProjection(invocation, &summaryview.View{Items: items})
	summaryfork.Attach(invocation, &model.Request{Messages: messages})
	return invocation
}

func parallelInvocationBenchmarkInputWithSession(
	historySize int,
	sessionEvents int,
	stateBytes int,
) *agent.Invocation {
	invocation := parallelInvocationBenchmarkInput(historySize)
	invocation.Session.Events = make([]event.Event, sessionEvents)
	for i := range invocation.Session.Events {
		invocation.Session.Events[i] = event.Event{
			ID: fmt.Sprintf("session-event-%d", i),
		}
	}
	if stateBytes > 0 {
		invocation.Session.State = session.StateMap{
			"benchmark": bytes.Repeat([]byte{'s'}, stateBytes),
		}
	}
	return invocation
}
