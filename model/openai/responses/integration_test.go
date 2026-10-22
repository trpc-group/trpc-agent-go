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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/failover"
	"trpc.group/trpc-go/trpc-agent-go/model/hedge"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const nativeToolOutput = `[
	{"type":"reasoning","id":"rs_tool","summary":[],"encrypted_content":"opaque-tool-state"},
	{"type":"message","id":"msg_progress","status":"completed","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"calculating"}]},
	{"type":"function_call","id":"fc_tool","call_id":"call_tool","name":"calculator","arguments":"{\"a\":1}"}
]`

func integrationCalculator(calls *atomic.Int32) tool.Tool {
	return function.NewFunctionTool(func(_ context.Context, input struct {
		A int `json:"a"`
	}) (int, error) {
		calls.Add(1)
		return input.A + 1, nil
	}, function.WithName("calculator"))
}

func runIntegration(t *testing.T, r runner.Runner, ctx context.Context, text string) []*event.Event {
	t.Helper()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage(text))
	require.NoError(t, err)
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	require.NoError(t, ctx.Err(), "the runner must stop without exhausting the test deadline")
	return events
}

func TestRunnerNativeToolLoopSurvivesSQLiteReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requests := make(chan map[string]json.RawMessage, 4)
	var httpCalls, toolCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		call := httpCalls.Add(1)
		if call == 1 {
			_, _ = io.WriteString(w, `{"id":"resp_tool","status":"completed","output":`+nativeToolOutput+`}`)
			return
		}
		if call > 3 {
			http.Error(w, "unexpected extra model call", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"resp_%d","status":"completed","output":[{"type":"message","id":"msg_answer_%d","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"2"}]}]}`, call, call)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	a := llmagent.New("native", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}))
	path := filepath.Join(t.TempDir(), "runner.db")
	openService := func() *sqlite.Service {
		db, err := sql.Open("sqlite3", path)
		require.NoError(t, err)
		svc, err := sqlite.NewService(db)
		require.NoError(t, err)
		return svc
	}
	svc := openService()
	t.Cleanup(func() { _ = svc.Close() })
	r := runner.NewRunner("native", a, runner.WithSessionService(svc))
	for _, evt := range runIntegration(t, r, ctx, "add one") {
		require.Nil(t, evt.Error)
	}
	require.EqualValues(t, 1, toolCalls.Load())
	require.EqualValues(t, 2, httpCalls.Load(), "a completed model tool response must continue the agent loop")
	<-requests
	second := <-requests
	assertToolReplayInput(t, second["input"])
	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())

	// The next run must compile history loaded from a new SQLite connection.
	svc = openService()
	r = runner.NewRunner("native", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = r.Close() })
	for _, evt := range runIntegration(t, r, ctx, "explain the previous answer") {
		require.Nil(t, evt.Error)
	}
	assertToolReplayInput(t, (<-requests)["input"])
	require.EqualValues(t, 1, toolCalls.Load(), "reloading a completed history must not execute its tools again")

	// A legacy adapter with the same configured name still cannot consume the
	// native protocol. The flow must reject before calling that adapter.
	legacy := &legacyIntegrationModel{}
	legacyAgent := llmagent.New("native", llmagent.WithModel(legacy))
	legacyRunner := runner.NewRunner("native", legacyAgent, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = legacyRunner.Close() })
	var replayError bool
	for _, evt := range runIntegration(t, legacyRunner, ctx, "switch model") {
		if evt.Error != nil {
			require.Contains(t, evt.Error.Message, "belongs to protocol")
			replayError = true
		}
	}
	require.True(t, replayError)
	require.Zero(t, legacy.calls.Load())
}

func assertToolReplayInput(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var input []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &input))
	var native []json.RawMessage
	var outputIndex int
	for i, item := range input {
		if string(item["id"]) == `"rs_tool"` || string(item["id"]) == `"msg_progress"` || string(item["id"]) == `"fc_tool"` {
			encoded, err := json.Marshal(item)
			require.NoError(t, err)
			native = append(native, encoded)
		}
		if string(item["type"]) == `"function_call_output"` {
			require.Equal(t, `"call_tool"`, string(item["call_id"]))
			require.Equal(t, `"2"`, string(item["output"]))
			outputIndex = i
		}
	}
	encoded, err := json.Marshal(native)
	require.NoError(t, err)
	require.JSONEq(t, nativeToolOutput, string(encoded))
	require.Greater(t, outputIndex, 0)
	require.Equal(t, `"fc_tool"`, string(input[outputIndex-1]["id"]), "native items must remain adjacent and ordered before the result")
}

type legacyIntegrationModel struct{ calls atomic.Int32 }

func (m *legacyIntegrationModel) Info() model.Info { return model.Info{Name: "gpt-5"} }
func (m *legacyIntegrationModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	m.calls.Add(1)
	return nil, fmt.Errorf("legacy model must not receive native history")
}

