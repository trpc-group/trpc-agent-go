//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryview

import (
	"bytes"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var summaryViewBenchmarkSink *View

func BenchmarkSnapshot(b *testing.B) {
	for _, historySize := range []int{16, 256, 1024} {
		b.Run(fmt.Sprintf("history=%d/state_delta_bytes=1024", historySize), func(b *testing.B) {
			invocation, _ := summaryViewBenchmarkInput(historySize, 1024)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				view, ok := Snapshot(invocation)
				if !ok {
					b.Fatal("summary view is missing")
				}
				summaryViewBenchmarkSink = view
			}
		})
	}
}

func BenchmarkFinalize(b *testing.B) {
	for _, historySize := range []int{16, 256, 1024} {
		b.Run(fmt.Sprintf("history=%d/state_delta_bytes=1024", historySize), func(b *testing.B) {
			invocation, request := summaryViewBenchmarkInput(historySize, 1024)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Finalize(invocation, request, historySize*32)
			}
		})
	}
}

func summaryViewBenchmarkInput(
	historySize int,
	stateDeltaBytes int,
) (*agent.Invocation, *model.Request) {
	invocation := agent.NewInvocation()
	messages := make([]model.Message, 1, historySize+1)
	messages[0] = model.NewSystemMessage("benchmark system prompt")
	items := make([]Item, historySize)
	for i := range items {
		message := model.Message{
			Role:    model.RoleAssistant,
			Content: fmt.Sprintf("model-visible history item %d", i),
			ToolCalls: []model.ToolCall{{
				ID: fmt.Sprintf("call-%d", i),
				Function: model.FunctionDefinitionParam{
					Name:      "benchmark_tool",
					Arguments: bytes.Repeat([]byte{'a' + byte(i%26)}, 64),
				},
			}},
		}
		messages = append(messages, message)
		items[i] = Item{
			Message: message,
			EffectiveEvent: event.Event{
				ID: fmt.Sprintf("event-%d", i),
				StateDelta: map[string][]byte{
					"benchmark": bytes.Repeat([]byte{'s'}, stateDeltaBytes),
				},
			},
			RequestIndex: i + 1,
		}
	}
	AttachProjection(invocation, &View{
		Items:                items,
		ContentRequestLength: len(messages),
	})
	return invocation, &model.Request{Messages: messages}
}
