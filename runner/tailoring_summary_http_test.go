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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	openaisdk "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
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

func newOpenAIHTTPRecordingServer(
	t *testing.T,
) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var (
		mu       sync.Mutex
		messages []map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		var body struct {
			Stream   bool             `json:"stream"`
			Messages []map[string]any `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		messages = body.Messages
		mu.Unlock()
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"resp-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	return server, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), messages...)
	}
}

func seedSessionMessages(
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

func runSingleTurn(
	t *testing.T,
	ctx context.Context,
	r runner.Runner,
	key session.Key,
	prompt string,
) {
	t.Helper()
	ch, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage(prompt))
	require.NoError(t, err)
	for evt := range ch {
		if evt != nil && evt.Error != nil {
			t.Fatalf("unexpected event error: %s: %s", evt.Error.Type, evt.Error.Message)
		}
	}
}

func TestOpenAIHTTP_PlaceholderThenChatRequestCallbackRewriteInvalidatesSummarySnapshots(
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

	t.Run("callback removing messages invalidates view and fork", func(t *testing.T) {
		ctx := context.Background()
		server, sentMessages := newOpenAIHTTPRecordingServer(t)
		recSvc := &recordingSummarySessionService{
			Service: inmemory.NewSessionService(),
		}
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
		seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

		m := openai.New(
			"gpt-4o",
			openai.WithBaseURL(server.URL),
			openai.WithAPIKey("test-key"),
			openai.WithEnableTokenTailoring(true),
			openai.WithMaxInputTokens(10_000),
			openai.WithChatRequestCallback(func(
				_ context.Context,
				req *openaisdk.ChatCompletionNewParams,
			) {
				req.Messages = req.Messages[len(req.Messages)-2:]
			}),
			openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
		)
		ag := llmagent.New(
			"qa",
			llmagent.WithModel(m),
			llmagent.WithInstruction("system prompt"),
			llmagent.WithAddSessionSummary(true),
		)
		r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
		t.Cleanup(func() { _ = r.Close() })

		runSingleTurn(t, ctx, r, key, "current question")

		require.Len(t, sentMessages(), 2)
		snap := recSvc.latest()
		require.True(t, snap.viewPresent)
		require.False(t, snap.view.Bound)
		require.Equal(t, summaryview.BindingReasonInvalidated, snap.view.BindingReason)
		require.False(t, snap.forkPresent)
	})

	t.Run("callback overriding messages via SetExtraFields invalidates view and fork", func(t *testing.T) {
		ctx := context.Background()
		server, sentMessages := newOpenAIHTTPRecordingServer(t)
		recSvc := &recordingSummarySessionService{
			Service: inmemory.NewSessionService(),
		}
		key := session.Key{AppName: "app", UserID: "u1b", SessionID: "s1b"}
		seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

		m := openai.New(
			"gpt-4o",
			openai.WithBaseURL(server.URL),
			openai.WithAPIKey("test-key"),
			openai.WithEnableTokenTailoring(true),
			openai.WithMaxInputTokens(10_000),
			openai.WithChatRequestCallback(func(
				_ context.Context,
				req *openaisdk.ChatCompletionNewParams,
			) {
				keep := []openaisdk.ChatCompletionMessageParamUnion{
					req.Messages[0],
					req.Messages[len(req.Messages)-1],
				}
				req.SetExtraFields(map[string]any{"messages": keep})
			}),
			openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
		)
		ag := llmagent.New(
			"qa",
			llmagent.WithModel(m),
			llmagent.WithInstruction("system prompt"),
			llmagent.WithAddSessionSummary(true),
		)
		r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
		t.Cleanup(func() { _ = r.Close() })

		runSingleTurn(t, ctx, r, key, "current question")

		require.Len(t, sentMessages(), 2)
		snap := recSvc.latest()
		require.True(t, snap.viewPresent)
		require.False(t, snap.view.Bound)
		require.Equal(t, summaryview.BindingReasonInvalidated, snap.view.BindingReason)
		require.False(t, snap.forkPresent)
	})

	t.Run("callback setting non-message SetExtraFields preserves rebased view and fork", func(t *testing.T) {
		ctx := context.Background()
		server, sentMessages := newOpenAIHTTPRecordingServer(t)
		recSvc := &recordingSummarySessionService{
			Service: inmemory.NewSessionService(),
		}
		key := session.Key{AppName: "app", UserID: "u1c", SessionID: "s1c"}
		seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

		m := openai.New(
			"gpt-4o",
			openai.WithBaseURL(server.URL),
			openai.WithAPIKey("test-key"),
			openai.WithEnableTokenTailoring(true),
			openai.WithMaxInputTokens(10_000),
			openai.WithChatRequestCallback(func(
				_ context.Context,
				req *openaisdk.ChatCompletionNewParams,
			) {
				req.SetExtraFields(map[string]any{
					"metadata": map[string]any{"trace_id": "t1"},
				})
			}),
			openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
		)
		ag := llmagent.New(
			"qa",
			llmagent.WithModel(m),
			llmagent.WithInstruction("system prompt"),
			llmagent.WithAddSessionSummary(true),
		)
		r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
		t.Cleanup(func() { _ = r.Close() })

		runSingleTurn(t, ctx, r, key, "current question")

		require.Len(t, sentMessages(), 5)
		snap := recSvc.latest()
		require.True(t, snap.viewPresent)
		require.True(t, snap.view.Bound)
		require.True(t, snap.forkPresent)
	})

	t.Run("read-only callback preserves rebased view and fork", func(t *testing.T) {
		ctx := context.Background()
		server, sentMessages := newOpenAIHTTPRecordingServer(t)
		recSvc := &recordingSummarySessionService{
			Service: inmemory.NewSessionService(),
		}
		key := session.Key{AppName: "app", UserID: "u2", SessionID: "s2"}
		seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

		m := openai.New(
			"gpt-4o",
			openai.WithBaseURL(server.URL),
			openai.WithAPIKey("test-key"),
			openai.WithEnableTokenTailoring(true),
			openai.WithMaxInputTokens(10_000),
			openai.WithChatRequestCallback(func(
				_ context.Context,
				req *openaisdk.ChatCompletionNewParams,
			) {
				_ = len(req.Messages)
			}),
			openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
		)
		ag := llmagent.New(
			"qa",
			llmagent.WithModel(m),
			llmagent.WithInstruction("system prompt"),
			llmagent.WithAddSessionSummary(true),
		)
		r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
		t.Cleanup(func() { _ = r.Close() })

		runSingleTurn(t, ctx, r, key, "current question")

		require.Len(t, sentMessages(), 5)
		snap := recSvc.latest()
		require.True(t, snap.viewPresent)
		require.True(t, snap.view.Bound)
		require.True(t, snap.forkPresent)
	})
}

func TestOpenAIHTTP_NumericToolCallExtraFieldsPreservesSummarySnapshots(t *testing.T) {
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
				ExtraFields: map[string]any{"score": float64(1)},
			}},
		},
		model.NewToolMessage("call_1", "search", "tool output"),
	}

	for _, tailoringEnabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("tailoring=%v", tailoringEnabled), func(t *testing.T) {
			ctx := context.Background()
			server, sentMessages := newOpenAIHTTPRecordingServer(t)
			recSvc := &recordingSummarySessionService{
				Service: inmemory.NewSessionService(),
			}
			key := session.Key{
				AppName:   "app",
				UserID:    "u",
				SessionID: fmt.Sprintf("s-%v", tailoringEnabled),
			}
			seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

			m := openai.New(
				"gpt-4o",
				openai.WithBaseURL(server.URL),
				openai.WithAPIKey("test-key"),
				openai.WithEnableTokenTailoring(tailoringEnabled),
				openai.WithMaxInputTokens(10_000),
				openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
			)
			ag := llmagent.New(
				"qa",
				llmagent.WithModel(m),
				llmagent.WithInstruction("system prompt"),
				llmagent.WithAddSessionSummary(true),
			)
			r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
			t.Cleanup(func() { _ = r.Close() })

			runSingleTurn(t, ctx, r, key, "current question")

			require.Len(t, sentMessages(), 5)
			snap := recSvc.latest()
			require.True(t, snap.viewPresent)
			require.True(t, snap.view.Bound)
			require.True(t, snap.forkPresent)
		})
	}
}

func TestOpenAIHTTP_DefaultOpenAICacheReorderWithoutTailoringPreservesSummarySnapshots(
	t *testing.T,
) {
	ctx := context.Background()
	server, sentMessages := newOpenAIHTTPRecordingServer(t)
	recSvc := &recordingSummarySessionService{
		Service: inmemory.NewSessionService(),
	}
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s-reorder"}
	seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", []model.Message{
		model.NewUserMessage("u1"),
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
		model.NewSystemMessage("mid-conversation system note"),
	})

	m := openai.New(
		"gpt-4o",
		openai.WithBaseURL(server.URL),
		openai.WithAPIKey("test-key"),
		openai.WithEnableTokenTailoring(false),
		openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
	)
	ag := llmagent.New(
		"qa",
		llmagent.WithModel(m),
		llmagent.WithInstruction("leading system prompt"),
		llmagent.WithAddSessionSummary(true),
	)
	r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
	t.Cleanup(func() { _ = r.Close() })

	runSingleTurn(t, ctx, r, key, "u2")

	sent := sentMessages()
	roles := make([]string, len(sent))
	for i, msg := range sent {
		roles[i], _ = msg["role"].(string)
	}
	require.Equal(t, []string{"system", "system", "user", "assistant", "tool", "user"}, roles)

	snap := recSvc.latest()
	require.True(t, snap.viewPresent)
	require.True(t, snap.view.Bound)
	require.True(t, snap.forkPresent)
}

func TestOpenAIHTTP_ExtraFieldsMessageOverrideInvalidatesSummarySnapshots(
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
	twoMessages := []map[string]any{
		{"role": "system", "content": "system prompt"},
		{"role": "user", "content": "current question"},
	}

	tests := []struct {
		name               string
		modelExtraFields   map[string]any
		requestExtraFields map[string]any
		wantSentLen        int
		wantFirstContent   string
		wantBound          bool
		wantFork           bool
	}{
		{
			name: "request ExtraFields replacing messages invalidates view and fork",
			requestExtraFields: map[string]any{
				"messages": twoMessages,
			},
			wantSentLen: 2,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "request ExtraFields subpath mutating messages.0.content invalidates view and fork",
			requestExtraFields: map[string]any{
				"messages.0.content": "rewritten system",
			},
			wantSentLen:      5,
			wantFirstContent: "rewritten system",
			wantBound:        false,
			wantFork:         false,
		},
		{
			name: "model WithExtraFields replacing messages invalidates view and fork",
			modelExtraFields: map[string]any{
				"messages": twoMessages,
			},
			wantSentLen: 2,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "model WithExtraFields subpath mutating messages.0.content invalidates view and fork",
			modelExtraFields: map[string]any{
				"messages.0.content": "rewritten system",
			},
			wantSentLen:      5,
			wantFirstContent: "rewritten system",
			wantBound:        false,
			wantFork:         false,
		},
		{
			name: "non-message model and request ExtraFields preserve rebased view and fork",
			modelExtraFields: map[string]any{
				"prompt_cache_key": "cache-k1",
			},
			requestExtraFields: map[string]any{
				"metadata": map[string]any{"trace_id": "t1"},
			},
			wantSentLen: 5,
			wantBound:   true,
			wantFork:    true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			server, sentMessages := newOpenAIHTTPRecordingServer(t)
			recSvc := &recordingSummarySessionService{
				Service: inmemory.NewSessionService(),
			}
			key := session.Key{
				AppName:   "app",
				UserID:    fmt.Sprintf("ef-u-%d", i),
				SessionID: fmt.Sprintf("ef-s-%d", i),
			}
			seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

			modelOpts := []openai.Option{
				openai.WithBaseURL(server.URL),
				openai.WithAPIKey("test-key"),
				openai.WithEnableTokenTailoring(true),
				openai.WithMaxInputTokens(10_000),
				openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
			}
			if tt.modelExtraFields != nil {
				modelOpts = append(
					modelOpts,
					openai.WithExtraFields(tt.modelExtraFields),
				)
			}
			m := openai.New("gpt-4o", modelOpts...)

			agentOpts := []llmagent.Option{
				llmagent.WithModel(m),
				llmagent.WithInstruction("system prompt"),
				llmagent.WithAddSessionSummary(true),
			}
			if tt.requestExtraFields != nil {
				extraCopy := tt.requestExtraFields
				callbacks := model.NewCallbacks().RegisterBeforeModel(
					func(
						_ context.Context,
						args *model.BeforeModelArgs,
					) (*model.BeforeModelResult, error) {
						args.Request.ExtraFields = extraCopy
						return nil, nil
					},
				)
				agentOpts = append(
					agentOpts,
					llmagent.WithModelCallbacks(callbacks),
				)
			}
			ag := llmagent.New("qa", agentOpts...)
			r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
			t.Cleanup(func() { _ = r.Close() })

			runSingleTurn(t, ctx, r, key, "current question")

			sent := sentMessages()
			require.Len(t, sent, tt.wantSentLen)
			if tt.wantFirstContent != "" {
				require.Equal(t, tt.wantFirstContent, sent[0]["content"])
			}
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

func TestOpenAIHTTP_WithOpenAIOptionsMessageOverrideInvalidatesSummarySnapshots(
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
	twoMessages := []map[string]any{
		{"role": "system", "content": "system prompt"},
		{"role": "user", "content": "current question"},
	}

	rewriteBodyMiddleware := func(
		req *http.Request,
		next openaiopt.MiddlewareNext,
	) (*http.Response, error) {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		payload["messages"] = twoMessages
		updated, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(updated))
		req.ContentLength = int64(len(updated))
		return next(req)
	}

	tests := []struct {
		name             string
		stream           bool
		openAIOpts       []openaiopt.RequestOption
		wantSentLen      int
		wantFirstContent string
		wantBound        bool
		wantFork         bool
	}{
		{
			name: "WithJSONSet replacing messages invalidates view and fork",
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithJSONSet("messages", twoMessages),
			},
			wantSentLen: 2,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "WithJSONSet subpath mutating messages.0.content invalidates view and fork",
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithJSONSet("messages.0.content", "rewritten system"),
			},
			wantSentLen:      5,
			wantFirstContent: "rewritten system",
			wantBound:        false,
			wantFork:         false,
		},
		{
			name: "WithJSONDel removing messages invalidates view and fork",
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithJSONDel("messages"),
			},
			wantSentLen: 0,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "WithMiddleware replacing request body messages invalidates view and fork",
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithMiddleware(rewriteBodyMiddleware),
			},
			wantSentLen: 2,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name:   "streaming WithJSONSet replacing messages invalidates view and fork",
			stream: true,
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithJSONSet("messages", twoMessages),
			},
			wantSentLen: 2,
			wantBound:   false,
			wantFork:    false,
		},
		{
			name: "non-message WithOpenAIOptions preserve rebased view and fork",
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithHeader("X-Trace-Id", "t1"),
				openaiopt.WithJSONSet("metadata", map[string]any{"k": "v"}),
			},
			wantSentLen: 5,
			wantBound:   true,
			wantFork:    true,
		},
		{
			name:   "streaming non-message WithOpenAIOptions preserve rebased view and fork",
			stream: true,
			openAIOpts: []openaiopt.RequestOption{
				openaiopt.WithMaxRetries(0),
				openaiopt.WithJSONSet("metadata", map[string]any{"k": "v"}),
			},
			wantSentLen: 5,
			wantBound:   true,
			wantFork:    true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			server, sentMessages := newOpenAIHTTPRecordingServer(t)
			recSvc := &recordingSummarySessionService{
				Service: inmemory.NewSessionService(),
			}
			key := session.Key{
				AppName:   "app",
				UserID:    fmt.Sprintf("oai-opt-u-%d", i),
				SessionID: fmt.Sprintf("oai-opt-s-%d", i),
			}
			seedSessionMessages(t, ctx, recSvc, key, key.AppName, "qa", history)

			m := openai.New(
				"gpt-4o",
				openai.WithBaseURL(server.URL),
				openai.WithAPIKey("test-key"),
				openai.WithEnableTokenTailoring(true),
				openai.WithMaxInputTokens(10_000),
				openai.WithOpenAIOptions(tt.openAIOpts...),
			)
			ag := llmagent.New(
				"qa",
				llmagent.WithModel(m),
				llmagent.WithInstruction("system prompt"),
				llmagent.WithAddSessionSummary(true),
				llmagent.WithGenerationConfig(model.GenerationConfig{
					Stream: tt.stream,
				}),
			)
			r := runner.NewRunner(key.AppName, ag, runner.WithSessionService(recSvc))
			t.Cleanup(func() { _ = r.Close() })

			runSingleTurn(t, ctx, r, key, "current question")

			sent := sentMessages()
			require.Len(t, sent, tt.wantSentLen)
			if tt.wantFirstContent != "" {
				require.Equal(t, tt.wantFirstContent, sent[0]["content"])
			}
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
