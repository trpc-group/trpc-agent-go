//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type summaryInjectingService struct {
	session.Service
	mu    sync.Mutex
	calls int
}

func (s *summaryInjectingService) CreateSessionSummary(
	_ context.Context,
	sess *session.Session,
	filterKey string,
	_ bool,
) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	sess.SummariesMu.Lock()
	defer sess.SummariesMu.Unlock()
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	sess.Summaries[filterKey] = &session.Summary{
		Summary:   "compressed history",
		UpdatedAt: time.Now().Add(time.Minute),
	}
	return nil
}

func (s *summaryInjectingService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type summaryFailingService struct {
	session.Service
	mu    sync.Mutex
	calls int
	err   error
}

func (s *summaryFailingService) CreateSessionSummary(
	context.Context,
	*session.Session,
	string,
	bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *summaryFailingService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type compactingModel struct {
	name string
}

func (m *compactingModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *compactingModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func TestMaybeCompactContextBeforeLLM_RebuildsRequestWithSummary(t *testing.T) {
	modelName := "compact-retry-model"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)
	require.Len(t, req.Messages, 2)
	require.Contains(t, req.Messages[0].Content, longContent)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 1, service.Calls())
	require.NotSame(t, req, rebuilt)
	require.Len(t, rebuilt.Messages, 2)
	require.Equal(t, model.RoleSystem, rebuilt.Messages[0].Role)
	require.Contains(t, rebuilt.Messages[0].Content, "compressed history")
	require.Equal(t, "current", rebuilt.Messages[1].Content)
}

func TestMaybeCompactContextBeforeLLM_SkipsWithoutSummaryAwareProcessor(t *testing.T) {
	modelName := "compact-retry-no-summary"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
	)

	f := New(
		[]flow.RequestProcessor{
			&seedMessagesRequestProcessor{
				messages: []model.Message{
					model.NewUserMessage(strings.Repeat("payload ", 2000)),
				},
			},
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)
	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_SkipsWhenSummaryInjectionDisabled(t *testing.T) {
	modelName := "compact-retry-summary-disabled"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryInjectingService{Service: baseSvc}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(false),
				processor.WithEnableContextCompaction(true),
				processor.WithContextCompactionToolResultMaxTokens(10),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 0, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_SkipsWhenSummaryRefreshFails(t *testing.T) {
	modelName := "compact-retry-summary-error"
	model.RegisterModelContextWindow(modelName, 10000)

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	service := &summaryFailingService{
		Service: baseSvc,
		err:     context.DeadlineExceeded,
	}
	longContent := strings.Repeat("history ", 2000)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: "req-old",
				Timestamp: time.Now().Add(-time.Hour),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewUserMessage(longContent),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(service),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationModel(&compactingModel{name: modelName}),
		agent.WithInvocationEventFilterKey("branch/test"),
	)

	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{}
	f.preprocess(context.Background(), inv, req, nil)

	rebuilt := f.maybeCompactContextBeforeLLM(
		context.Background(),
		inv,
		req,
	)

	require.Equal(t, 1, service.Calls())
	require.Same(t, req, rebuilt)
}

func TestMaybeCompactContextBeforeLLM_InitialGuards(t *testing.T) {
	f := New(
		[]flow.RequestProcessor{
			processor.NewContentRequestProcessor(
				processor.WithAddSessionSummary(true),
			),
		},
		nil,
		Options{
			EnableContextCompaction:         true,
			ContextCompactionThresholdRatio: 0.2,
		},
	)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(inmemory.NewSessionService()),
	)
	t.Cleanup(func() {
		require.NoError(t, inv.SessionService.Close())
	})

	require.Nil(t, f.maybeCompactContextBeforeLLM(context.Background(), inv, nil))

	disabled := New(
		f.requestProcessors,
		nil,
		Options{EnableContextCompaction: false},
	)
	require.Same(t, req, disabled.maybeCompactContextBeforeLLM(context.Background(), inv, req))
	require.Same(t, req, f.maybeCompactContextBeforeLLM(context.Background(), nil, req))
	require.Same(t, req, f.maybeCompactContextBeforeLLM(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSessionService(inmemory.NewSessionService()),
		),
		req,
	))
	require.Same(t, req, f.maybeCompactContextBeforeLLM(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{}),
		),
		req,
	))
}

func TestSupportsSyncSummaryRetry(t *testing.T) {
	tests := []struct {
		name       string
		processors []flow.RequestProcessor
		want       bool
	}{
		{
			name: "summary aware content processor",
			processors: []flow.RequestProcessor{
				processor.NewContentRequestProcessor(
					processor.WithAddSessionSummary(true),
				),
			},
			want: true,
		},
		{
			name: "summary disabled",
			processors: []flow.RequestProcessor{
				processor.NewContentRequestProcessor(
					processor.WithAddSessionSummary(false),
				),
			},
			want: false,
		},
		{
			name: "non all timeline filter",
			processors: []flow.RequestProcessor{
				processor.NewContentRequestProcessor(
					processor.WithAddSessionSummary(true),
					processor.WithTimelineFilterMode(processor.TimelineFilterCurrentRequest),
				),
			},
			want: false,
		},
		{
			name: "non content processor",
			processors: []flow.RequestProcessor{
				&seedMessagesRequestProcessor{
					messages: []model.Message{model.NewUserMessage("hello")},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(tt.processors, nil, Options{})
			require.Equal(t, tt.want, f.supportsSyncSummaryRetry())
		})
	}
}

func TestNormalizeContextCompactionThresholdRatio(t *testing.T) {
	require.Equal(t, defaultContextCompactionThresholdRatio,
		normalizeContextCompactionThresholdRatio(0))
	require.Equal(t, defaultContextCompactionThresholdRatio,
		normalizeContextCompactionThresholdRatio(1.5))
	require.Equal(t, defaultContextCompactionThresholdRatio,
		normalizeContextCompactionThresholdRatio(-0.1))
	require.Equal(t, 0.5, normalizeContextCompactionThresholdRatio(0.5))
}

func TestContextCompactionThreshold(t *testing.T) {
	t.Run("caps to small model window", func(t *testing.T) {
		const modelName = "compact-threshold-small-window"
		model.RegisterModelContextWindow(modelName, 1024)

		inv := agent.NewInvocation(
			agent.WithInvocationModel(&compactingModel{name: modelName}),
		)
		require.Equal(t, 1024, contextCompactionThreshold(inv, 0.1))
	})

	t.Run("uses fallback window for unknown model", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationModel(&compactingModel{name: "compact-threshold-unknown"}),
		)
		require.Equal(t, 5734, contextCompactionThreshold(inv, 0))
	})
}

func TestShouldSyncCompactContext(t *testing.T) {
	const modelName = "compact-threshold-sync"
	model.RegisterModelContextWindow(modelName, 1024)

	inv := agent.NewInvocation(
		agent.WithInvocationModel(&compactingModel{name: modelName}),
	)

	require.False(t, shouldSyncCompactContext(context.Background(), nil, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}, 0.5))
	require.False(t, shouldSyncCompactContext(context.Background(), inv, nil, 0.5))
	require.False(t, shouldSyncCompactContext(context.Background(), inv, &model.Request{}, 0.5))

	require.False(t, shouldSyncCompactContext(context.Background(), inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage(strings.Repeat("a", 100))},
	}, 0.5))
	require.True(t, shouldSyncCompactContext(context.Background(), inv, &model.Request{
		Messages: []model.Message{model.NewUserMessage(strings.Repeat("a", 5000))},
	}, 0.5))
}
