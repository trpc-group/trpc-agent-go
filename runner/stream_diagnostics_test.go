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
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Only the synchronous compaction path may summarize in this fixture. Waiting
// for the consumer makes a buffered, post-summary start notification fail.
type gatedDiagnosticSummaryService struct {
	session.Service
	release <-chan struct{}
	calls   int
}

func (s *gatedDiagnosticSummaryService) CreateSessionSummary(
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

func (*gatedDiagnosticSummaryService) EnqueueSummaryJob(
	context.Context, *session.Session, string, bool,
) error {
	return nil
}

type diagnosticObserver struct {
	events []*event.Event
}

func (*diagnosticObserver) Name() string { return "diagnostic-observer" }

func (p *diagnosticObserver) Register(r *plugin.Registry) {
	r.OnEvent(func(_ context.Context, _ *agent.Invocation, e *event.Event) (*event.Event, error) {
		if e.Response != nil && e.Object == model.ObjectTypePreprocessingStatus {
			p.events = append(p.events, e)
		}
		return nil, nil
	})
}

func TestRunnerStreamLatencyDiagnosticsCompaction(t *testing.T) {
	for _, tc := range []struct {
		name          string
		diagnostics   bool
		disableEmit   bool
		modes         []agent.StreamMode
		overrideModes []agent.StreamMode
		wantEvents    bool
		wantMessage   bool
	}{
		{
			name: "messages and diagnostics", diagnostics: true,
			modes:      []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeLatencyDiagnostics},
			wantEvents: true, wantMessage: true,
		},
		{
			name: "combined repeated modes forward each event once", diagnostics: true,
			modes: []agent.StreamMode{
				agent.StreamModeMessages, agent.StreamModeLatencyDiagnostics,
				agent.StreamModeCustom, agent.StreamModeLatencyDiagnostics,
			},
			wantEvents: true, wantMessage: true,
		},
		{
			name: "later selection replaces prior selection", diagnostics: true,
			modes:         []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeLatencyDiagnostics},
			overrideModes: []agent.StreamMode{agent.StreamModeMessages},
			wantMessage:   true,
		},
		{
			name: "messages only", diagnostics: true,
			modes: []agent.StreamMode{agent.StreamModeMessages}, wantMessage: true,
		},
		{
			name: "diagnostics only", diagnostics: true,
			modes: []agent.StreamMode{agent.StreamModeLatencyDiagnostics}, wantEvents: true,
		},
		{
			name:        "subscription does not enable diagnostics",
			modes:       []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeLatencyDiagnostics},
			wantMessage: true,
		},
		{
			name: "event emission disabled", diagnostics: true, disableEmit: true,
			modes:       []agent.StreamMode{agent.StreamModeMessages, agent.StreamModeLatencyDiagnostics},
			wantMessage: true,
		},
		{
			name: "default forwarding", diagnostics: true,
			wantEvents: true, wantMessage: true,
		},
		{
			name: "default production unchanged", wantMessage: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			release := make(chan struct{})
			if !tc.wantEvents {
				close(release)
			}
			summaryModel := &summaryCounterModel{name: "diagnostic-summary"}
			base := inmemory.NewSessionService(inmemory.WithSummarizer(
				summary.NewSummarizer(summaryModel, summary.WithTokenThreshold(1)),
			))
			defer base.Close()
			svc := &gatedDiagnosticSummaryService{Service: base, release: release}
			key := session.Key{AppName: "diagnostics", UserID: "user", SessionID: "session"}
			sess, err := svc.CreateSession(ctx, key, session.StateMap{})
			require.NoError(t, err)
			for i := 0; i < 8; i++ {
				role, author := model.RoleUser, "user"
				if i%2 == 1 {
					role, author = model.RoleAssistant, "diagnostics"
				}
				require.NoError(t, svc.AppendEvent(ctx, sess, &event.Event{
					ID: fmt.Sprintf("event-%d", i), Author: author,
					InvocationID: fmt.Sprintf("invocation-%d", i/2),
					RequestID:    fmt.Sprintf("request-%d", i/2),
					FilterKey:    "diagnostics",
					Timestamp:    time.Now().Add(time.Duration(i-10) * time.Second),
					Response: &model.Response{
						Object: model.ObjectTypeChatCompletion, Done: true,
						Choices: []model.Choice{{Message: model.Message{
							Role: role, Content: strings.Repeat("previous context ", 200),
						}}},
					},
				}))
			}
			chatModel := &summaryCounterModel{name: "diagnostic-chat"}
			llm := llmagent.New("diagnostics", llmagent.WithModel(chatModel),
				llmagent.WithAddSessionSummary(true),
				llmagent.WithEnableContextCompaction(true),
				llmagent.WithContextCompactionThresholdRatio(0.2),
			)
			observer := &diagnosticObserver{}
			r := runner.NewRunner(key.AppName, llm,
				runner.WithSessionService(svc), runner.WithPlugins(observer))
			defer r.Close()
			opts := []agent.RunOption{
				agent.WithModelContextWindow(4096), agent.WithLatencyDiagnostics(tc.diagnostics),
			}
			if tc.modes != nil {
				opts = append(opts, agent.WithStreamMode(tc.modes...))
			}
			if tc.overrideModes != nil {
				opts = append(opts, agent.WithStreamMode(tc.overrideModes...))
			}
			if tc.disableEmit {
				opts = append(opts, agent.WithLatencyDiagnosticsEvents(false))
			}
			events, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage("Continue."), opts...)
			require.NoError(t, err)
			var sequence []string
			var diagnostics []*event.Event
			for e := range events {
				require.NotNil(t, e.Response)
				require.Nil(t, e.Error)
				switch e.Object {
				case model.ObjectTypePreprocessingStatus:
					d, ok, err := event.GetExtension[event.LatencyDiagnostic](e, event.LatencyDiagnosticExtensionKey)
					require.NoError(t, err)
					require.True(t, ok)
					require.Equal(t, "context_compaction", d.Stage)
					require.True(t, tc.wantEvents)
					if d.Status == "started" {
						require.Empty(t, diagnostics)
						close(release)
					} else {
						require.Equal(t, "completed", d.Status)
						require.NotNil(t, d.Updated)
						require.True(t, *d.Updated)
					}
					diagnostics = append(diagnostics, e)
					sequence = append(sequence, d.Status)
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
				require.Len(t, diagnostics, 2)
				for i, e := range diagnostics {
					require.Same(t, observer.events[i], e)
				}
			}
			if tc.wantMessage {
				want = append(want, "message")
			}
			want = append(want, "completion")
			require.Equal(t, want, sequence)
			if tc.diagnostics && !tc.disableEmit {
				require.Len(t, observer.events, 2, "plugins observe diagnostics before stream filtering")
			} else {
				require.Empty(t, observer.events)
			}
			require.Equal(t, 1, svc.calls)
			require.Equal(t, 1, summaryModel.calls)
			require.Equal(t, 1, chatModel.calls)
			persisted, err := svc.GetSession(ctx, key)
			require.NoError(t, err)
			for _, e := range persisted.GetEvents() {
				require.NotEqual(t, model.ObjectTypePreprocessingStatus, e.Object,
					"status-only diagnostics must not enter conversation history")
			}
		})
	}
}
