//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var contentRequestMessageCountSink int

func BenchmarkContentRequestProcessorProcessRequest(b *testing.B) {
	for _, historySize := range []int{16, 256, 1024} {
		b.Run(fmt.Sprintf("history=%d", historySize), func(b *testing.B) {
			ctx := context.Background()
			invocation := contentRequestBenchmarkInvocation(historySize)
			processor := NewContentRequestProcessor()
			events := make(chan *event.Event, 1)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				request := &model.Request{
					Messages: []model.Message{
						model.NewSystemMessage("benchmark system prompt"),
					},
				}
				processor.ProcessRequest(ctx, invocation, request, events)
				contentRequestMessageCountSink = len(request.Messages)
				<-events
			}
		})
	}
}

func contentRequestBenchmarkInvocation(historySize int) *agent.Invocation {
	const agentName = "benchmark-agent"
	events := make([]event.Event, historySize)
	for i := range events {
		role := model.RoleAssistant
		if i%2 == 0 {
			role = model.RoleUser
		}
		events[i] = event.Event{
			ID:           fmt.Sprintf("event-%d", i),
			RequestID:    fmt.Sprintf("request-%d", i/2),
			InvocationID: fmt.Sprintf("history-invocation-%d", i/2),
			Author:       agentName,
			Branch:       agentName,
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    role,
						Content: fmt.Sprintf("history message %d", i),
					},
				}},
			},
		}
	}
	invocation := agent.NewInvocation(
		agent.WithInvocationID("benchmark-invocation"),
		agent.WithInvocationBranch(agentName),
		agent.WithInvocationEventFilterKey(agentName),
		agent.WithInvocationSession(&session.Session{Events: events}),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	invocation.AgentName = agentName
	return invocation
}
