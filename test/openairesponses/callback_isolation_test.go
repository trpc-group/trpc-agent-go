//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

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
	sdkresponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/failover"
	"trpc.group/trpc-go/trpc-agent-go/model/hedge"
	legacyopenai "trpc.group/trpc-go/trpc-agent-go/model/openai"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
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
	afterModel  model.AfterModelCallbackStructured
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
	r.AfterModel(p.afterModel)
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
	if string(body["stream"]) == "true" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(responseBody)); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + compact.String() + "}\n\n")),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(responseBody))),
		Request:    req,
	}, nil
}

func callbackIsolationModel(rt *callbackIsolationTransport) *responses.Model {
	return responses.New("gpt-5", responses.WithAPIKey("test"), responses.WithBaseURL("http://fixture.invalid/v1"),
		responses.WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: rt}), option.WithMaxRetries(0)))
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
			if msg.ProviderData == nil || len(msg.ToolCalls) == 0 {
				continue
			}
			found = true
			require.Equal(t, "call_tool", msg.ToolCalls[0].ID)
			require.Equal(t, `{"a":1}`, string(msg.ToolCalls[0].Function.Arguments))
			require.NotNil(t, msg.ProviderData)
			replay := msg.ProviderData.Payload
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
			if msg.ProviderData == nil || len(msg.ToolCalls) == 0 {
				continue
			}
			found = true
			require.Len(t, msg.ToolCalls, 2)
			require.Equal(t, "call_a", msg.ToolCalls[0].ID)
			require.Equal(t, "call_b", msg.ToolCalls[1].ID)
			for _, call := range msg.ToolCalls {
				require.Equal(t, `{"a":1}`, string(call.Function.Arguments))
			}
			require.NotNil(t, msg.ProviderData)
			replay := msg.ProviderData.Payload
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
			if msg.ProviderData == nil || len(msg.ToolCalls) == 0 {
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
			case "mutate_native_arguments":
				const original = `\"a\":1`
				index := bytes.Index(msg.ProviderData.Payload, []byte(original))
				if index < 0 {
					return nil, errors.New("missing native tool arguments")
				}
				msg.ProviderData.Payload[index+len(original)-1] = '9'
			default:
				return nil, fmt.Errorf("unknown mutation mode %q", mode)
			}
			mutated.Store(true)
			return nil, nil
		}
		return nil, nil
	}
}

func editNativeFunctionArguments(message *model.Message, callID, arguments string) error {
	var items []sdkresponses.ResponseOutputItemUnion
	if err := json.Unmarshal(message.ProviderData.Payload, &items); err != nil {
		return err
	}
	output := make([]json.RawMessage, len(items))
	for i, item := range items {
		output[i] = json.RawMessage(item.RawJSON())
		if item.Type != "function_call" || item.CallID != callID {
			continue
		}
		var call sdkresponses.ResponseFunctionToolCallParam
		if err := json.Unmarshal(output[i], &call); err != nil {
			return err
		}
		call.Arguments = arguments
		encoded, err := json.Marshal(call)
		if err != nil {
			return err
		}
		output[i] = encoded
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	message.ProviderData.Payload = encoded
	return nil
}

func editableProviderDataAfterModel(mode string) model.AfterModelCallbackStructured {
	return func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		if args == nil || args.Response == nil || len(args.Response.Choices) == 0 ||
			len(args.Response.Choices[0].Message.ToolCalls) == 0 {
			return nil, nil
		}
		response := args.Response
		if mode == "replace_response_and_rebind" {
			response = args.Response.Clone()
			message := response.Choices[0].Message
			message.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
			message.ToolCalls[0].Function.Arguments = json.RawMessage(`{"a":9}`)
			if err := editNativeFunctionArguments(&message, message.ToolCalls[0].ID, `{"a":9}`); err != nil {
				return nil, err
			}
			response.Choices[0].Message = message
			return &model.AfterModelResult{CustomResponse: response}, nil
		}

		message := &response.Choices[0].Message
		switch mode {
		case "display_only":
			message.Content = "edited display text"
			message.ReasoningContent = "edited display reasoning"
		case "tool_calls_only":
			message.ToolCalls[0].Function.Arguments = json.RawMessage(`{"a":9}`)
		case "replay_only":
			return nil, editNativeFunctionArguments(message, message.ToolCalls[0].ID, `{"a":9}`)
		case "in_place_native_edit":
			const original = `\"a\":1`
			index := bytes.Index(message.ProviderData.Payload, []byte(original))
			if index < 0 {
				return nil, errors.New("native arguments not found")
			}
			message.ProviderData.Payload[index+len(original)-1] = '9'
		default:
			return nil, fmt.Errorf("unknown after-model edit mode %q", mode)
		}
		return nil, nil
	}
}

