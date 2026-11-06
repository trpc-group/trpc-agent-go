//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const replayTestPayload = `[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}]`

// replayTestModel records requests and answers with one assistant message.
// The tool call, when set, is only part of the first answer. A model with a
// replay protocol binds its answer as a native record.
type replayTestModel struct {
	info     model.Info
	answer   string
	toolCall *model.ToolCall
	mu       sync.Mutex
	requests []*model.Request
}

func (m *replayTestModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	first := len(m.requests) == 1
	m.mu.Unlock()
	msg := model.NewAssistantMessage(m.answer)
	if m.toolCall != nil && first {
		msg.ToolCalls = []model.ToolCall{*m.toolCall}
	}
	if m.info.ProviderDataProtocol != "" {
		msg.ProviderData = &model.ProviderData{
			Protocol: m.info.ProviderDataProtocol,
			Model:    m.info.Name,
			Payload:  json.RawMessage(replayTestPayload),
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done:    true,
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Index: 0, Message: msg}},
	}
	close(ch)
	return ch, nil
}

func (m *replayTestModel) Info() model.Info {
	return m.info
}

func (m *replayTestModel) recorded() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.requests...)
}

func newNativeProviderDataTestModel() *replayTestModel {
	return &replayTestModel{
		info:   model.Info{Name: "native-model", ProviderDataProtocol: "test.native"},
		answer: "native findings",
	}
}

func runProviderDataTest(
	t *testing.T,
	r runner.Runner,
	sessionID string,
	text string,
) []*event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := r.Run(ctx, "user", sessionID, model.NewUserMessage(text))
	require.NoError(t, err)
	var out []*event.Event
	for evt := range events {
		out = append(out, evt)
	}
	return out
}

func replayTestError(events []*event.Event) string {
	for _, evt := range events {
		if evt.Error != nil {
			return evt.Error.Message
		}
	}
	return ""
}

func TestRunner_NativeAgentFeedsLegacyAgent(t *testing.T) {
	native := newNativeProviderDataTestModel()
	legacy := &replayTestModel{info: model.Info{Name: "legacy-model"}, answer: "legacy summary"}
	chain := chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{
		llmagent.New("native-agent", llmagent.WithModel(native)),
		llmagent.New("legacy-agent", llmagent.WithModel(legacy)),
	}))
	r := runner.NewRunner("replay-app", chain,
		runner.WithSessionService(inmemory.NewSessionService()))

	for turn, text := range []string{"investigate", "continue"} {
		events := runProviderDataTest(t, r, "session", text)
		require.Empty(t, replayTestError(events), "turn %d", turn)
	}

	nativeRequests := native.recorded()
	require.Len(t, nativeRequests, 2)
	var continued bool
	for _, msg := range nativeRequests[1].Messages {
		if msg.ProviderData != nil {
			require.NotNil(t, msg.ProviderData)
			continued = true
		}
	}
	require.True(t, continued, "the native agent continues its own records natively")

	legacyRequests := legacy.recorded()
	require.Len(t, legacyRequests, 2)
	for _, req := range legacyRequests {
		var quoted bool
		for _, msg := range req.Messages {
			require.Nil(t, msg.ProviderData, "a legacy agent receives no native records")
			if msg.Role == model.RoleUser && strings.Contains(msg.Content, "native findings") {
				quoted = true
			}
		}
		require.True(t, quoted, "the native answer is quoted as user context")
	}
}

func replayTestToolCall(arguments string) model.ToolCall {
	return model.ToolCall{
		Type: "function",
		ID:   "call_1",
		Function: model.FunctionDefinitionParam{
			Name:      "calculator",
			Arguments: []byte(arguments),
		},
	}
}