func TestRunnerDoesNotExecuteOrPersistUncommittedStreamCalls(t *testing.T) {
	for _, terminal := range []string{"eof", "failed", "incomplete", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var toolCalls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSSE(w,
					`{"type":"response.created","response":{"id":"uncommitted","status":"in_progress"}}`,
					`{"type":"response.output_text.delta","delta":"working"}`,
					`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_uncommitted","call_id":"uncommitted","status":"completed","name":"calculator","arguments":"{\"a\":1}"}}`,
				)
				if terminal != "eof" {
					writeSSE(w, fmt.Sprintf(`{"type":"response.%s","response":{"id":"uncommitted","status":"%s"}}`, terminal, terminal))
				}
			}))
			defer srv.Close()
			svc := inmemory.NewSessionService()
			defer svc.Close()
			a := llmagent.New("native",
				llmagent.WithModel(New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))),
				llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
				llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}),
			)
			r := runner.NewRunner("native", a, runner.WithSessionService(svc))
			defer r.Close()
			var sawError bool
			for _, evt := range runIntegration(t, r, ctx, "calculate") {
				sawError = sawError || evt.Error != nil
			}
			require.True(t, sawError)
			require.Zero(t, toolCalls.Load())
			sess, err := svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			for _, evt := range sess.Events {
				for _, choice := range evt.Choices {
					require.Empty(t, choice.Message.ToolCalls)
					require.Empty(t, choice.Delta.ToolCalls)
					require.Nil(t, choice.Message.Replay)
				}
			}
		})
	}
}

func TestRunnerRejectsCallbackEditsBeforeToolExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var toolCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_tool","status":"completed","output":`+nativeToolOutput+`}`)
	}))
	defer srv.Close()
	callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		if args.Response != nil && len(args.Response.Choices) > 0 {
			msg := &args.Response.Choices[0].Message
			if len(msg.ToolCalls) > 0 {
				msg.ToolCalls[0].Function.Arguments = json.RawMessage(`{"a":999}`)
			}
		}
		return nil, nil
	})
	a := llmagent.New("native", llmagent.WithModel(New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))),
		llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}), llmagent.WithModelCallbacks(callbacks))
	r := runner.NewRunner("native", a)
	defer r.Close()
	var sawError bool
	for _, evt := range runIntegration(t, r, ctx, "calculate") {
		if evt.Error != nil {
			require.Contains(t, evt.Error.Message, "projection changed")
			sawError = true
		}
	}
	require.True(t, sawError)
	require.Zero(t, toolCalls.Load())
}

func TestRunnerNativeHistoryThroughRoutingWrappers(t *testing.T) {
	for _, kind := range []string{"failover", "hedge"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := mustReadJSONBody(t, r)
				call := calls.Add(1)
				if call == 2 {
					require.Contains(t, string(body["input"]), `"phase":"final_answer"`)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"resp_%d","status":"completed","output":[{"type":"message","id":"msg_%d","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"ok"}]}]}`, call, call)
			}))
			defer srv.Close()
			primary := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
			backup := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
			var routed model.Model
			var err error
			if kind == "failover" {
				routed, err = failover.New(failover.WithCandidates(primary, backup))
			} else {
				routed, err = hedge.New(hedge.WithCandidates(primary, backup), hedge.WithDelay(time.Hour))
			}
			require.NoError(t, err)
			require.Equal(t, responseProvider, routed.Info().ReplayProtocol)
			r := runner.NewRunner("native", llmagent.New("native", llmagent.WithModel(routed)))
			defer r.Close()
			for _, text := range []string{"hello", "continue"} {
				for _, evt := range runIntegration(t, r, ctx, text) {
					require.Nil(t, evt.Error)
				}
			}
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestNativeRoutingRejectsMixedProducerIdentities(t *testing.T) {
	primary := New("gpt-5")
	for _, candidates := range [][]model.Model{
		{primary, New("another-model")},
		{primary, &legacyIntegrationModel{}},
		{&legacyIntegrationModel{}, primary},
	} {
		_, err := failover.New(failover.WithCandidates(candidates...))
		require.ErrorContains(t, err, "same protocol and configured model name")
		_, err = hedge.New(hedge.WithCandidates(candidates...))
		require.ErrorContains(t, err, "same protocol and configured model name")
	}
	_, err := hedge.New(hedge.WithCandidates(primary), hedge.WithName("logical-alias"))
	require.ErrorContains(t, err, "logical name")
}

func TestRunnerResponseProducerIsCapturedBeforeCallbacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_native","status":"completed","output":[{"type":"message","id":"msg_native","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer srv.Close()
	callbacks := model.NewCallbacks().RegisterAfterModel(func(ctx context.Context, _ *model.AfterModelArgs) (*model.AfterModelResult, error) {
		invocation, ok := agent.InvocationFromContext(ctx)
		require.True(t, ok)
		invocation.Model = &legacyIntegrationModel{}
		return nil, nil
	})
	a := llmagent.New("native", llmagent.WithModel(New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))),
		llmagent.WithModelCallbacks(callbacks))
	r := runner.NewRunner("native", a)
	defer r.Close()
	var sawNative bool
	for _, evt := range runIntegration(t, r, ctx, "hello") {
		require.Nil(t, evt.Error)
		for _, choice := range evt.Choices {
			sawNative = sawNative || choice.Message.Replay != nil
		}
	}
	require.True(t, sawNative)
}
