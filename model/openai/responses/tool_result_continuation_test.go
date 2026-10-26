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
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const toolResultNativeMultiOutput = `[
	{"type":"reasoning","id":"rs_result_multi","summary":[],"encrypted_content":"opaque-result-state"},
	{"type":"function_call","id":"fc_a","status":"completed","call_id":"call_a","name":"calculator","arguments":"{\"a\":1}"},
	{"type":"function_call","id":"fc_b","status":"completed","call_id":"call_b","name":"calculator","arguments":"{\"a\":2}"}
]`

type toolResultNativeTransport struct {
	calls       atomic.Int32
	requests    chan json.RawMessage
	firstOutput string
}

func newToolResultNativeTransport(firstOutput string) *toolResultNativeTransport {
	return &toolResultNativeTransport{
		requests:    make(chan json.RawMessage, 4),
		firstOutput: firstOutput,
	}
}

func (rt *toolResultNativeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	var body struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	call := rt.calls.Add(1)
	if call > 3 {
		return nil, fmt.Errorf("unexpected model request %d", call)
	}
	select {
	case rt.requests <- append(json.RawMessage(nil), body.Input...):
	default:
		return nil, fmt.Errorf("model request capture buffer is full")
	}
	responseBody := `{"id":"result_answer","status":"completed","output":[{"type":"message","id":"msg_result_answer","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}]}`
	if call == 1 {
		responseBody = `{"id":"result_tool","status":"completed","output":` + rt.firstOutput + `}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(responseBody))),
		Request:    req,
	}, nil
}

func toolResultNativeModel(rt *toolResultNativeTransport) *Model {
	return New("gpt-5", WithAPIKey("test"), WithBaseURL("http://fixture.invalid/v1"),
		WithOpenAIOptions(option.WithHTTPClient(&http.Client{Transport: rt}), option.WithMaxRetries(0)))
}

func openToolResultNativeSQLite(t *testing.T, path string) *sqlite.Service {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func runToolResultNative(t *testing.T, r runner.Runner, text string, opts ...agent.RunOption) []*event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage(text), opts...)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	require.NoError(t, ctx.Err(), "runner must finish within the fixture deadline")
	return events
}

func toolResultNativeErrors(events []*event.Event) []string {
	var messages []string
	for _, evt := range events {
		if evt != nil && evt.Error != nil {
			messages = append(messages, evt.Error.Message)
		}
	}
	return messages
}

func assertToolResultNativeReplay(t *testing.T, sess *session.Session, expected string) {
	t.Helper()
	found := false
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
			require.Equal(t, expected, string(msg.ToolCalls[0].Function.Arguments))
			raw, err := msg.ReplayDataFor(responseProvider, "gpt-5")
			require.NoError(t, err)
			require.JSONEq(t, nativeToolOutput, string(raw), "opaque native response items must survive persistence")
		}
	}
	require.True(t, found, "session must retain the native tool call")
}

func assertToolResultNativeMultiReplay(t *testing.T, sess *session.Session) {
	t.Helper()
	found := false
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
			require.Equal(t, `{"a":1}`, string(msg.ToolCalls[0].Function.Arguments))
			require.Equal(t, "call_b", msg.ToolCalls[1].ID)
			require.Equal(t, `{"a":2}`, string(msg.ToolCalls[1].Function.Arguments))
			raw, err := msg.ReplayDataFor(responseProvider, "gpt-5")
			require.NoError(t, err)
			require.JSONEq(t, toolResultNativeMultiOutput, string(raw), "opaque native response items must survive persistence")
		}
	}
	require.True(t, found, "session must retain both native tool calls")
}

func assertToolResultNativeOutputs(t *testing.T, input json.RawMessage, expectedCalls map[string]string, expectedOutputs map[string]string) {
	t.Helper()
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(input, &items))
	calls := make(map[string]string)
	outputs := make(map[string]string)
	var native []json.RawMessage
	for _, raw := range items {
		var item struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments string          `json:"arguments"`
			Output    json.RawMessage `json:"output"`
		}
		require.NoError(t, json.Unmarshal(raw, &item))
		switch item.Type {
		case "reasoning", "function_call":
			native = append(native, raw)
			if item.Type == "function_call" {
				require.Equal(t, "calculator", item.Name)
				calls[item.CallID] = item.Arguments
			}
		case "message":
			if item.ID == "msg_progress" {
				native = append(native, raw)
			}
		case "function_call_output":
			var output string
			require.NoError(t, json.Unmarshal(item.Output, &output))
			outputs[item.CallID] = output
		}
	}
	require.JSONEq(t, toolResultNativeNativeJSON(expectedCalls), stringMustJSON(t, native), "all opaque items and original function_call arguments must remain unchanged")
	require.Equal(t, expectedCalls, calls)
	require.Equal(t, expectedOutputs, outputs)
}

func toolResultNativeNativeJSON(expectedCalls map[string]string) string {
	if len(expectedCalls) == 2 {
		return toolResultNativeMultiOutput
	}
	return nativeToolOutput
}

func stringMustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

type toolResultNativePlugin struct {
	name              string
	afterTool         tool.AfterToolCallbackStructured
	afterToolMessages plugin.AfterToolMessagesCallback
}

func (p *toolResultNativePlugin) Name() string { return p.name }

func (p *toolResultNativePlugin) Register(r *plugin.Registry) {
	r.AfterTool(p.afterTool)
	r.AfterToolMessages(p.afterToolMessages)
}

func TestToolResultNativeAfterToolReplacementContinuesAfterSQLiteReopen(t *testing.T) {
	rt := newToolResultNativeTransport(nativeToolOutput)
	m := toolResultNativeModel(rt)
	var toolCalls atomic.Int32
	calculator := function.NewFunctionTool(func(_ context.Context, input struct {
		A int `json:"a"`
	}) (int, error) {
		toolCalls.Add(1)
		return input.A + 1, nil
	}, function.WithName("calculator"))
	callbacks := tool.NewCallbacks().RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		if args == nil || args.ToolCallID != "call_tool" || args.Error != nil {
			return nil, fmt.Errorf("unexpected after-tool input: %#v", args)
		}
		return &tool.AfterToolResult{CustomResult: 9}, nil
	})
	a := llmagent.New("native", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{calculator}), llmagent.WithToolCallbacks(callbacks))
	path := filepath.Join(t.TempDir(), "tool-result-after-tool.db")
	svc := openToolResultNativeSQLite(t, path)
	r := runner.NewRunner("native", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = r.Close() })

	first := runToolResultNative(t, r, "calculate")
	require.Empty(t, toolResultNativeErrors(first))
	require.EqualValues(t, 1, toolCalls.Load())
	require.EqualValues(t, 2, rt.calls.Load())
	<-rt.requests
	assertToolResultNativeOutputs(t, <-rt.requests, map[string]string{"call_tool": `{"a":1}`}, map[string]string{"call_tool": "9"})

	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())
	svc = openToolResultNativeSQLite(t, path)
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	assertToolResultNativeReplay(t, sess, `{"a":1}`)

	r = runner.NewRunner("native", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = r.Close() })
	continued := runToolResultNative(t, r, "continue")
	require.Empty(t, toolResultNativeErrors(continued))
	require.EqualValues(t, 1, toolCalls.Load(), "a completed tool must not run again after loading SQLite history")
	require.EqualValues(t, 3, rt.calls.Load())
	assertToolResultNativeOutputs(t, <-rt.requests, map[string]string{"call_tool": `{"a":1}`}, map[string]string{"call_tool": "9"})
}

func TestToolResultNativeAfterToolMessagesRewriteAndParallelPanicContinueAfterSQLiteReopen(t *testing.T) {
	rt := newToolResultNativeTransport(toolResultNativeMultiOutput)
	m := toolResultNativeModel(rt)
	var toolCalls atomic.Int32
	calculator := function.NewFunctionTool(func(_ context.Context, input struct {
		A int `json:"a"`
	}) (int, error) {
		toolCalls.Add(1)
		if input.A == 1 {
			panic("fixture panic")
		}
		return 7, nil
	}, function.WithName("calculator"))
	var afterMessages atomic.Int32
	p := &toolResultNativePlugin{
		name: "tool-result-messages-fixture",
		afterToolMessages: func(_ context.Context, args *plugin.AfterToolMessagesArgs) (*plugin.AfterToolMessagesResult, error) {
			afterMessages.Add(1)
			if args == nil || len(args.ToolResultMessages) == 0 {
				return nil, fmt.Errorf("expected at least one tool result")
			}
			replacements := make([]model.Message, len(args.ToolResultMessages))
			for i, msg := range args.ToolResultMessages {
				switch msg.ToolID {
				case "call_a":
					if !strings.Contains(msg.Content, "fixture panic") {
						return nil, fmt.Errorf("panic result was not preserved: %q", msg.Content)
					}
					msg.Content = "normalized panic: fixture panic"
				case "call_b":
					if msg.Content != "7" {
						return nil, fmt.Errorf("unexpected successful result %q", msg.Content)
					}
					msg.Content = "normalized result: 7"
				default:
					return nil, fmt.Errorf("unexpected tool result id %q", msg.ToolID)
				}
				replacements[i] = msg
			}
			return &plugin.AfterToolMessagesResult{ToolResultMessages: replacements}, nil
		},
	}
	a := llmagent.New("native",
		llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{calculator}),
		llmagent.WithEnableParallelTools(true),
	)
	path := filepath.Join(t.TempDir(), "tool-result-messages.db")
	svc := openToolResultNativeSQLite(t, path)
	r := runner.NewRunner("native", a, runner.WithSessionService(svc), runner.WithPlugins(p))
	t.Cleanup(func() { _ = r.Close() })
	perCall := agent.WithToolResultEventPerCallEnabled(true)

	first := runToolResultNative(t, r, "calculate in parallel", perCall)
	require.Empty(t, toolResultNativeErrors(first))
	require.EqualValues(t, 2, toolCalls.Load(), "parallel sibling must run even when one tool panics")
	require.EqualValues(t, 2, afterMessages.Load(), "per-call events should rewrite each tool result")
	require.EqualValues(t, 2, rt.calls.Load())
	<-rt.requests
	assertToolResultNativeOutputs(t, <-rt.requests,
		map[string]string{"call_a": `{"a":1}`, "call_b": `{"a":2}`},
		map[string]string{"call_a": "normalized panic: fixture panic", "call_b": "normalized result: 7"})

	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())
	svc = openToolResultNativeSQLite(t, path)
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	assertToolResultNativeMultiReplay(t, sess)

	r = runner.NewRunner("native", a, runner.WithSessionService(svc), runner.WithPlugins(p))
	t.Cleanup(func() { _ = r.Close() })
	continued := runToolResultNative(t, r, "continue", perCall)
	require.Empty(t, toolResultNativeErrors(continued))
	require.EqualValues(t, 2, toolCalls.Load(), "reloaded completed tool calls must not execute again")
	require.EqualValues(t, 2, afterMessages.Load(), "the callback should not run again when history is reloaded")
	require.EqualValues(t, 3, rt.calls.Load())
	assertToolResultNativeOutputs(t, <-rt.requests,
		map[string]string{"call_a": `{"a":1}`, "call_b": `{"a":2}`},
		map[string]string{"call_a": "normalized panic: fixture panic", "call_b": "normalized result: 7"})
}
