//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var runnerAgentLoopEventCountSink int

func BenchmarkRunnerAgentLoop(b *testing.B) {
	for _, eventCount := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("events=%d", eventCount), func(b *testing.B) {
			ctx := context.Background()
			r := NewRunner(
				"benchmark-app",
				newRunnerAgentLoopBenchmarkAgent(eventCount),
				WithSessionService(&runnerAgentLoopBenchmarkSessionService{}),
			)
			message := model.NewUserMessage("benchmark request")

			b.ReportAllocs()
			b.ReportMetric(float64(eventCount), "events/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				events, err := r.Run(
					ctx,
					"benchmark-user",
					"benchmark-session",
					message,
					agent.WithRequestID("benchmark-request"),
				)
				if err != nil {
					b.Fatal(err)
				}
				count := 0
				for range events {
					count++
				}
				runnerAgentLoopEventCountSink = count
			}
		})
	}
}

type runnerAgentLoopBenchmarkAgent struct {
	events []*event.Event
}

func newRunnerAgentLoopBenchmarkAgent(eventCount int) *runnerAgentLoopBenchmarkAgent {
	events := make([]*event.Event, eventCount)
	for i := range events {
		events[i] = &event.Event{
			ID:     fmt.Sprintf("benchmark-event-%d", i),
			Author: "benchmark-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("benchmark response"),
				}},
			},
		}
	}
	return &runnerAgentLoopBenchmarkAgent{events: events}
}

func (*runnerAgentLoopBenchmarkAgent) Info() agent.Info {
	return agent.Info{Name: "benchmark-agent"}
}

func (*runnerAgentLoopBenchmarkAgent) SubAgents() []agent.Agent {
	return nil
}

func (*runnerAgentLoopBenchmarkAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (*runnerAgentLoopBenchmarkAgent) Tools() []tool.Tool {
	return nil
}

func (a *runnerAgentLoopBenchmarkAgent) Run(
	_ context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event, len(a.events))
	for _, evt := range a.events {
		cloned := *evt
		cloned.InvocationID = invocation.InvocationID
		events <- &cloned
	}
	close(events)
	return events, nil
}

type runnerAgentLoopBenchmarkSessionService struct {
	session.Service
}

func (*runnerAgentLoopBenchmarkSessionService) GetSession(
	_ context.Context,
	key session.Key,
	_ ...session.Option,
) (*session.Session, error) {
	return session.NewSession(key.AppName, key.UserID, key.SessionID), nil
}

func (*runnerAgentLoopBenchmarkSessionService) AppendEvent(
	_ context.Context,
	_ *session.Session,
	_ *event.Event,
	_ ...session.Option,
) error {
	return nil
}

func (*runnerAgentLoopBenchmarkSessionService) EnqueueSummaryJob(
	_ context.Context,
	_ *session.Session,
	_ string,
	_ bool,
) error {
	return nil
}
