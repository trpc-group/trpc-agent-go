//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hunyuan_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/hunyuan"
	ihunyuan "trpc.group/trpc-go/trpc-agent-go/model/hunyuan/internal/hunyuan"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type recordingSummarySessionService struct {
	session.Service
	mu    sync.Mutex
	calls []summaryEnqueueSnapshot
}

type summaryEnqueueSnapshot struct {
	viewPresent bool
	view        *summaryview.View
	forkPresent bool
	fork        *model.Request
}

func (s *recordingSummarySessionService) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	view, viewOK := summaryview.FromContext(ctx)
	fork, forkOK := summary.CacheSafeForkRequestFromContext(ctx)
	s.mu.Lock()
	s.calls = append(s.calls, summaryEnqueueSnapshot{
		viewPresent: viewOK,
		view:        view,
		forkPresent: forkOK,
		fork:        fork,
	})
	s.mu.Unlock()
	return nil
}

func (s *recordingSummarySessionService) latest() summaryEnqueueSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func newHunyuanHTTPRecordingServer(
	t *testing.T,
) (*httptest.Server, func() []*ihunyuan.ChatCompletionMessageParam) {
	t.Helper()
	var (
		mu       sync.Mutex
		messages []*ihunyuan.ChatCompletionMessageParam
	)
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		var body ihunyuan.ChatCompletionNewParams
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		messages = body.Messages
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"Response": ihunyuan.ChatCompletionResponse{
				Id: "hunyuan-resp-1",
				Choices: []*ihunyuan.ChatCompletionResponseChoice{{
					Index:        0,
					FinishReason: "stop",
					Message: &ihunyuan.ChatCompletionMessageParam{
						Role:    "assistant",
						Content: "ok",
					},
				}},
			},
		}))
	}))
	t.Cleanup(server.Close)
	return server, func() []*ihunyuan.ChatCompletionMessageParam {
		mu.Lock()
		defer mu.Unlock()
		return append([]*ihunyuan.ChatCompletionMessageParam(nil), messages...)
	}
}

func seedHunyuanSessionMessages(
	t *testing.T,
	ctx context.Context,
	svc session.Service,
	key session.Key,
	filterKey string,
	agentName string,
	msgs []model.Message,
) {
	t.Helper()
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	for i, msg := range msgs {
		author := agentName
		if msg.Role == model.RoleUser {
			author = "user"
		}
		ev := event.NewResponseEvent(
			fmt.Sprintf("seed-%d", i),
			author,
			&model.Response{
				Done:    true,
				Choices: []model.Choice{{Message: msg}},
			},
		)
		ev.FilterKey = filterKey
		require.NoError(t, svc.AppendEvent(ctx, sess, ev))
	}
}

func TestHunyuanHTTP_PlaceholderThenChatRequestCallbackRewriteInvalidatesSummarySnapshots(
	t *testing.T,
) {
	history := []model.Message{
		model.NewUserMessage("old question"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call_1",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"x"}`),
				},
			}},
		},
		model.NewToolMessage("call_1", "search", "tool output"),
	}

	tests := []struct {
		name        string
		callback    hunyuan.ChatRequestCallbackFunc
		wantSentLen int
		wantBound   bool
		wantFork    bool
	}{
		{
			name: "callback removing messages invalidates view and fork",
			callback: func(
				_ context.Context,
				req *ihunyuan.ChatCompletionNewParams,
			) {
				req.Messages = []*ihunyuan.ChatCompletionMessageParam{
					req.Messages[0],
					req.Messages[len(req.Messages)-1],
				}
			},
			wantSentLen: 2,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "callback rewriting message in place invalidates view and fork",
			callback: func(
				_ context.Context,
				req *ihunyuan.ChatCompletionNewParams,
			) {
				req.Messages[len(req.Messages)-1].Content = "rewritten question"
			},
			wantSentLen: 5,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "read-only callback preserves rebased view and fork",
			callback: func(
				_ context.Context,
				req *ihunyuan.ChatCompletionNewParams,
			) {
				_ = len(req.Messages)
			},
			wantSentLen: 5,
			wantBound:   true,
			wantFork:    true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			server, sentMessages := newHunyuanHTTPRecordingServer(t)
			recSvc := &recordingSummarySessionService{
				Service: inmemory.NewSessionService(),
			}
			key := session.Key{
				AppName:   "app",
				UserID:    fmt.Sprintf("hy-u-%d", i),
				SessionID: fmt.Sprintf("hy-s-%d", i),
			}
			seedHunyuanSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

			m := hunyuan.New(
				"hunyuan-lite",
				hunyuan.WithSecretId("test-secret-id"),
				hunyuan.WithSecretKey("test-secret-key"),
				hunyuan.WithBaseUrl(server.URL),
				hunyuan.WithHost("test-host"),
				hunyuan.WithEnableTokenTailoring(true),
				hunyuan.WithMaxInputTokens(10_000),
				hunyuan.WithChatRequestCallback(tt.callback),
			)
			ag := llmagent.New(
				"qa",
				llmagent.WithModel(m),
				llmagent.WithInstruction("system prompt"),
				llmagent.WithAddSessionSummary(true),
			)
			r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
			t.Cleanup(func() { _ = r.Close() })

			ch, err := r.Run(
				ctx,
				key.UserID,
				key.SessionID,
				model.NewUserMessage("current question"),
			)
			require.NoError(t, err)
			for evt := range ch {
				if evt != nil && evt.Error != nil {
					t.Fatalf(
						"unexpected event error: %s: %s",
						evt.Error.Type,
						evt.Error.Message,
					)
				}
			}

			require.Len(t, sentMessages(), tt.wantSentLen)
			snap := recSvc.latest()
			require.True(t, snap.viewPresent)
			require.Equal(t, tt.wantBound, snap.view.Bound)
			if !tt.wantBound {
				require.Equal(
					t,
					summaryview.BindingReasonInvalidated,
					snap.view.BindingReason,
				)
			}
			require.Equal(t, tt.wantFork, snap.forkPresent)
		})
	}
}
