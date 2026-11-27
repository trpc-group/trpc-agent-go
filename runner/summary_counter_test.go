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
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type summaryCounterModel struct {
	name         string
	calls        int
	requestRunes int
}

func (m *summaryCounterModel) Info() model.Info {
	return model.Info{Name: m.name, ContextWindow: 1_000_000}
}

func (m *summaryCounterModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.calls++
	for _, msg := range req.Messages {
		m.requestRunes += utf8.RuneCountInString(msg.Content)
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("Preserve the task and its pending steps."),
		}},
	}
	close(responses)
	return responses, nil
}

// Execute queued jobs inline to preserve the runner's request context and view
// while making both summary decisions and model-call assertions deterministic.
type inlineSummaryCounterService struct {
	session.Service
	syncAttempts int
}

func (s *inlineSummaryCounterService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, key string, force bool) error {
	return s.Service.CreateSessionSummary(ctx, sess, key, force)
}

func (s *inlineSummaryCounterService) CreateSessionSummary(ctx context.Context, sess *session.Session, key string, force bool) error {
	s.syncAttempts++
	return s.Service.CreateSessionSummary(ctx, sess, key, force)
}

func TestRunnerSummaryUsesConfiguredDefaultCounter(t *testing.T) {
	for _, tc := range []struct {
		name         string
		compact      bool
		explicit     model.TokenCounter
		wantCalls    int
		wantCompacts int
	}{
		{name: "automatic summary without compaction", wantCalls: 1},
		{name: "synchronous compaction", compact: true, wantCalls: 1, wantCompacts: 1},
		{name: "explicit agent counter wins", compact: true, explicit: model.NewSimpleTokenCounter()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary.SetTokenCounter(nil)
			t.Cleanup(func() { summary.SetTokenCounter(nil) })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			summaryModel := &summaryCounterModel{name: "summary-counter"}
			base := inmemory.NewSessionService(inmemory.WithSummarizer(
				summary.NewSummarizer(summaryModel,
					summary.WithContextThreshold(summary.WithContextThresholdRatio(0.6)),
				),
			))
			t.Cleanup(func() { require.NoError(t, base.Close()) })
			svc := &inlineSummaryCounterService{Service: base}
			key := session.Key{AppName: "counter", UserID: "user", SessionID: "session"}
			sess, err := svc.CreateSession(ctx, key, session.StateMap{})
			require.NoError(t, err)
			// 960k runes exceed the 600k threshold with /1.5, but not /4.
			content := strings.Repeat("中", 120_000)
			for i := 0; i < 8; i++ {
				role, author := model.RoleUser, "user"
				if i%2 == 1 {
					role, author = model.RoleAssistant, "counter"
				}
				require.NoError(t, svc.AppendEvent(ctx, sess, &event.Event{
					ID:           fmt.Sprintf("event-%d", i),
					InvocationID: fmt.Sprintf("invocation-%d", i/2),
					RequestID:    fmt.Sprintf("request-%d", i/2),
					Author:       author,
					FilterKey:    "counter",
					Timestamp:    time.Now().Add(time.Duration(i-10) * time.Second),
					Response: &model.Response{
						Object: model.ObjectTypeChatCompletion, Done: true,
						Choices: []model.Choice{{Message: model.Message{Role: role, Content: content}}},
					},
				}))
			}
			chatModel := &summaryCounterModel{name: "chat-counter"}
			llm := llmagent.New("counter",
				llmagent.WithModel(chatModel),
				llmagent.WithAddSessionSummary(true),
				llmagent.WithEnableContextCompaction(tc.compact),
				llmagent.WithContextCompactionThresholdRatio(0.6),
				llmagent.WithContextCompactionTokenCounter(tc.explicit),
			)
			r := runner.NewRunner("counter", llm, runner.WithSessionService(svc))
			t.Cleanup(func() { require.NoError(t, r.Close()) })
			// Existing agents must observe subsequent default-counter updates.
			summary.SetTokenCounter(model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1.5)))
			events, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage("Continue."))
			require.NoError(t, err)
			for evt := range events {
				if evt.Response != nil {
					require.Nil(t, evt.Error)
				}
			}
			require.Equal(t, 1, chatModel.calls)
			require.Equal(t, tc.wantCalls, summaryModel.calls)
			require.Equal(t, tc.wantCompacts, svc.syncAttempts)
			if tc.wantCompacts > 0 {
				require.Less(t, chatModel.requestRunes, 1000)
			} else {
				require.GreaterOrEqual(t, chatModel.requestRunes, 960_000)
			}
		})
	}
}