func assertEditableProviderDataContinuation(t *testing.T, raw json.RawMessage, replayArguments, toolOutput string) {
	t.Helper()
	var input []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	require.NoError(t, json.Unmarshal(raw, &input))
	var callCount, resultCount int
	for _, item := range input {
		switch item.Type {
		case "function_call":
			callCount++
			require.Equal(t, "fc_tool", item.ID)
			require.Equal(t, "call_tool", item.CallID)
			require.Equal(t, "calculator", item.Name)
			require.JSONEq(t, replayArguments, item.Arguments)
		case "function_call_output":
			resultCount++
			require.Equal(t, "call_tool", item.CallID)
			var output string
			require.NoError(t, json.Unmarshal(item.Output, &output))
			require.Equal(t, toolOutput, output)
		}
	}
	require.Equal(t, 1, callCount)
	require.Equal(t, 1, resultCount)
}

func findEditableProviderDataMessage(t *testing.T, sess *session.Session) model.Message {
	t.Helper()
	for _, evt := range sess.Events {
		choices := evt.Choices
		if evt.Response != nil {
			choices = evt.Response.Choices
		}
		for _, choice := range choices {
			if choice.Message.ProviderData != nil && len(choice.Message.ToolCalls) > 0 {
				return choice.Message
			}
		}
	}
	t.Fatal("session has no replay-bearing tool call")
	return model.Message{}
}

