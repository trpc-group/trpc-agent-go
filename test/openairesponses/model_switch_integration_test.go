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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	chat "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	switchSource = "gpt-6-astra"
	switchTarget = "gpt-6.2-astra"
	switchChat   = "chat-model"

	switchReasoningItem = `{"type":"reasoning","id":"rs_switch","status":"completed","summary":[{"type":"summary_text","text":"private Responses summary"}],"encrypted_content":"opaque-switch-reasoning"}`
	switchCallItem      = `{"type":"function_call","id":"fc_switch","call_id":"call_responses","status":"completed","name":"lookup","arguments":"{}"}`
	switchFirstOutput   = switchReasoningItem + "," + switchCallItem
)

type switchRequest struct {
	Path     string
	Model    string          `json:"model"`
	Input    json.RawMessage `json:"input"`
	Messages json.RawMessage `json:"messages"`
}

type switchReply struct {
	status int
	body   string
}

type switchFixture struct {
	mu       sync.Mutex
	requests []switchRequest
	replies  []switchReply
	models   map[string]model.Model
	sessions *inmemory.SessionService
	runner   runner.Runner
	toolRuns atomic.Int32
}

func newSwitchFixture(t *testing.T, replies ...switchReply) *switchFixture {
	t.Helper()
	return newSwitchFixtureWithCallbacks(t, nil, replies...)
}

func newSwitchFixtureWithCallbacks(t *testing.T, callbacks *model.Callbacks, replies ...switchReply) *switchFixture {
	return newSwitchFixtureWithTools(t, callbacks, nil, replies...)
}

func newSwitchFixtureWithTools(t *testing.T, callbacks *model.Callbacks, makeTools func(*switchFixture) []tool.Tool, replies ...switchReply) *switchFixture {
	t.Helper()
	f := &switchFixture{replies: replies, sessions: inmemory.NewSessionService()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request switchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		request.Path = r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, request)
		index := len(f.requests) - 1
		var reply switchReply
		if index < len(f.replies) {
			reply = f.replies[index]
		}
		f.mu.Unlock()
		if reply.status == 0 {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = fmt.Fprint(w, reply.body)
	}))
	t.Cleanup(server.Close)

	f.models = map[string]model.Model{
		switchSource: responses.New(switchSource, responses.WithBaseURL(server.URL), responses.WithAPIKey("test")),
		switchTarget: responses.New(switchTarget, responses.WithBaseURL(server.URL), responses.WithAPIKey("test")),
		switchChat:   chat.New(switchChat, chat.WithBaseURL(server.URL), chat.WithAPIKey("test")),
	}
	lookup := function.NewFunctionTool(func(_ context.Context, _ struct{}) (string, error) {
		f.toolRuns.Add(1)
		return "case-42", nil
	}, function.WithName("lookup"), function.WithDescription("Look up the case."))
	tools := []tool.Tool{lookup}
	if makeTools != nil {
		tools = makeTools(f)
	}
	a := llmagent.New("switch",
		llmagent.WithModel(f.models[switchSource]),
		llmagent.WithModels(f.models),
		llmagent.WithInstruction("Use the conversation."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
		llmagent.WithTools(tools),
		llmagent.WithModelCallbacks(callbacks),
	)
	f.runner = runner.NewRunner("switch", a, runner.WithSessionService(f.sessions))
	t.Cleanup(func() {
		require.NoError(t, f.runner.Close())
		require.NoError(t, f.sessions.Close())
	})
	return f
}

func (f *switchFixture) snapshot() []switchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]switchRequest(nil), f.requests...)
}

func (f *switchFixture) run(t *testing.T, sessionID, name string, message model.Message, opts ...agent.RunOption) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts = append(opts, agent.WithModelName(name))
	events, err := f.runner.Run(ctx, "user", sessionID, message, opts...)
	require.NoError(t, err)
	var failure string
	for evt := range events {
		if evt.Error != nil && failure == "" {
			failure = evt.Error.Message
		}
	}
	require.NoError(t, ctx.Err())
	return failure
}

func (f *switchFixture) savedNative(t *testing.T, sessionID string) model.Message {
	t.Helper()
	s, err := f.sessions.GetSession(context.Background(), session.Key{
		AppName: "switch", UserID: "user", SessionID: sessionID,
	})
	require.NoError(t, err)
	require.NotNil(t, s)
	for _, evt := range s.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Choices {
			if choice.Message.ProviderData != nil {
				return choice.Message
			}
		}
	}
	t.Fatal("no native message in session")
	return model.Message{}
}

