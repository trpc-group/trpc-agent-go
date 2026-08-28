//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func BenchmarkRunnerLatestTurnReplacementCapabilityAgentLoop(b *testing.B) {
	services := []struct {
		name    string
		service session.Service
	}{
		{name: "base", service: &benchmarkSessionService{}},
		{
			name: "replacement_capable",
			service: &benchmarkReplacementSessionService{
				benchmarkSessionService: &benchmarkSessionService{},
			},
		},
	}
	for _, service := range services {
		for _, eventCount := range []int{1, 100} {
			b.Run(fmt.Sprintf("%s/events_%d", service.name, eventCount), func(b *testing.B) {
				benchmarkRunnerAgentLoop(
					b,
					service.service,
					eventCount,
					agent.WithRequestID("benchmark-request"),
				)
			})
		}
	}
}

func BenchmarkRunnerLatestTurnReplacementAgentLoop(b *testing.B) {
	for _, eventCount := range []int{1, 100} {
		b.Run(fmt.Sprintf("events_%d", eventCount), func(b *testing.B) {
			benchmarkRunnerAgentLoop(
				b,
				&benchmarkReplacementSessionService{
					benchmarkSessionService: &benchmarkSessionService{},
				},
				eventCount,
				agent.WithRequestID("benchmark-request"),
				agent.WithLatestTurnReplacement("benchmark-previous-request"),
			)
		})
	}
}

func benchmarkRunnerAgentLoop(
	b *testing.B,
	service session.Service,
	eventCount int,
	runOptions ...agent.RunOption,
) {
	ctx := context.Background()
	r := NewRunner(
		"benchmark-app",
		newBenchmarkAgent(eventCount),
		WithSessionService(service),
	)
	message := model.NewUserMessage("hello")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events, err := r.Run(
			ctx,
			"benchmark-user",
			"benchmark-session",
			message,
			runOptions...,
		)
		if err != nil {
			b.Fatal(err)
		}
		for range events {
		}
	}
}

type benchmarkAgent struct {
	events []*event.Event
}

func newBenchmarkAgent(eventCount int) *benchmarkAgent {
	events := make([]*event.Event, eventCount)
	for i := range events {
		events[i] = &event.Event{
			Author: "benchmark-agent",
			ID:     fmt.Sprintf("benchmark-event-%d", i),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("ok"),
				}},
			},
		}
	}
	return &benchmarkAgent{events: events}
}

func (*benchmarkAgent) Info() agent.Info {
	return agent.Info{Name: "benchmark-agent"}
}

func (*benchmarkAgent) SubAgents() []agent.Agent {
	return nil
}

func (*benchmarkAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (*benchmarkAgent) Tools() []tool.Tool {
	return nil
}

func (a *benchmarkAgent) Run(
	_ context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event, len(a.events))
	for _, evt := range a.events {
		evt.InvocationID = invocation.InvocationID
		events <- evt
	}
	close(events)
	return events, nil
}

type benchmarkSessionService struct {
	session.Service
}

type benchmarkReplacementSessionService struct {
	*benchmarkSessionService
}

var _ session.RewindService = (*benchmarkReplacementSessionService)(nil)

func (*benchmarkSessionService) GetSession(
	_ context.Context,
	key session.Key,
	_ ...session.Option,
) (*session.Session, error) {
	return session.NewSession(key.AppName, key.UserID, key.SessionID), nil
}

func (*benchmarkSessionService) AppendEvent(
	_ context.Context,
	_ *session.Session,
	_ *event.Event,
	_ ...session.Option,
) error {
	return nil
}

func (*benchmarkSessionService) EnqueueSummaryJob(
	_ context.Context,
	_ *session.Session,
	_ string,
	_ bool,
) error {
	return nil
}

func (s *benchmarkReplacementSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	sess, err := s.benchmarkSessionService.GetSession(ctx, key, opts...)
	if err == nil {
		sessionrevision.SetGeneration(sess, 1)
	}
	return sess, err
}

func (*benchmarkReplacementSessionService) Rewind(
	_ context.Context,
	req sessionrevision.RewindRequest,
) (*session.RewindResult, error) {
	sess := session.NewSession(
		req.Key.AppName,
		req.Key.UserID,
		req.Key.SessionID,
	)
	sessionrevision.SetGeneration(sess, 1)
	return &session.RewindResult{
		Session: sess,
	}, nil
}