func TestAfterModelNativeEditsUpdateExecutionAndSurviveSQLiteReopen(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		callbackMode         string
		wantToolArguments    string
		wantProviderDataArgs string
		wantToolOutput       string
		wantContent          string
		wantReasoning        string
		stream               bool
		wrapper              string
	}{
		{name: "display fields only", callbackMode: "display_only", wantToolArguments: `{"a":1}`, wantProviderDataArgs: `{"a":1}`, wantToolOutput: "1", wantContent: "edited display text", wantReasoning: "edited display reasoning"},
		{name: "tool calls only", callbackMode: "tool_calls_only", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":1}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "native edit updates execution", callbackMode: "replay_only", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "in-place payload edit", callbackMode: "in_place_native_edit", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "streamed native edit", callbackMode: "replay_only", stream: true, wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "failover forwards projection", callbackMode: "replay_only", wrapper: "failover", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "hedge forwards projection", callbackMode: "replay_only", wrapper: "hedge", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "selector uses selected model projector", callbackMode: "replay_only", wrapper: "selector", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "native edit wins after callback chain", callbackMode: "replay_only", wrapper: "callbacks", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
		{name: "replacement response rebinds both", callbackMode: "replace_response_and_rebind", wantToolArguments: `{"a":9}`, wantProviderDataArgs: `{"a":9}`, wantToolOutput: "9", wantContent: "calculating"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newCallbackIsolationTransport()
			var m model.Model = callbackIsolationModel(rt)
			if tc.wrapper == "failover" {
				wrapped, err := failover.New(failover.WithCandidates(m))
				require.NoError(t, err)
				m = wrapped
			}
			if tc.wrapper == "hedge" {
				wrapped, err := hedge.New(hedge.WithCandidates(m))
				require.NoError(t, err)
				m = wrapped
			}
			var toolCalls atomic.Int32
			seen := make(chan int, 2)
			calculator := callbackIsolationCalculator(&toolCalls, seen, false)
			callbacks := model.NewCallbacks().RegisterAfterModel(editableProviderDataAfterModel(tc.callbackMode))
			if tc.wrapper == "callbacks" {
				callbacks.RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					if args.Response != nil && len(args.Response.Choices) > 0 && len(args.Response.Choices[0].Message.ToolCalls) > 0 {
						args.Response.Choices[0].Message.ToolCalls[0].Function.Arguments = json.RawMessage(`{"a":5}`)
					}
					return nil, nil
				})
			}
			opts := []llmagent.Option{llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{calculator}),
				llmagent.WithModelCallbacks(callbacks), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: tc.stream})}
			if tc.wrapper == "selector" {
				opts = append(opts, llmagent.WithModel(legacyopenai.New("unused-chat")),
					llmagent.WithModelSelector(func(context.Context, *agent.Invocation) (model.Model, error) { return m, nil }))
			}
			a := llmagent.New("native", opts...)
			path := filepath.Join(t.TempDir(), "editable-replay.db")
			svc := openCallbackIsolationSQLite(t, path)
			r := runner.NewRunner("native", a, runner.WithSessionService(svc))
			first := runCallbackIsolation(t, r, "calculate")
			require.Empty(t, callbackIsolationErrors(first))
			var sawEditedDisplay bool
			for _, evt := range first {
				if evt.Response == nil {
					continue
				}
				for _, choice := range evt.Response.Choices {
					if choice.Message.ProviderData == nil || len(choice.Message.ToolCalls) == 0 {
						continue
					}
					sawEditedDisplay = true
					require.Equal(t, tc.wantContent, choice.Message.Content)
					require.Equal(t, tc.wantReasoning, choice.Message.ReasoningContent)
				}
			}
			require.True(t, sawEditedDisplay, "callback message should be emitted to consumers")
			require.EqualValues(t, 1, toolCalls.Load())
			wantExecuted := 1
			if tc.wantToolArguments == `{"a":9}` {
				wantExecuted = 9
			}
			require.Equal(t, wantExecuted, <-seen)
			require.EqualValues(t, 2, rt.calls.Load())
			<-rt.requests // initial request
			assertEditableProviderDataContinuation(t, <-rt.requests, tc.wantProviderDataArgs, tc.wantToolOutput)

			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			stored := findEditableProviderDataMessage(t, sess)
			require.Equal(t, tc.wantContent, stored.Content)
			require.Equal(t, tc.wantReasoning, stored.ReasoningContent)
			require.Equal(t, tc.wantToolArguments, string(stored.ToolCalls[0].Function.Arguments))
			require.NotNil(t, stored.ProviderData)
			storedProviderData := stored.ProviderData.Payload
			var items []struct {
				Type      string `json:"type"`
				Arguments string `json:"arguments"`
			}
			require.NoError(t, json.Unmarshal(storedProviderData, &items))
			var replayArgs string
			for _, item := range items {
				if item.Type == "function_call" {
					replayArgs = item.Arguments
				}
			}
			require.JSONEq(t, tc.wantProviderDataArgs, replayArgs)
			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())
			svc = openCallbackIsolationSQLite(t, path)
			loaded, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			stored = findEditableProviderDataMessage(t, loaded)
			require.NotNil(t, stored.ProviderData)
			storedProviderData = stored.ProviderData.Payload
			var loadedItems []struct {
				Type      string `json:"type"`
				Arguments string `json:"arguments"`
			}
			require.NoError(t, json.Unmarshal(storedProviderData, &loadedItems))
			replayArgs = ""
			for _, item := range loadedItems {
				if item.Type == "function_call" {
					replayArgs = item.Arguments
				}
			}
			require.JSONEq(t, tc.wantProviderDataArgs, replayArgs)

			r = runner.NewRunner("native", a, runner.WithSessionService(svc))
			continued := runCallbackIsolation(t, r, "continue")
			require.Empty(t, callbackIsolationErrors(continued))
			require.EqualValues(t, 1, toolCalls.Load(), "reopening the session must not rerun completed tools")
			require.EqualValues(t, 3, rt.calls.Load())
			assertEditableProviderDataContinuation(t, <-rt.requests, tc.wantProviderDataArgs, tc.wantToolOutput)
			require.NoError(t, r.Close())
		})
	}
}

