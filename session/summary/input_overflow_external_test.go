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
	"sync/atomic"
	"testing"
	"time"

	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type overflowTailoringProbe struct {
	calls atomic.Int32
}

func (p *overflowTailoringProbe) TailorMessages(
	_ context.Context, _ []model.Message, _ int,
) ([]model.Message, error) {
	p.calls.Add(1)
	return []model.Message{model.NewUserMessage("tailoring replaced the input")}, nil
}

func TestInputOverflowHook_SendsApprovedTextAndPersistsOnlyCoveredEvents(t *testing.T) {
	for _, test := range []struct {
		name        string
		replace     bool
		providerErr bool
		decline     bool
		truncated   bool
	}{
		{name: "original"},
		{name: "replacement still over estimated budget", replace: true},
		{name: "provider rejection", providerErr: true},
		{name: "truncated summary", truncated: true},
		{name: "default rejection", decline: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan []model.Message, 2)
			var httpCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpCalls.Add(1)
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
				if test.providerErr {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"message":"maximum context length exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`)
					return
				}
				if test.truncated {
					fmt.Fprint(w, `{"id":"summary-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"incomplete summary"},"finish_reason":"length"}]}`)
					return
				}
				fmt.Fprint(w, `{"id":"summary-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"first batch summary"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			tailoring := &overflowTailoringProbe{}
			llm := openai.New("test-model",
				openai.WithBaseURL(server.URL), openai.WithAPIKey("test-key"),
				openai.WithContextWindow(100_000), openai.WithMaxInputTokens(100),
				openai.WithEnableTokenTailoring(true), openai.WithTailoringStrategy(tailoring),
				openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
			)
			var approved string
			hookCalls := 0
			summarizer := summary.NewSummarizer(llm,
				summary.WithInputOverflowHook(func(_ context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
					hookCalls++
					if test.decline {
						return nil, nil
					}
					approved = in.Text
					if test.replace {
						approved = strings.Repeat("business-authorized complete representation ", 100)
					}
					return &summary.OverflowDecision{Text: approved}, nil
				}),
			)
			service := inmemory.NewSessionService(inmemory.WithSummarizer(summarizer))
			defer service.Close()
			ctx := context.Background()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "overflow"}
			sess, err := service.CreateSession(ctx, key, session.StateMap{})
			require.NoError(t, err)
			now := time.Now().UTC()
			for i, role := range []model.Role{model.RoleUser, model.RoleAssistant, model.RoleUser, model.RoleAssistant} {
				author := "agent"
				if role == model.RoleUser {
					author = "user"
				}
				evt := &event.Event{ID: fmt.Sprintf("event-%d", i), Author: author, Timestamp: now.Add(time.Duration(i) * time.Millisecond),
					Response: &model.Response{ID: fmt.Sprintf("response-%d", i),
						Choices: []model.Choice{{Message: model.Message{
							Role: role, Content: fmt.Sprintf("marker-%d ", i) + strings.Repeat("source fact ", 100),
						}}},
					},
				}
				require.NoError(t, service.AppendEvent(ctx, sess, evt))
			}
			sess, err = service.GetSession(ctx, key)
			require.NoError(t, err)
			err = service.CreateSessionSummary(ctx, sess, "", true)
			require.Equal(t, 1, hookCalls)
			stored, getErr := service.GetSession(ctx, key)
			require.NoError(t, getErr)
			require.Len(t, stored.Events, 4)
			if test.providerErr || test.decline || test.truncated {
				require.Error(t, err)
				require.Empty(t, stored.Summaries)
			} else {
				require.NoError(t, err)
				require.Equal(t, "event-1", stored.Summaries[""].Boundary.LastEventID)
				require.Equal(t, "first batch summary", stored.Summaries[""].Summary)
			}
			if test.decline {
				require.EqualValues(t, 0, httpCalls.Load())
				return
			}
			require.EqualValues(t, 1, httpCalls.Load())
			require.EqualValues(t, 0, tailoring.calls.Load())
			wire := <-requests
			require.Len(t, wire, 1)
			require.Contains(t, wire[0].Content, approved)
			require.NotContains(t, wire[0].Content, "marker-2")
			// The approval applies only to the summary attempt, not this model's
			// subsequent requests or the caller's context.
			responses, err := llm.GenerateContent(ctx, &model.Request{Messages: []model.Message{model.NewUserMessage("next call")}})
			require.NoError(t, err)
			for range responses {
			}
			require.EqualValues(t, 1, tailoring.calls.Load())
		})
	}
}
