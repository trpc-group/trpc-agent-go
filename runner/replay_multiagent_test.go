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
	if m.info.ReplayProtocol != "" {
		if err := msg.SetReplayData(
			m.info.ReplayProtocol,
			m.info.Name,
			json.RawMessage(replayTestPayload),
		); err != nil {
			return nil, err
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

func newNativeReplayTestModel() *replayTestModel {
	return &replayTestModel{
		info:   model.Info{Name: "native-model", ReplayProtocol: "test.native"},
		answer: "native findings",
	}
}

func runReplayTest(
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
	native := newNativeReplayTestModel()
	legacy := &replayTestModel{info: model.Info{Name: "legacy-model"}, answer: "legacy summary"}
	chain := chainagent.New("chain", chainagent.WithSubAgents([]agent.Agent{
		llmagent.New("native-agent", llmagent.WithModel(native)),
		llmagent.New("legacy-agent", llmagent.WithModel(legacy)),
	}))
	r := runner.NewRunner("replay-app", chain,
		runner.WithSessionService(inmemory.NewSessionService()))

	for turn, text := range []string{"investigate", "continue"} {
		events := runReplayTest(t, r, "session", text)
		require.Empty(t, replayTestError(events), "turn %d", turn)
	}

	nativeRequests := native.recorded()
	require.Len(t, nativeRequests, 2)
	var continued bool
	for _, msg := range nativeRequests[1].Messages {
		if msg.Replay != nil {
			_, err := msg.ReplayDataFor("test.native", "native-model")
			require.NoError(t, err)
			continued = true
		}
	}
	require.True(t, continued, "the native agent continues its own records natively")

	legacyRequests := legacy.recorded()
	require.Len(t, legacyRequests, 2)
	for _, req := range legacyRequests {
		var quoted bool
		for _, msg := range req.Messages {
			require.Nil(t, msg.Replay, "a legacy agent receives no native records")
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

func TestRunner_AfterModelCallbacksCannotBypassNativeRecords(t *testing.T) {
	editArguments := func(rsp *model.Response) {
		rsp.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"a":1000}`)
	}
	for _, tc := range []struct {
		name     string
		legacy   bool
		noTool   bool
		callback func(t *testing.T, rsp *model.Response) (*model.Response, error)
		wantErr  string
		// wantInput is the calculator input when the tool must run once.
		wantInput int
	}{
		{
			name: "unchanged native record",
			callback: func(*testing.T, *model.Response) (*model.Response, error) {
				return nil, nil
			},
			wantInput: 1,
		},
		{
			name: "edited with the record retained",
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				editArguments(rsp)
				return nil, nil
			},
			wantErr: "projection changed",
		},
		{
			name: "edited with the record dropped",
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				editArguments(rsp)
				rsp.Choices[0].Message.Replay = nil
				return nil, nil
			},
			wantErr: "was removed",
		},
		{
			name: "replaced without a record",
			callback: func(*testing.T, *model.Response) (*model.Response, error) {
				msg := model.NewAssistantMessage("")
				msg.ToolCalls = []model.ToolCall{replayTestToolCall(`{"a":1000}`)}
				return &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}, nil
			},
			wantErr: "was removed",
		},
		{
			name: "edited and rebound",
			callback: func(t *testing.T, rsp *model.Response) (*model.Response, error) {
				msg := &rsp.Choices[0].Message
				payload, err := msg.ReplayDataFor("test.native", "native-model")
				require.NoError(t, err)
				editArguments(rsp)
				require.NoError(t, msg.SetReplayData("test.native", "native-model", payload))
				return nil, nil
			},
			wantErr: "was removed",
		},
		{
			name: "choice appended to native response",
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				msg := model.NewAssistantMessage("")
				msg.ToolCalls = []model.ToolCall{replayTestToolCall(`{"a":1000}`)}
				rsp.Choices = append(rsp.Choices, model.Choice{Index: 1, Message: msg})
				return nil, nil
			},
			wantErr: "native response choices were added or removed",
		},
		{
			name:   "reasoning only with the record dropped",
			noTool: true,
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				rsp.Choices[0].Message.Replay = nil
				return nil, nil
			},
			wantErr: "was removed",
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
			m := newNativeReplayTestModel()
			if tc.legacy {
				m.info = model.Info{Name: "legacy-model"}
			}
			if !tc.noTool {
				call := replayTestToolCall(`{"a":1}`)
				m.toolCall = &call
			}
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

			errMessage := replayTestError(runReplayTest(t, r, "session", "add one"))
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
	r.AfterModel(func(context.Context, *model.AfterModelArgs) (*model.AfterModelResult, error) {
		msg := model.NewAssistantMessage("")
		msg.ToolCalls = []model.ToolCall{replayTestToolCall(`{"a":1000}`)}
		return &model.AfterModelResult{CustomResponse: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: msg}},
		}}, nil
	})
}

func TestRunner_PluginCannotReplaceNativeRecord(t *testing.T) {
	m := newNativeReplayTestModel()
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

	errMessage := replayTestError(runReplayTest(t, r, "session", "add one"))
	require.Contains(t, errMessage, "was removed")
	require.Zero(t, executed.Load())
}

func TestRunner_NativeAgentRejectsRewrittenOwnRecord(t *testing.T) {
	native := newNativeReplayTestModel()
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

	require.Empty(t, replayTestError(runReplayTest(t, r, "session", "investigate")))
	errMessage := replayTestError(runReplayTest(t, r, "session", "continue"))
	require.Contains(t, errMessage, "projection changed",
		"a stale projection in the same conversation must not be sent")
	require.Len(t, native.recorded(), 1, "the rejected request never reaches the model")
}
