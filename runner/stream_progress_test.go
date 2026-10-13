//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Only the synchronous compaction path may summarize in this fixture. Waiting
// for the consumer makes a buffered, post-summary start notification fail.
type gatedProgressSummaryService struct {
	session.Service
	release <-chan struct{}
	calls   int
}

func (s *gatedProgressSummaryService) CreateSessionSummary(
	ctx context.Context, sess *session.Session, key string, force bool,
) error {
	s.calls++
	select {
	case <-s.release:
		return s.Service.CreateSessionSummary(ctx, sess, key, force)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*gatedProgressSummaryService) EnqueueSummaryJob(
	context.Context, *session.Session, string, bool,
) error {
	return nil
}

type progressObserver struct {
	events []*event.Event
}

func (*progressObserver) Name() string { return "progress-observer" }

func (p *progressObserver) Register(r *plugin.Registry) {
	r.OnEvent(func(_ context.Context, _ *agent.Invocation, e *event.Event) (*event.Event, error) {
		if e.Response != nil && e.Object == model.ObjectTypePreprocessingStatus {
			p.events = append(p.events, e)
		}
		return nil, nil
	})
}

func TestRunnerStreamProgressCompaction(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		progress, diagnostics, disableDiagnosticsEvents bool
		modes                                           []agent.StreamMode
		wantEvents, wantMessage                         bool
	}{
		{name: "messages and progress without tracing", progress: true,
			modes: []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeProgress}, wantEvents: true, wantMessage: true},
		{name: "messages only", progress: true,
			modes: []agent.StreamMode{agent.StreamModeMessages}, wantMessage: true},
		{name: "progress only", progress: true,
			modes: []agent.StreamMode{agent.StreamModeProgress}, wantEvents: true},
		{name: "subscription does not enable production",
			modes: []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeProgress}, wantMessage: true},
		{name: "diagnostics alone do not become progress", diagnostics: true,
			modes: []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeProgress}, wantMessage: true},
		{name: "both extensions share one event", progress: true, diagnostics: true,
			modes: []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeProgress, agent.StreamModeProgress}, wantEvents: true, wantMessage: true},
		{name: "diagnostic emission independent", progress: true, diagnostics: true, disableDiagnosticsEvents: true,
			modes: []agent.StreamMode{agent.StreamModeProgress}, wantEvents: true},
		{name: "default forwarding", progress: true, wantEvents: true, wantMessage: true},
		{name: "legacy diagnostics unchanged", diagnostics: true, wantEvents: true, wantMessage: true},
		{name: "default production unchanged", wantMessage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			release := make(chan struct{})
			if !tc.wantEvents {
				close(release)
			}
			summaryModel := &summaryCounterModel{name: "progress-summary"}
			base := inmemory.NewSessionService(inmemory.WithSummarizer(
				summary.NewSummarizer(summaryModel, summary.WithTokenThreshold(1))))
			defer base.Close()
			svc := &gatedProgressSummaryService{Service: base, release: release}
			key := session.Key{AppName: "progress", UserID: "user", SessionID: "session"}
			sess, err := svc.CreateSession(ctx, key, session.StateMap{})
			require.NoError(t, err)
			for i := 0; i < 8; i++ {
				role, author := model.RoleUser, "user"
				if i%2 == 1 {
					role, author = model.RoleAssistant, "progress"
				}
				require.NoError(t, svc.AppendEvent(ctx, sess, &event.Event{
					ID: fmt.Sprintf("event-%d", i), Author: author,
					InvocationID: fmt.Sprintf("invocation-%d", i/2), RequestID: fmt.Sprintf("request-%d", i/2),
					FilterKey: "progress", Timestamp: time.Now().Add(time.Duration(i-10) * time.Second),
					Response: &model.Response{Object: model.ObjectTypeChatCompletion, Done: true,
						Choices: []model.Choice{{Message: model.Message{Role: role, Content: strings.Repeat("previous context ", 200)}}}},
				}))
			}
			chatModel := &summaryCounterModel{name: "progress-chat"}
			llm := llmagent.New("progress", llmagent.WithModel(chatModel),
				llmagent.WithAddSessionSummary(true), llmagent.WithEnableContextCompaction(true),
				llmagent.WithContextCompactionThresholdRatio(0.2))
			observer := &progressObserver{}
			r := runner.NewRunner(key.AppName, llm, runner.WithSessionService(svc), runner.WithPlugins(observer))
			defer r.Close()
			opts := []agent.RunOption{agent.WithModelContextWindow(4096), agent.WithProgressEvents(tc.progress),
				agent.WithLatencyDiagnostics(tc.diagnostics), agent.WithDisableTracing(true)}
			if tc.modes != nil {
				opts = append(opts, agent.WithStreamMode(tc.modes...))
			}
			if tc.disableDiagnosticsEvents {
				opts = append(opts, agent.WithLatencyDiagnosticsEvents(false))
			}
			events, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage("Continue."), opts...)
			require.NoError(t, err)
			var sequence []string
			var notifications []*event.Event
			var operationID string
			for e := range events {
				require.NotNil(t, e.Response)
				require.Nil(t, e.Error)
				switch e.Object {
				case model.ObjectTypePreprocessingStatus:
					require.True(t, tc.wantEvents)
					d, hasDiagnostic, err := event.GetExtension[event.LatencyDiagnostic](e, event.LatencyDiagnosticExtensionKey)
					require.NoError(t, err)
					require.Equal(t, tc.diagnostics && !tc.disableDiagnosticsEvents, hasDiagnostic)
					p, hasProgress, err := event.GetExtension[event.Progress](e, event.ProgressExtensionKey)
					require.NoError(t, err)
					require.Equal(t, tc.progress, hasProgress)
					status := d.Status
					if hasProgress {
						status = string(p.Status)
						require.Equal(t, "context_compaction", p.Kind)
						require.NotEmpty(t, p.OperationID)
						require.Nil(t, p.Percent)
						require.NotEmpty(t, e.InvocationID)
						if operationID == "" {
							operationID = p.OperationID
						}
						require.Equal(t, operationID, p.OperationID)
					}
					if status == "started" {
						require.Empty(t, notifications)
						close(release)
					} else {
						require.Equal(t, "completed", status)
					}
					notifications = append(notifications, e)
					sequence = append(sequence, status)
				case model.ObjectTypeChatCompletion:
					sequence = append(sequence, "message")
				case model.ObjectTypeRunnerCompletion:
					sequence = append(sequence, "completion")
				default:
					require.Nil(t, tc.modes, "unexpected filtered event: %s", e.Object)
				}
			}
			require.NoError(t, ctx.Err())
			var want []string
			if tc.wantEvents {
				want = append(want, "started", "completed")
				require.Len(t, notifications, 2)
				require.NotEqual(t, notifications[0].ID, notifications[1].ID)
				for i, e := range notifications {
					require.Same(t, observer.events[i], e)
				}
			}
			if tc.wantMessage {
				want = append(want, "message")
			}
			want = append(want, "completion")
			require.Equal(t, want, sequence)
			if tc.progress || tc.diagnostics && !tc.disableDiagnosticsEvents {
				require.Len(t, observer.events, 2, "plugins observe status before stream filtering")
			} else {
				require.Empty(t, observer.events)
			}
			require.Equal(t, 1, svc.calls)
			require.Equal(t, 1, summaryModel.calls)
			require.Equal(t, 1, chatModel.calls)
			persisted, err := svc.GetSession(ctx, key)
			require.NoError(t, err)
			for _, e := range persisted.GetEvents() {
				require.NotEqual(t, model.ObjectTypePreprocessingStatus, e.Object, "progress must not enter conversation history")
			}
		})
	}
}

