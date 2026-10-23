//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunnerResumeValidatesNativeRecordBeforeExecutingTools(t *testing.T) {
	for _, scenario := range []string{"valid", "edited_arguments", "other_model", "chat_protocol", "selected_other_model", "selected_native_model", "cancel_after_tool_result"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var httpCalls, toolCalls, selectorCalls, beforeModelCalls atomic.Int32
			callbackModels := make(chan model.Info, 1)
			inputs := make(chan json.RawMessage, 2)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := mustReadJSONBody(t, r)
				w.Header().Set("Content-Type", "application/json")
				call := httpCalls.Add(1)
				if call > 2 {
					http.Error(w, "unexpected extra model request", http.StatusBadRequest)
					return
				}
				if call == 1 {
					_, _ = io.WriteString(w, `{"id":"pending","status":"completed","output":`+nativeToolOutput+`}`)
					return
				}
				inputs <- body["input"]
				_, _ = io.WriteString(w, `{"id":"answer","status":"completed","output":`+messageOutput("answer", "2")+`}`)
			}))
			defer srv.Close()
			original := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
			ch, err := original.GenerateContent(ctx, &model.Request{Messages: []model.Message{model.NewUserMessage("calculate")}})
			require.NoError(t, err)
			responses := collect(t, ch)
			require.Len(t, responses, 1)
			pending := responses[0]
			if scenario == "edited_arguments" {
				pending.Choices[0].Message.ToolCalls[0].Function.Arguments = json.RawMessage(`{"a":999}`)
			}
			path := filepath.Join(t.TempDir(), "resume.db")
			open := func() *sqlite.Service {
				db, err := sql.Open("sqlite3", path)
				require.NoError(t, err)
				svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false), sqlite.WithAppendEventHook(func(c *session.AppendEventContext, next func() error) error {
					err := next()
					if scenario == "cancel_after_tool_result" && err == nil && c.Event.IsToolResultResponse() {
						cancel()
					}
					return err
				}))
				require.NoError(t, err)
				return svc
			}
			svc := open()
			key := session.Key{AppName: "native", UserID: "user", SessionID: "session"}
			sess, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			user := event.NewResponseEvent("old-run", "user", &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewUserMessage("calculate")}}})
			user.FilterKey = "native"
			require.NoError(t, svc.AppendEvent(ctx, sess, user))
			evt := event.NewResponseEvent("old-run", "native", pending)
			evt.FilterKey = "native"
			require.NoError(t, svc.AppendEvent(ctx, sess, evt))
			require.NoError(t, svc.Close())
			svc = open()
			defer svc.Close()
			loaded, err := svc.GetSession(ctx, key)
			require.NoError(t, err)
			require.Len(t, loaded.Events, 2)
			require.NotNil(t, loaded.Events[1].Choices[0].Message.Replay)

			var selected model.Model = original
			legacy := &legacyIntegrationModel{}
			other := New("other-model", WithAPIKey("test"), WithBaseURL(srv.URL))
			switch scenario {
			case "other_model":
				selected = other
			case "chat_protocol", "selected_native_model":
				selected = legacy
			}
			toolCallbacks := tool.NewCallbacks().RegisterBeforeTool(func(ctx context.Context, _ *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
				inv, ok := agent.InvocationFromContext(ctx)
				if ok && inv.Model != nil {
					callbackModels <- inv.Model.Info()
				}
				return nil, nil
			})
			modelCallbacks := model.NewCallbacks().RegisterBeforeModel(func(context.Context, *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
				beforeModelCalls.Add(1)
				return nil, nil
			})
			opts := []llmagent.Option{
				llmagent.WithModel(selected),
				llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}),
				llmagent.WithToolCallbacks(toolCallbacks),
				llmagent.WithModelCallbacks(modelCallbacks),
			}
			if scenario == "selected_other_model" || scenario == "selected_native_model" {
				opts = append(opts, llmagent.WithModelSelector(func(context.Context, *agent.Invocation) (model.Model, error) {
					selectorCalls.Add(1)
					if scenario == "selected_native_model" {
						return original, nil
					}
					return other, nil
				}))
			}
			r := runner.NewRunner("native", llmagent.New("native", opts...), runner.WithSessionService(svc))
			defer r.Close()
			events, err := r.Run(ctx, "user", "session", model.Message{}, agent.WithResume(true))
			require.NoError(t, err)
			var errors []string
			for evt := range events {
				if evt.Error != nil {
					errors = append(errors, evt.Error.Message)
				}
			}
			if scenario == "cancel_after_tool_result" {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				require.EqualValues(t, 1, toolCalls.Load())
				require.EqualValues(t, 1, httpCalls.Load())
				require.Zero(t, beforeModelCalls.Load(), "canceled resume must not start another model call or callback")
				return
			}
			require.NoError(t, ctx.Err())
			if scenario == "valid" || scenario == "selected_native_model" {
				require.Empty(t, errors)
				require.EqualValues(t, 1, toolCalls.Load())
				require.EqualValues(t, 2, httpCalls.Load())
				require.EqualValues(t, 1, beforeModelCalls.Load())
				select {
				case info := <-callbackModels:
					require.Equal(t, original.Info(), info, "resumed tool callbacks must observe the selected model")
				default:
					t.Fatal("resumed tool callback did not receive its model")
				}
				assertToolReplayInput(t, <-inputs)
			} else {
				require.NotEmpty(t, errors)
				if scenario == "edited_arguments" {
					require.Contains(t, strings.Join(errors, "\n"), "message projection changed")
				} else {
					require.Contains(t, strings.Join(errors, "\n"), "belongs to protocol")
				}
				require.Zero(t, toolCalls.Load(), "an invalid persisted native record must fail before any tool side effect")
				require.Zero(t, beforeModelCalls.Load())
				require.EqualValues(t, 1, httpCalls.Load(), "a rejected resume must not call another model")
			}
			if scenario == "selected_other_model" || scenario == "selected_native_model" {
				require.EqualValues(t, 1, selectorCalls.Load(), "resume and the first model request must share one model selection")
			}
			require.Zero(t, legacy.calls.Load())
		})
	}
}
