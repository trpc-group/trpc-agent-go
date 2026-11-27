//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openaigo "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestInputOverflowHook_ProviderCallbackKeepsAuxiliaryTailoring(t *testing.T) {
	auxiliaryProbe := &overflowTailoringProbe{}
	auxiliary, auxiliaryRequests := newOverflowCallbackModel(t, auxiliaryProbe)
	approvedProbe := &overflowTailoringProbe{}
	approved, approvedRequests := newOverflowCallbackModel(t, approvedProbe,
		openai.WithChatRequestCallback(func(ctx context.Context, _ *openaigo.ChatCompletionNewParams) {
			responses, err := auxiliary.GenerateContent(ctx, &model.Request{
				Messages: []model.Message{model.NewUserMessage("auxiliary request")},
			})
			require.NoError(t, err)
			for range responses {
			}
		}),
	)
	summarizer := summary.NewSummarizer(approved, summary.WithInputOverflowHook(approveOverflowInput))
	sess := overflowCallbackSession()

	text, err := summarizer.Summarize(context.Background(), sess)

	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.Zero(t, approvedProbe.calls.Load())
	require.EqualValues(t, 1, auxiliaryProbe.calls.Load())
	require.Contains(t, (<-approvedRequests)[0].Content, "approved fact")
	require.Equal(t, "tailoring replaced the input", (<-auxiliaryRequests)[0].Content)
	require.Equal(t, "assistant-event", string(sess.State[session.SummaryLastIncludedEventIDStateKey]))
}

func TestInputOverflowHook_BeforeModelDelegationPreservesApprovedInput(t *testing.T) {
	for _, replaceContext := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace_context_%t", replaceContext), func(t *testing.T) {
			probe := &overflowTailoringProbe{}
			llm, requests := newOverflowCallbackModel(t, probe)
			callbacks := model.NewCallbacks()
			if replaceContext {
				callbacks.RegisterBeforeModel(func(context.Context, *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					return &model.BeforeModelResult{Context: context.Background()}, nil
				})
			}
			callbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
				responses, err := llm.GenerateContent(ctx, args.Request)
				if err != nil {
					return nil, err
				}
				var last *model.Response
				for response := range responses {
					last = response
				}
				return &model.BeforeModelResult{CustomResponse: last}, nil
			})
			summarizer := summary.NewSummarizer(llm,
				summary.WithInputOverflowHook(approveOverflowInput),
				summary.WithModelCallbacks(callbacks),
			)
			sess := overflowCallbackSession()

			text, err := summarizer.Summarize(context.Background(), sess)

			require.NoError(t, err)
			require.Equal(t, "summary", text)
			require.Zero(t, probe.calls.Load())
			require.Len(t, requests, 1)
			require.Contains(t, (<-requests)[0].Content, "approved fact")
			require.Equal(t, "assistant-event", string(sess.State[session.SummaryLastIncludedEventIDStateKey]))

			// Wrapping callbacks for one summary must not mutate the registered
			// callback chain or exempt a later ordinary request.
			_, err = callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{
				Request: &model.Request{Messages: []model.Message{model.NewUserMessage("next request")}},
			})
			require.NoError(t, err)
			require.EqualValues(t, 1, probe.calls.Load())
		})
	}
}

func TestInputOverflowHook_CallbackContextReplacementKeepsDirectApproval(t *testing.T) {
	probe := &overflowTailoringProbe{}
	llm, requests := newOverflowCallbackModel(t, probe)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(context.Context, *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{Context: context.Background()}, nil
		},
	)
	summarizer := summary.NewSummarizer(llm,
		summary.WithInputOverflowHook(approveOverflowInput),
		summary.WithModelCallbacks(callbacks),
	)
	sess := overflowCallbackSession()

	_, err := summarizer.Summarize(context.Background(), sess)

	require.NoError(t, err)
	require.Zero(t, probe.calls.Load())
	require.Len(t, requests, 1)
	require.Contains(t, (<-requests)[0].Content, "approved fact")
	require.Equal(t, "assistant-event", string(sess.State[session.SummaryLastIncludedEventIDStateKey]))
}

func approveOverflowInput(_ context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
	return &summary.OverflowDecision{Text: in.Text}, nil
}

func newOverflowCallbackModel(t *testing.T, probe *overflowTailoringProbe, opts ...openai.Option) (*openai.Model, <-chan []model.Message) {
	t.Helper()
	requests := make(chan []model.Message, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []model.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- input.Messages
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"summary-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	options := []openai.Option{
		openai.WithBaseURL(server.URL), openai.WithAPIKey("test-key"),
		openai.WithContextWindow(100_000), openai.WithMaxInputTokens(100),
		openai.WithEnableTokenTailoring(true), openai.WithTailoringStrategy(probe),
		openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
	}
	return openai.New("test-model", append(options, opts...)...), requests
}

func overflowCallbackSession() *session.Session {
	now := time.Now().UTC()
	return &session.Session{ID: "callback-overflow", Events: []event.Event{
		{ID: "user-event", Author: "user", Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("question")}}}},
		{ID: "assistant-event", Author: "agent", Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage(strings.Repeat("approved fact ", 200))}}}},
	}}
}
