//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/eventvisibility"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type completionSessionService struct {
	*mockSessionService
	append func(*event.Event) error
}

func (s completionSessionService) AppendEvent(_ context.Context, _ *session.Session, evt *event.Event, _ ...session.Option) error {
	return s.append(evt)
}

func TestRunnerPreservesProducerCompletion(t *testing.T) {
	for _, mode := range []string{"backfill", "replace ID", "mutate ID", "replacement completion"} {
		for _, appendFails := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "/append error", false: "/append success"}[appendFails], func(t *testing.T) {
				ctx := eventvisibility.WithCapture(context.Background())
				inv := agent.NewInvocation()
				original := event.NewResponseEvent(inv.InvocationID, "agent", &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("original")}}})
				original.RequiresCompletion = true
				originalID := original.ID
				notice := inv.AddNoticeChannel(ctx, agent.GetAppendEventNoticeKey(originalID))
				require.True(t, eventvisibility.Hide(ctx, originalID))
				inv.Plugins = plugin.MustNewManager(&testPlugin{name: "replace", reg: func(r *plugin.Registry) {
					r.OnEvent(func(_ context.Context, _ *agent.Invocation, e *event.Event) (*event.Event, error) {
						select {
						case <-notice:
							t.Error("completion before plugin processing")
						default:
						}
						if mode == "mutate ID" {
							e.ID = "replacement"
							e.RequiresCompletion = false
							return e, nil
						}
						updated := &event.Event{Response: e.Response, Author: e.Author, Timestamp: e.Timestamp, Version: e.Version}
						if mode != "backfill" {
							updated.ID = "replacement"
						}
						updated.RequiresCompletion = mode == "replacement completion"
						return updated, nil
					})
				}})
				var persisted *event.Event
				service := completionSessionService{mockSessionService: &mockSessionService{}, append: func(evt *event.Event) error {
					select {
					case <-notice:
						t.Error("completion before append attempt")
					default:
					}
					persisted = evt
					if appendFails {
						return errors.New("append failed")
					}
					return nil
				}}
				r := &runner{sessionService: service}
				loop := &eventLoopContext{sess: session.NewSession("app", "user", "session"), invocation: inv,
					processedEventCh: make(chan *event.Event, 1), streamFilter: graph.NewStreamModeFilter(false, nil)}
				require.NoError(t, r.processSingleAgentEvent(ctx, loop, original))
				require.NotNil(t, persisted)
				if mode == "backfill" {
					require.Equal(t, originalID, persisted.ID)
				} else {
					require.Equal(t, "replacement", persisted.ID)
				}
				select {
				case <-notice:
				default:
					t.Fatal("original producer was not acknowledged")
				}
				if mode == "replacement completion" {
					select {
					case <-inv.AddNoticeChannel(ctx, agent.GetAppendEventNoticeKey("replacement")):
					default:
						t.Fatal("replacement completion was not preserved")
					}
				}
				require.Empty(t, loop.processedEventCh, "a replacement must not expose hidden events")
				require.Empty(t, loop.fallbackChoices, "hidden messages must not reappear in runner completion")
			})
		}
	}
}