func TestRunnerStreamProgressGraphUnion(t *testing.T) {
	for _, modes := range [][]agent.StreamMode{
		{agent.StreamModeProgress},
		{agent.StreamModeCustom, agent.StreamModeProgress},
		{agent.StreamModeMessages, agent.StreamModeCustom, agent.StreamModeProgress},
	} {
		sg := graph.NewStateGraph(graph.NewStateSchema())
		sg.AddNode("work", func(ctx context.Context, state graph.State) (any, error) {
			emitter := graph.GetEventEmitterWithContext(ctx, state)
			if err := emitter.EmitProgress(0, "starting"); err != nil {
				return nil, err
			}
			if err := emitter.EmitProgress(100, "last snapshot"); err != nil {
				return nil, err
			}
			return graph.State{}, nil
		})
		sg.SetEntryPoint("work")
		sg.SetFinishPoint("work")
		ga, err := graphagent.New("graph", sg.MustCompile())
		require.NoError(t, err)
		r := runner.NewRunner("progress-union", ga)
		defer r.Close()
		events, err := r.Run(context.Background(), "user", "session", model.NewUserMessage("work"),
			agent.WithProgressEvents(true), agent.WithStreamMode(modes...))
		require.NoError(t, err)
		var ids []string
		var percentages []float64
		var operationID string
		for e := range events {
			require.Nil(t, e.Error)
			p, ok, err := event.GetExtension[event.Progress](e, event.ProgressExtensionKey)
			require.NoError(t, err)
			if !ok {
				continue
			}
			require.NotContains(t, ids, e.ID)
			ids = append(ids, e.ID)
			require.Equal(t, graph.ObjectTypeGraphNodeCustom, e.Object)
			require.NotEmpty(t, e.StateDelta[graph.MetadataKeyNodeCustom])
			if operationID == "" {
				operationID = p.OperationID
			}
			require.Equal(t, operationID, p.OperationID)
			require.Equal(t, event.ProgressStatusRunning, p.Status)
			require.NotNil(t, p.Percent)
			percentages = append(percentages, *p.Percent)
		}
		require.Equal(t, []float64{0, 100}, percentages, "union must forward each event once in source order")
	}
}
