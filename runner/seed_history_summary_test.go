//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type summaryMessageCapturingAgent struct {
	name       string
	addSummary bool
	messages   []model.Message
}

func (a *summaryMessageCapturingAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *summaryMessageCapturingAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *summaryMessageCapturingAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *summaryMessageCapturingAgent) Tools() []tool.Tool {
	return nil
}

func (a *summaryMessageCapturingAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if a.addSummary {
		events := inv.Session.Events
		last := events[len(events)-1]
		filterKey := inv.GetEventFilterKey()
		if inv.Session.Summaries == nil {
			inv.Session.Summaries = make(map[string]*session.Summary)
		}
		inv.Session.Summaries[filterKey] = &session.Summary{
			Summary:   "covered history",
			UpdatedAt: last.Timestamp,
			Boundary: session.NewSummaryBoundaryWithEventID(
				filterKey,
				last.Timestamp,
				last.ID,
			),
		}
	}

	req := &model.Request{}
	processor.NewContentRequestProcessor(
		processor.WithAddSessionSummary(a.addSummary),
	).ProcessRequest(ctx, inv, req, nil)
	a.messages = append([]model.Message(nil), req.Messages...)

	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func TestRunner_Run_SeedHistoryRespectsMidRunSummary(t *testing.T) {
	tests := []struct {
		name       string
		run        func(Runner) (<-chan *event.Event, error)
		want       []model.Message
		addSummary bool
	}{
		{
			name: "seed history is ordinary history at the summary cutoff",
			run: func(r Runner) (<-chan *event.Event, error) {
				return RunWithMessages(
					context.Background(),
					r,
					"user",
					"session",
					[]model.Message{
						model.NewUserMessage("current"),
						model.NewAssistantMessage("old answer"),
						model.NewUserMessage("current"),
					},
				)
			},
			want: []model.Message{
				model.NewUserMessage("current"),
			},
			addSummary: true,
		},
		{
			name: "rewritten current turn remains after the summary cutoff",
			run: func(r Runner) (<-chan *event.Event, error) {
				return r.Run(
					context.Background(),
					"user",
					"session",
					model.NewUserMessage("original"),
					agent.WithMessages([]model.Message{
						model.NewUserMessage("old question"),
						model.NewAssistantMessage("old answer"),
						model.NewUserMessage("original"),
					}),
					agent.WithUserMessageRewriter(func(
						context.Context,
						*agent.UserMessageRewriteArgs,
					) ([]model.Message, error) {
						return []model.Message{
							model.NewUserMessage("current context"),
							model.NewUserMessage("rewritten"),
						}, nil
					}),
				)
			},
			want: []model.Message{
				model.NewUserMessage("current context"),
				model.NewUserMessage("rewritten"),
			},
			addSummary: true,
		},
		{
			name: "seed history is unchanged without a summary cutoff",
			run: func(r Runner) (<-chan *event.Event, error) {
				return RunWithMessages(
					context.Background(),
					r,
					"user",
					"session",
					[]model.Message{
						model.NewUserMessage("old question"),
						model.NewAssistantMessage("old answer"),
						model.NewUserMessage("current"),
					},
				)
			},
			want: []model.Message{
				model.NewUserMessage("old question"),
				model.NewAssistantMessage("old answer"),
				model.NewUserMessage("current"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &summaryMessageCapturingAgent{
				name:       "capture",
				addSummary: tt.addSummary,
			}
			r := NewRunner(
				"app",
				capture,
				WithSessionService(sessioninmemory.NewSessionService()),
			)
			events, err := tt.run(r)
			require.NoError(t, err)
			for range events {
			}
			if tt.addSummary {
				require.Len(t, capture.messages, len(tt.want)+1)
				require.Equal(t, model.RoleSystem, capture.messages[0].Role)
				require.Contains(t, capture.messages[0].Content, "covered history")
				require.Equal(t, tt.want, capture.messages[1:])
				return
			}
			require.Equal(t, tt.want, capture.messages)
		})
	}
}