func switchResponsesReply(output string) switchReply {
	return switchReply{status: http.StatusOK, body: `{"id":"response","object":"response","status":"completed","output":[` + output + `]}`}
}

func switchTextReply(id, text string) switchReply {
	return switchResponsesReply(fmt.Sprintf(`{"type":"message","id":%q,"status":"completed","role":"assistant","content":[{"type":"output_text","text":%q,"annotations":[]}]}`, id, text))
}

func switchChatReply(message, finish string) switchReply {
	return switchReply{status: http.StatusOK, body: `{"id":"chat","object":"chat.completion","choices":[{"index":0,"message":` + message + `,"finish_reason":"` + finish + `"}]}`}
}

func TestModelSwitchResponsesReplaysCompletedNativeHistory(t *testing.T) {
	f := newSwitchFixture(t,
		switchResponsesReply(switchFirstOutput),
		switchTextReply("msg_source", "source answer"),
		switchTextReply("msg_target", "target answer"),
	)
	const sid = "responses-switch"
	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("look it up")))
	saved := f.savedNative(t, sid)
	require.Equal(t, switchSource, saved.ProviderData.Model)
	require.JSONEq(t, "["+switchFirstOutput+"]", string(saved.ProviderData.Payload))
	require.EqualValues(t, 1, f.toolRuns.Load())

	require.Empty(t, f.run(t, sid, switchTarget, model.NewUserMessage("continue")))
	requests := f.snapshot()
	require.Len(t, requests, 3)
	require.Equal(t, "/responses", requests[2].Path)
	require.Equal(t, switchTarget, requests[2].Model)
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(requests[2].Input, &items))
	var native []json.RawMessage
	var resultIndex int
	for i, raw := range items {
		var item struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			CallID string `json:"call_id"`
		}
		require.NoError(t, json.Unmarshal(raw, &item))
		if item.ID == "rs_switch" || item.ID == "fc_switch" {
			native = append(native, raw)
		}
		if item.Type == "function_call_output" && item.CallID == "call_responses" {
			resultIndex = i
			require.Contains(t, string(raw), "case-42")
		}
	}
	encoded, err := json.Marshal(native)
	require.NoError(t, err)
	require.JSONEq(t, "["+switchFirstOutput+"]", string(encoded))
	require.Greater(t, resultIndex, 0)
	require.Contains(t, string(items[resultIndex-1]), `"fc_switch"`)
	require.Contains(t, string(requests[2].Input), "source answer")
	require.Contains(t, string(requests[2].Input), "continue")
	require.EqualValues(t, 1, f.toolRuns.Load(), "completed calls must not execute again")
	require.Equal(t, switchSource, f.savedNative(t, sid).ProviderData.Model)
	require.JSONEq(t, string(saved.ProviderData.Payload), string(f.savedNative(t, sid).ProviderData.Payload))
}

