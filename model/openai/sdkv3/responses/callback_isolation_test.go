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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type callbackIsolationPlugin struct {
	name        string
	beforeTool  tool.BeforeToolCallbackStructured
	beforeModel model.BeforeModelCallbackStructured
}

const callbackIsolationMultiToolOutput = `[
	{"type":"reasoning","id":"rs_callback_multi","summary":[],"encrypted_content":"opaque-multi-state"},
	{"type":"function_call","id":"fc_a","status":"completed","call_id":"call_a","name":"calculator","arguments":"{\"a\":1}"},
	{"type":"function_call","id":"fc_b","status":"completed","call_id":"call_b","name":"calculator","arguments":"{\"a\":1}"}
]`

func (p *callbackIsolationPlugin) Name() string { return p.name }

func (p *callbackIsolationPlugin) Register(r *plugin.Registry) {
	r.BeforeTool(p.beforeTool)
	r.BeforeModel(p.beforeModel)
}

type callbackIsolationTransport struct {
	calls       atomic.Int32
	requests    chan json.RawMessage
	firstOutput string
}

func newCallbackIsolationTransport() *callbackIsolationTransport {
	return &callbackIsolationTransport{requests: make(chan json.RawMessage, 4), firstOutput: nativeToolOutput}
}

func (rt *callbackIsolationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	input := append(json.RawMessage(nil), body["input"]...)
	call := rt.calls.Add(1)
	if call > 3 {
		return nil, fmt.Errorf("unexpected model request %d", call)
	}
	select {
	case rt.requests <- input:
	default:
		return nil, errors.New("model request capture buffer is full")
	}

	responseBody := `{"id":"callback_tool","status":"completed","output":` + rt.firstOutput + `}`
	if call > 1 {
		responseBody = `{"id":"callback_answer","status":"completed","output":[{"type":"message","id":"msg_callback_answer","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}]}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(responseBody))),
		Request:    req,
	}, nil
}

func callbackIsolationModel(rt *callbackIsolationTransport) *Model {
	return New("gpt-5", WithAPIKey("test"), WithBaseURL("http://fixture.invalid/v1"),
		WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: rt}), option.WithMaxRetries(0)))
}

func openCallbackIsolationSQLite(t *testing.T, path string) *sqlite.Service {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func runCallbackIsolation(t *testing.T, r runner.Runner, text string, opts ...agent.RunOption) []*event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage(text), opts...)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	require.NoError(t, ctx.Err(), "the runner must finish before the test deadline")
	return events
}

func callbackIsolationErrors(events []*event.Event) []string {
	var out []string
	for _, evt := range events {
		if evt != nil && evt.Error != nil {
			out = append(out, evt.Error.Message)
		}
	}
	return out
}

func callbackIsolationCalculator(calls *atomic.Int32, seen chan<- int, retry bool) tool.Tool {
	var attempts atomic.Int32
	return function.NewFunctionTool(func(_ context.Context, input struct {
		A int `json:"a"`
	}) (int, error) {
		calls.Add(1)
		seen <- input.A
		if retry && attempts.Add(1) == 1 {
			return 0, io.EOF
		}
		return input.A, nil
	}, function.WithName("calculator"))
}

func mutateCallbackIsolationArguments(args *tool.BeforeToolArgs) error {
	if args == nil {
		return errors.New("before-tool arguments are nil")
	}
	if string(args.Arguments) != `{"a":1}` {
		return fmt.Errorf("unexpected tool arguments %q", args.Arguments)
	}
	args.Arguments[5] = '9'
	return nil
}

func callbackIsolationToolCallback() tool.BeforeToolCallbackStructured {
	return func(_ context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		return nil, mutateCallbackIsolationArguments(args)
	}
}

func callbackIsolationPluginOption(name string, beforeTool tool.BeforeToolCallbackStructured, beforeModel model.BeforeModelCallbackStructured) runner.Option {
	return runner.WithPlugins(&callbackIsolationPlugin{
		name:        name,
		beforeTool:  beforeTool,
		beforeModel: beforeModel,
	})
}

func assertCallbackIsolationContinuation(t *testing.T, raw json.RawMessage, expectedOutput string) {
	t.Helper()
	var items []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	require.NoError(t, json.Unmarshal(raw, &items))
	var calls, results int
	for _, item := range items {
		switch item.Type {
		case "function_call":
			calls++
			require.Equal(t, "fc_tool", item.ID)
			require.Equal(t, "call_tool", item.CallID)
			require.Equal(t, "calculator", item.Name)
			require.JSONEq(t, `{"a":1}`, item.Arguments)
		case "function_call_output":
			results++
			require.Equal(t, "call_tool", item.CallID)
			var output string
			require.NoError(t, json.Unmarshal(item.Output, &output))
			require.Equal(t, expectedOutput, output)
		}
	}
	require.Equal(t, 1, calls)
	require.Equal(t, 1, results)
}

func assertCallbackIsolationSession(t *testing.T, sess *session.Session) {
	t.Helper()
	var found bool
	for _, evt := range sess.Events {
		choices := evt.Choices
		if evt.Response != nil {
			choices = evt.Response.Choices
		}
		for _, choice := range choices {
			msg := choice.Message
			if msg.Replay == nil || len(msg.ToolCalls) == 0 {
				continue
			}
			found = true
			require.Equal(t, "call_tool", msg.ToolCalls[0].ID)
			require.Equal(t, `{"a":1}`, string(msg.ToolCalls[0].Function.Arguments))
			replay, err := msg.ReplayDataFor(responseProvider, "gpt-5")
			require.NoError(t, err)
			require.JSONEq(t, nativeToolOutput, string(replay))
		}
	}
	require.True(t, found, "the session must retain the original native tool call")
}

func assertCallbackIsolationMultiContinuation(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var items []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	require.NoError(t, json.Unmarshal(raw, &items))
	calls := make(map[string]string)
	results := make(map[string]string)
	callCount, resultCount := 0, 0
	for _, item := range items {
		switch item.Type {
		case "function_call":
			callCount++
			require.JSONEq(t, `{"a":1}`, item.Arguments)
			calls[item.ID] = item.CallID
		case "function_call_output":
			resultCount++
			var output string
			require.NoError(t, json.Unmarshal(item.Output, &output))
			results[item.CallID] = output
		}
	}
	require.Equal(t, 2, callCount)
	require.Equal(t, map[string]string{"fc_a": "call_a", "fc_b": "call_b"}, calls)
	require.Equal(t, 2, resultCount)
	require.Equal(t, map[string]string{"call_a": "9", "call_b": "9"}, results)
}

func assertCallbackIsolationMultiSession(t *testing.T, sess *session.Session) {
	t.Helper()
	var found bool
	for _, evt := range sess.Events {
		choices := evt.Choices
		if evt.Response != nil {
			choices = evt.Response.Choices
		}
		for _, choice := range choices {
			msg := choice.Message
			if msg.Replay == nil || len(msg.ToolCalls) == 0 {
				continue
			}
			found = true
			require.Len(t, msg.ToolCalls, 2)
			require.Equal(t, "call_a", msg.ToolCalls[0].ID)
			require.Equal(t, "call_b", msg.ToolCalls[1].ID)
			for _, call := range msg.ToolCalls {
				require.Equal(t, `{"a":1}`, string(call.Function.Arguments))
			}
			replay, err := msg.ReplayDataFor(responseProvider, "gpt-5")
			require.NoError(t, err)
			require.JSONEq(t, callbackIsolationMultiToolOutput, string(replay))
		}
	}
	require.True(t, found, "the session must retain both original native tool calls")
}

func callbackIsolationBeforeModelMutation(mode string) model.BeforeModelCallbackStructured {
	var mutated atomic.Bool
	return func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		if args == nil || args.Request == nil || mutated.Load() {
			return nil, nil
		}
		for i := range args.Request.Messages {
			msg := &args.Request.Messages[i]
			if msg.Replay == nil || len(msg.ToolCalls) == 0 {
				continue
			}
			switch mode {
			case "mutate_arguments":
				if string(msg.ToolCalls[0].Function.Arguments) != `{"a":1}` {
					return nil, fmt.Errorf("unexpected replay arguments %q", msg.ToolCalls[0].Function.Arguments)
				}
				msg.ToolCalls[0].Function.Arguments[5] = '9'
			case "replace_tool_call":
				call := msg.ToolCalls[0]
				call.Function.Arguments = []byte(`{"a":9}`)
				msg.ToolCalls[0] = call
			default:
				return nil, fmt.Errorf("unknown mutation mode %q", mode)
			}
			mutated.Store(true)
			return nil, nil
		}
		return nil, nil
	}
}

func TestRunnerBeforeToolInPlaceMutationPreservesNativeReplayAcrossSQLiteReopen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		plugin bool
	}{
		{name: "local_callback_with_retry"},
		{name: "runner_plugin_callback", plugin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newCallbackIsolationTransport()
			m := callbackIsolationModel(rt)
			var toolCalls atomic.Int32
			seen := make(chan int, 4)
			calculator := callbackIsolationCalculator(&toolCalls, seen, true)
			callback := callbackIsolationToolCallback()
			opts := []llmagent.Option{
				llmagent.WithModel(m),
				llmagent.WithTools([]tool.Tool{calculator}),
				llmagent.WithToolCallRetryPolicy(&tool.RetryPolicy{
					MaxAttempts: 2,
					RetryOn: func(_ context.Context, info *tool.RetryInfo) (bool, error) {
						return info != nil && errors.Is(info.Error, io.EOF), nil
					},
				}),
			}
			runnerOpts := []runner.Option{}
			if tc.plugin {
				runnerOpts = append(runnerOpts, callbackIsolationPluginOption("before-tool-isolation", callback, nil))
			} else {
				opts = append(opts, llmagent.WithToolCallbacks(tool.NewCallbacks().RegisterBeforeTool(callback)))
			}
			a := llmagent.New("native", opts...)
			path := filepath.Join(t.TempDir(), "callback-isolation.db")
			svc := openCallbackIsolationSQLite(t, path)
			runnerOpts = append(runnerOpts, runner.WithSessionService(svc))
			r := runner.NewRunner("native", a, runnerOpts...)
			t.Cleanup(func() { _ = r.Close() })

			events := runCallbackIsolation(t, r, "calculate")
			require.Empty(t, callbackIsolationErrors(events))
			require.EqualValues(t, 2, toolCalls.Load(), "the retry policy should run two tool attempts")
			require.EqualValues(t, 2, rt.calls.Load())
			require.Equal(t, 9, <-seen)
			require.Equal(t, 9, <-seen)
			<-rt.requests
			assertCallbackIsolationContinuation(t, <-rt.requests, "9")

			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())
			svc = openCallbackIsolationSQLite(t, path)
			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			assertCallbackIsolationSession(t, sess)

			r = runner.NewRunner("native", a, runner.WithSessionService(svc))
			t.Cleanup(func() { _ = r.Close() })
			continued := runCallbackIsolation(t, r, "continue")
			require.Empty(t, callbackIsolationErrors(continued))
			require.EqualValues(t, 2, toolCalls.Load(), "reloaded history must not execute the completed tool again")
			require.EqualValues(t, 3, rt.calls.Load())
			assertCallbackIsolationContinuation(t, <-rt.requests, "9")
		})
	}
}

func TestRunnerBeforeToolInPlaceMutationPerCallEventsPreservesMultiCallReplay(t *testing.T) {
	rt := newCallbackIsolationTransport()
	rt.firstOutput = callbackIsolationMultiToolOutput
	m := callbackIsolationModel(rt)
	var toolCalls atomic.Int32
	seen := make(chan int, 4)
	calculator := callbackIsolationCalculator(&toolCalls, seen, false)
	callback := callbackIsolationToolCallback()
	a := llmagent.New("native",
		llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{calculator}),
		llmagent.WithEnableParallelTools(true),
		llmagent.WithToolCallbacks(tool.NewCallbacks().RegisterBeforeTool(callback)),
	)
	path := filepath.Join(t.TempDir(), "callback-isolation-per-call.db")
	svc := openCallbackIsolationSQLite(t, path)
	r := runner.NewRunner("native", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = r.Close() })
	perCall := agent.WithToolResultEventPerCallEnabled(true)

	events := runCallbackIsolation(t, r, "calculate", perCall)
	require.Empty(t, callbackIsolationErrors(events))
	require.EqualValues(t, 2, toolCalls.Load())
	require.EqualValues(t, 2, rt.calls.Load())
	require.ElementsMatch(t, []int{9, 9}, []int{<-seen, <-seen})
	<-rt.requests
	assertCallbackIsolationMultiContinuation(t, <-rt.requests)

	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())
	svc = openCallbackIsolationSQLite(t, path)
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	assertCallbackIsolationMultiSession(t, sess)

	continuedRunner := runner.NewRunner("native", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = continuedRunner.Close() })
	continued := runCallbackIsolation(t, continuedRunner, "continue", perCall)
	require.Empty(t, callbackIsolationErrors(continued))
	require.EqualValues(t, 2, toolCalls.Load(), "reloaded history must not execute either completed tool again")
	require.EqualValues(t, 3, rt.calls.Load())
	assertCallbackIsolationMultiContinuation(t, <-rt.requests)
}

func TestRunnerBeforeModelEditsDoNotPolluteNativeSession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   string
		plugin bool
	}{
		{name: "local_callback_mutates_arguments", mode: "mutate_arguments"},
		{name: "runner_plugin_replaces_tool_call", mode: "replace_tool_call", plugin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newCallbackIsolationTransport()
			m := callbackIsolationModel(rt)
			var toolCalls atomic.Int32
			seen := make(chan int, 2)
			calculator := callbackIsolationCalculator(&toolCalls, seen, false)
			beforeModel := callbackIsolationBeforeModelMutation(tc.mode)
			opts := []llmagent.Option{
				llmagent.WithModel(m),
				llmagent.WithTools([]tool.Tool{calculator}),
			}
			svc := inmemory.NewSessionService()
			t.Cleanup(func() { _ = svc.Close() })
			runnerOpts := []runner.Option{runner.WithSessionService(svc)}
			if tc.plugin {
				runnerOpts = append(runnerOpts, callbackIsolationPluginOption("before-model-isolation", nil, beforeModel))
			} else {
				opts = append(opts, llmagent.WithModelCallbacks(model.NewCallbacks().RegisterBeforeModel(beforeModel)))
			}
			a := llmagent.New("native", opts...)
			r := runner.NewRunner("native", a, runnerOpts...)
			defer r.Close()

			first := runCallbackIsolation(t, r, "calculate")
			require.Contains(t, strings.Join(callbackIsolationErrors(first), "\n"), "projection changed")
			require.EqualValues(t, 1, toolCalls.Load())
			require.Equal(t, 1, <-seen)
			require.EqualValues(t, 1, rt.calls.Load(), "the edited native history must be rejected before HTTP")
			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			assertCallbackIsolationSession(t, sess)
			<-rt.requests

			continued := runCallbackIsolation(t, r, "continue")
			require.Empty(t, callbackIsolationErrors(continued))
			require.EqualValues(t, 1, toolCalls.Load(), "a rejected callback edit must not change the stored completed tool call")
			require.EqualValues(t, 2, rt.calls.Load())
			assertCallbackIsolationContinuation(t, <-rt.requests, "1")
			sess, err = svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			assertCallbackIsolationSession(t, sess)
		})
	}
}