func TestRunner_AfterModelCallbacksMayEditNativeProjections(t *testing.T) {
	editArguments := func(rsp *model.Response) {
		rsp.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"a":1000}`)
	}
	for _, tc := range []struct {
		name     string
		legacy   bool
		callback func(t *testing.T, rsp *model.Response) (*model.Response, error)
		wantErr  string
		// wantInput is the calculator input when the tool must run once.
		wantInput int
	}{
		{
			name: "edited with the record retained",
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				editArguments(rsp)
				return nil, nil
			},
			wantInput: 1000,
		},
		{
			name: "replaced without a record",
			callback: func(*testing.T, *model.Response) (*model.Response, error) {
				msg := model.NewAssistantMessage("")
				msg.ToolCalls = []model.ToolCall{replayTestToolCall(`{"a":1000}`)}
				return &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}, nil
			},
			wantInput: 1000,
		},
		{
			name: "foreign model binding remains invalid",
			callback: func(t *testing.T, _ *model.Response) (*model.Response, error) {
				msg := model.NewAssistantMessage("")
				msg.ProviderData = &model.ProviderData{
					Protocol: "test.native",
					Model:    "other-model",
					Payload:  json.RawMessage(`[]`),
				}
				return &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}, nil
			},
			wantErr: "invalid model response",
		},
		{
			name: "callback error",
			callback: func(*testing.T, *model.Response) (*model.Response, error) {
				return nil, errors.New("blocked by policy")
			},
			wantErr: "blocked by policy",
		},
		{
			name:   "legacy replacement",
			legacy: true,
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				if len(rsp.Choices) == 0 || len(rsp.Choices[0].Message.ToolCalls) == 0 {
					return nil, nil
				}
				msg := model.NewAssistantMessage("")
				msg.ToolCalls = []model.ToolCall{replayTestToolCall(`{"a":41}`)}
				return &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}, nil
			},
			wantInput: 41,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newNativeProviderDataTestModel()
			if tc.legacy {
				m.info = model.Info{Name: "legacy-model"}
			}
			call := replayTestToolCall(`{"a":1}`)
			m.toolCall = &call
			var executed, input atomic.Int32
			calculator := function.NewFunctionTool(func(_ context.Context, in struct {
				A int `json:"a"`
			}) (int, error) {
				executed.Add(1)
				input.Store(int32(in.A))
				return in.A + 1, nil
			}, function.WithName("calculator"))
			callbacks := model.NewCallbacks().RegisterAfterModel(func(
				_ context.Context,
				_ *model.Request,
				rsp *model.Response,
				_ error,
			) (*model.Response, error) {
				if rsp == nil || len(m.recorded()) > 1 {
					return nil, nil
				}
				return tc.callback(t, rsp)
			})
			r := runner.NewRunner("replay-app", llmagent.New("agent",
				llmagent.WithModel(m),
				llmagent.WithTools([]tool.Tool{calculator}),
				llmagent.WithModelCallbacks(callbacks),
			), runner.WithSessionService(inmemory.NewSessionService()))

			errMessage := replayTestError(runProviderDataTest(t, r, "session", "add one"))
			if tc.wantErr != "" {
				require.Contains(t, errMessage, tc.wantErr)
				require.Zero(t, executed.Load(), "a rejected response must not run tools")
				require.Len(t, m.recorded(), 1)
				return
			}
			require.Empty(t, errMessage)
			require.EqualValues(t, 1, executed.Load())
			require.EqualValues(t, tc.wantInput, input.Load())
		})
	}
}

type replacingPlugin struct{}

func (replacingPlugin) Name() string { return "replacing" }

func (replacingPlugin) Register(r *plugin.Registry) {
	replaced := false
	r.AfterModel(func(context.Context, *model.AfterModelArgs) (*model.AfterModelResult, error) {
		if replaced {
			return nil, nil
		}
		replaced = true
		msg := model.NewAssistantMessage("")
		msg.ToolCalls = []model.ToolCall{replayTestToolCall(`{"a":1000}`)}
		return &model.AfterModelResult{CustomResponse: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: msg}},
		}}, nil
	})
}

func TestRunner_PluginMayReplaceNativeResponse(t *testing.T) {
	m := newNativeProviderDataTestModel()
	call := replayTestToolCall(`{"a":1}`)
	m.toolCall = &call
	var executed atomic.Int32
	calculator := function.NewFunctionTool(func(_ context.Context, in struct {
		A int `json:"a"`
	}) (int, error) {
		executed.Add(1)
		return in.A + 1, nil
	}, function.WithName("calculator"))
	r := runner.NewRunner("replay-app", llmagent.New("agent",
		llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{calculator}),
	),
		runner.WithSessionService(inmemory.NewSessionService()),
		runner.WithPlugins(replacingPlugin{}),
	)

	errMessage := replayTestError(runProviderDataTest(t, r, "session", "add one"))
	require.Empty(t, errMessage)
	require.EqualValues(t, 1, executed.Load())
}

func TestRunner_NativeAgentProjectorEditsDisplayProjection(t *testing.T) {
	native := newNativeProviderDataTestModel()
	rewrite := func(_ *agent.Invocation, _ event.Event, msg model.Message) model.Message {
		if msg.Role == model.RoleAssistant {
			msg.Content = "rewritten findings"
		}
		return msg
	}
	r := runner.NewRunner("replay-app", llmagent.New("native-agent",
		llmagent.WithModel(native),
		llmagent.WithEventMessageProjector(rewrite),
	), runner.WithSessionService(inmemory.NewSessionService()))

	require.Empty(t, replayTestError(runProviderDataTest(t, r, "session", "investigate")))
	require.Empty(t, replayTestError(runProviderDataTest(t, r, "session", "continue")))
	recorded := native.recorded()
	require.Len(t, recorded, 2)
	var sawEditedProviderData bool
	for _, message := range recorded[1].Messages {
		if message.Role != model.RoleAssistant || message.Content != "rewritten findings" {
			continue
		}
		sawEditedProviderData = true
		require.NotNil(t, message.ProviderData)
		payload := message.ProviderData.Payload
		require.JSONEq(t, replayTestPayload, string(payload))
	}
	require.True(t, sawEditedProviderData, "the projector's display edit reaches the next request")
}