func TestModelSwitchResponsesChatAndBack(t *testing.T) {
	f := newSwitchFixture(t,
		switchResponsesReply(switchFirstOutput),
		switchTextReply("msg_source", "source answer"),
		switchChatReply(`{"role":"assistant","reasoning_content":"private Chat reasoning","tool_calls":[{"id":"call_chat","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`, "tool_calls"),
		switchChatReply(`{"role":"assistant","content":"chat answer"}`, "stop"),
		switchTextReply("msg_back", "back answer"),
	)
	const sid = "round-trip"
	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("look it up")))
	saved := f.savedNative(t, sid)
	require.Empty(t, f.run(t, sid, switchChat, model.NewUserMessage("look it up again")))
	require.EqualValues(t, 2, f.toolRuns.Load())

	requests := f.snapshot()
	require.Len(t, requests, 4)
	require.Equal(t, "/chat/completions", requests[2].Path)
	require.Equal(t, switchChat, requests[2].Model)
	var messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Calls      json.RawMessage `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
	}
	require.NoError(t, json.Unmarshal(requests[2].Messages, &messages))
	var callIndex, resultIndex int
	for i, message := range messages {
		if message.Role == "assistant" && len(message.Calls) > 0 {
			callIndex = i
			require.JSONEq(t, `[{"id":"call_responses","type":"function","function":{"name":"lookup","arguments":"{}"}}]`, string(message.Calls))
		}
		if message.ToolCallID == "call_responses" {
			resultIndex = i
			require.Contains(t, string(message.Content), "case-42")
		}
	}
	require.Greater(t, callIndex, 0)
	require.Equal(t, callIndex+1, resultIndex)
	require.Contains(t, string(requests[2].Messages), "source answer")
	require.NotContains(t, string(requests[2].Messages), "opaque-switch-reasoning")
	require.NotContains(t, string(requests[2].Messages), "private Responses summary")

	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("return to Responses")))
	requests = f.snapshot()
	require.Len(t, requests, 5)
	require.Equal(t, "/responses", requests[4].Path)
	require.Equal(t, switchSource, requests[4].Model)
	input := string(requests[4].Input)
	require.Contains(t, input, "opaque-switch-reasoning")
	require.Contains(t, input, `"id":"rs_switch"`)
	require.Contains(t, input, `"call_id":"call_chat"`)
	require.Contains(t, input, "function_call_output")
	require.Contains(t, input, "chat answer")
	require.NotContains(t, input, "private Chat reasoning")
	require.EqualValues(t, 2, f.toolRuns.Load(), "switching back must not repeat either completed tool")
	require.JSONEq(t, string(saved.ProviderData.Payload), string(f.savedNative(t, sid).ProviderData.Payload))
}

func TestModelSwitchCitationFailsChatAndReplaysResponses(t *testing.T) {
	const (
		sid          = "citation-round-trip"
		nativeOutput = `{"type":"reasoning","id":"rs_citation","status":"completed","summary":[{"type":"summary_text","text":"source reasoning"}],"encrypted_content":"opaque-citation-reasoning"},` +
			`{"type":"message","id":"msg_citation","status":"completed","role":"assistant","content":[{"type":"output_text","text":"See the source.","annotations":[{"type":"url_citation","url":"https://example.com/source","title":"Source","start_index":8,"end_index":14}]}]}`
	)
	f := newSwitchFixture(t, switchResponsesReply(nativeOutput), switchTextReply("msg_back", "back answer"))
	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("find a source")))
	require.Len(t, f.snapshot(), 1)
	saved := f.savedNative(t, sid)
	require.Equal(t, switchSource, saved.ProviderData.Model)
	require.Equal(t, "See the source.", saved.Content)
	require.JSONEq(t, "["+nativeOutput+"]", string(saved.ProviderData.Payload))

	snapshot, err := json.Marshal(saved)
	require.NoError(t, err)
	assertSavedRecord := func() {
		t.Helper()
		current, err := json.Marshal(f.savedNative(t, sid))
		require.NoError(t, err)
		require.JSONEq(t, string(snapshot), string(current))
	}

	failure := f.run(t, sid, switchChat, model.NewUserMessage("continue in Chat"))
	require.Contains(t, failure, "annotations")
	require.Len(t, f.snapshot(), 1, "unsupported citation must fail before Chat HTTP")
	assertSavedRecord()

	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("return to Responses")))
	requests := f.snapshot()
	require.Len(t, requests, 2)
	require.Equal(t, "/responses", requests[1].Path)
	require.Equal(t, switchSource, requests[1].Model)
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(requests[1].Input, &items))
	var native []json.RawMessage
	var positions []int
	for i, raw := range items {
		var item struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(raw, &item))
		if item.ID == "rs_citation" || item.ID == "msg_citation" {
			native = append(native, raw)
			positions = append(positions, i)
		}
	}
	require.Len(t, native, 2)
	require.Equal(t, positions[0]+1, positions[1], "native items must remain in their original order")
	replayed, err := json.Marshal(native)
	require.NoError(t, err)
	require.JSONEq(t, "["+nativeOutput+"]", string(replayed), "the original text and citation must survive replay")
	assertSavedRecord()
}

func (f *switchFixture) seedPending(t *testing.T, sid string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user := model.NewUserMessage("look up the case")
	ch, err := f.models[switchSource].GenerateContent(ctx, &model.Request{Messages: []model.Message{user}})
	require.NoError(t, err)
	var pending *model.Response
	for response := range ch {
		pending = response
	}
	require.NotNil(t, pending)
	require.Nil(t, pending.Error)
	require.True(t, pending.IsToolCallResponse())
	s, err := f.sessions.CreateSession(ctx, session.Key{AppName: "switch", UserID: "user", SessionID: sid}, session.StateMap{})
	require.NoError(t, err)
	require.NoError(t, f.sessions.AppendEvent(ctx, s, event.NewResponseEvent(
		"interrupted", "user", &model.Response{Choices: []model.Choice{{Message: user}}},
	)))
	require.NoError(t, f.sessions.AppendEvent(ctx, s, event.NewResponseEvent("interrupted", "switch", pending)))
}

func TestModelSwitchWithPendingNativeTool(t *testing.T) {
	for _, name := range []string{switchSource, switchTarget, switchChat} {
		t.Run(name, func(t *testing.T) {
			f := newSwitchFixture(t, switchResponsesReply(switchFirstOutput), switchTextReply("msg_resume", "resumed"))
			f.seedPending(t, "pending")
			failure := f.run(t, "pending", name, model.Message{}, agent.WithResume(true))
			requests := f.snapshot()
			if name == switchSource {
				require.Empty(t, failure)
				require.Len(t, requests, 2)
				require.EqualValues(t, 1, f.toolRuns.Load())
				require.Contains(t, string(requests[1].Input), `"call_id":"call_responses"`)
				require.Contains(t, string(requests[1].Input), "function_call_output")
				return
			}
			require.Contains(t, failure, "cannot resume native tool calls")
			require.Len(t, requests, 1, "changing models must fail before making a request")
			require.Zero(t, f.toolRuns.Load(), "changing models must fail before executing a pending call")
		})
	}
}

func TestModelSwitchReturnsUpstreamRejection(t *testing.T) {
	f := newSwitchFixture(t,
		switchResponsesReply(switchFirstOutput),
		switchTextReply("msg_source", "source answer"),
		switchReply{status: http.StatusBadRequest, body: `{"error":{"message":"synthetic invalid encrypted content","type":"invalid_request_error","code":"invalid_encrypted_content"}}`},
	)
	const sid = "rejected"
	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("remember this")))
	failure := f.run(t, sid, switchTarget, model.NewUserMessage("continue"))
	require.Contains(t, failure, "synthetic invalid encrypted content")
	requests := f.snapshot()
	require.Len(t, requests, 3, "an upstream rejection must not silently retry without native reasoning")
	require.Equal(t, switchTarget, requests[2].Model)
	require.Contains(t, string(requests[2].Input), "opaque-switch-reasoning")
	require.Contains(t, string(requests[2].Input), `"id":"rs_switch"`)
	require.Equal(t, switchSource, f.savedNative(t, sid).ProviderData.Model)
}

func TestGraphModelSwitchWithCompletedHistory(t *testing.T) {
	for _, target := range []string{switchTarget, switchChat} {
		t.Run(target, func(t *testing.T) {
			last := switchTextReply("msg_graph", "graph answer")
			if target == switchChat {
				last = switchChatReply(`{"role":"assistant","content":"graph answer"}`, "stop")
			}
			f := newSwitchFixture(t, switchResponsesReply(switchFirstOutput), switchTextReply("msg_source", "source answer"), last)
			const sid = "graph"
			require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("look it up")))
			saved := f.savedNative(t, sid)
			history := []model.Message{
				model.NewUserMessage("look it up"),
				saved,
				model.NewToolMessage("call_responses", "lookup", "case-42"),
				model.NewAssistantMessage("source answer"),
				model.NewUserMessage("continue"),
			}
			g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
				AddLLMNode("llm", f.models[target], "Use the conversation.", nil,
					graph.WithGenerationConfig(model.GenerationConfig{Stream: false})).
				SetEntryPoint("llm").Compile()
			require.NoError(t, err)
			executor, err := graph.NewExecutor(g)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events, err := executor.Execute(ctx, graph.State{graph.StateKeyMessages: history},
				&agent.Invocation{InvocationID: "model-switch-graph"})
			require.NoError(t, err)
			for evt := range events {
				require.Nil(t, evt.Error)
			}
			require.NoError(t, ctx.Err())
			requests := f.snapshot()
			require.Len(t, requests, 3)
			require.Equal(t, target, requests[2].Model)
			if target == switchTarget {
				require.Equal(t, "/responses", requests[2].Path)
				require.Contains(t, string(requests[2].Input), "opaque-switch-reasoning")
				require.Contains(t, string(requests[2].Input), `"call_id":"call_responses"`)
			} else {
				require.Equal(t, "/chat/completions", requests[2].Path)
				require.NotContains(t, string(requests[2].Messages), "opaque-switch-reasoning")
				require.Contains(t, string(requests[2].Messages), `"tool_call_id":"call_responses"`)
			}
			require.EqualValues(t, 1, f.toolRuns.Load())
			require.JSONEq(t, string(saved.ProviderData.Payload), string(f.savedNative(t, sid).ProviderData.Payload))
		})
	}
}

func TestModelSwitchChatReportsUnsupportedToolImage(t *testing.T) {
	f := newSwitchFixture(t, switchResponsesReply(switchFirstOutput), switchTextReply("msg_source", "source answer"))
	require.Empty(t, f.run(t, "original", switchSource, model.NewUserMessage("look it up")))
	native := f.savedNative(t, "original")
	result := model.NewToolMessage("call_responses", "lookup", "photo attached")
	result.ContentParts = []model.ContentPart{{
		Type:  model.ContentTypeImage,
		Image: &model.Image{URL: "https://example.invalid/photo.png"},
	}}
	failure := f.run(t, "image", switchChat, model.NewUserMessage("describe the photo"),
		agent.WithMessages([]model.Message{model.NewUserMessage("look it up"), native, result}))
	require.NotEmpty(t, failure)
	require.Contains(t, failure, "image")
	require.Len(t, f.snapshot(), 2, "unsupported image must be reported before Chat HTTP")
	require.EqualValues(t, 1, f.toolRuns.Load())
}

func TestModelSwitchChatToResponsesWithoutNativeHistory(t *testing.T) {
	f := newSwitchFixture(t,
		switchChatReply(`{"role":"assistant","reasoning_content":"previous chat reasoning","tool_calls":[{"id":"call_chat","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`, "tool_calls"),
		switchChatReply(`{"role":"assistant","content":"chat answer"}`, "stop"),
		switchTextReply("msg_target", "responses answer"),
	)
	const sid = "legacy-chat"
	require.Empty(t, f.run(t, sid, switchChat, model.NewUserMessage("look it up")))
	require.EqualValues(t, 1, f.toolRuns.Load())
	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("continue")))
	requests := f.snapshot()
	require.Len(t, requests, 3)
	require.Equal(t, "/responses", requests[2].Path)
	input := string(requests[2].Input)
	require.Contains(t, input, `"call_id":"call_chat"`)
	require.Contains(t, input, "function_call_output")
	require.Contains(t, input, "chat answer")
	require.NotContains(t, input, "previous chat reasoning")
	require.EqualValues(t, 1, f.toolRuns.Load())
}