func TestAfterModelCanReplaceResponseChoiceSetForNativeOutput(t *testing.T) {
	rt := newCallbackIsolationTransport()
	rt.firstOutput = `[{"type":"message","id":"msg_final","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"original"}]}]`
	m := callbackIsolationModel(rt)
	callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		copy := *args.Response
		copy.Choices = append([]model.Choice(nil), args.Response.Choices...)
		copy.Choices = append(copy.Choices, model.Choice{Index: 1, Message: model.NewAssistantMessage("callback choice")})
		return &model.AfterModelResult{CustomResponse: &copy}, nil
	})
	a := llmagent.New("native", llmagent.WithModel(m), llmagent.WithModelCallbacks(callbacks))
	r := runner.NewRunner("native", a)
	defer r.Close()

	events := runCallbackIsolation(t, r, "hello")
	require.Empty(t, callbackIsolationErrors(events))
	require.EqualValues(t, 1, rt.calls.Load())
	var sawReplacement bool
	for _, evt := range events {
		if evt.Response == nil || len(evt.Response.Choices) != 2 || evt.Response.Choices[0].Message.ProviderData == nil {
			continue
		}
		sawReplacement = true
		require.Equal(t, "callback choice", evt.Response.Choices[1].Message.Content)
	}
	require.True(t, sawReplacement, "Runner must emit the complete callback replacement response")
}

func TestRunnerBeforeToolInPlaceMutationPreservesNativeProviderDataAcrossSQLiteReopen(t *testing.T) {
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

func TestRunnerBeforeToolInPlaceMutationPerCallEventsPreservesMultiCallProviderData(t *testing.T) {
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

func TestRunnerBeforeModelEditsAreScopedToTheRequest(t *testing.T) {
	for _, tc := range []struct {
		name              string
		mode              string
		plugin            bool
		wantWireArguments string
	}{
		{name: "local_callback_mutates_arguments", mode: "mutate_arguments", wantWireArguments: `{"a":1}`},
		{name: "runner_plugin_replaces_tool_call", mode: "replace_tool_call", plugin: true, wantWireArguments: `{"a":1}`},
		{name: "local_callback_edits_payload_bytes", mode: "mutate_native_arguments", wantWireArguments: `{"a":9}`},
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
			require.Empty(t, callbackIsolationErrors(first))
			require.EqualValues(t, 1, toolCalls.Load())
			require.Equal(t, 1, <-seen)
			require.EqualValues(t, 2, rt.calls.Load())
			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			assertCallbackIsolationSession(t, sess)
			<-rt.requests // initial request
			assertEditableProviderDataContinuation(t, <-rt.requests, tc.wantWireArguments, "1")

			continued := runCallbackIsolation(t, r, "continue")
			require.Empty(t, callbackIsolationErrors(continued))
			require.EqualValues(t, 1, toolCalls.Load(), "a detached callback edit must not change the stored completed tool call")
			require.EqualValues(t, 3, rt.calls.Load())
			assertCallbackIsolationContinuation(t, <-rt.requests, "1")
			sess, err = svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			assertCallbackIsolationSession(t, sess)
		})
	}
}