func TestModelSwitchChatPreservesAfterModelEdits(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		if args.Response == nil || len(args.Response.Choices) == 0 {
			return nil, nil
		}
		message := &args.Response.Choices[0].Message
		if message.ProviderData == nil {
			return nil, nil
		}
		if len(message.ToolCalls) != 0 {
			message.ToolCalls[0].Function.Arguments = json.RawMessage(`{"redacted":true}`)
		}
		if message.Content == "source answer" {
			message.Content = "redacted answer"
		}
		return nil, nil
	})
	f := newSwitchFixtureWithCallbacks(t, callbacks,
		switchResponsesReply(switchFirstOutput),
		switchTextReply("msg_source", "source answer"),
		switchChatReply(`{"role":"assistant","content":"chat answer"}`, "stop"),
	)
	const sid = "callback-edits"
	require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("look it up")))
	saved := f.savedNative(t, sid)
	require.JSONEq(t, "["+switchFirstOutput+"]", string(saved.ProviderData.Payload))
	require.EqualValues(t, 1, f.toolRuns.Load())
	require.Empty(t, f.run(t, sid, switchChat, model.NewUserMessage("continue")))
	requests := f.snapshot()
	require.Len(t, requests, 3)
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Calls   []struct {
			Function struct {
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	require.NoError(t, json.Unmarshal(requests[2].Messages, &messages))
	var calls int
	for _, message := range messages {
		for _, call := range message.Calls {
			calls++
			require.JSONEq(t, `{"redacted":true}`, call.Function.Arguments)
		}
	}
	require.Equal(t, 1, calls)
	require.Contains(t, string(requests[2].Messages), "redacted answer")
	require.NotContains(t, string(requests[2].Messages), "source answer")
	require.EqualValues(t, 1, f.toolRuns.Load())
}
