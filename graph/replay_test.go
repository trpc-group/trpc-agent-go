//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const (
	nativeTestProvider = "test.native"
	nativeTestModel    = "native-model"
	nativeTestPayload  = `[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}]`
)

var nativeTestInfo = model.Info{Name: nativeTestModel, ReplayProtocol: nativeTestProvider}

func newNativeTestMessage(t *testing.T) model.Message {
	t.Helper()
	msg := legacyCacheKeyMessage()
	require.NoError(t, msg.SetReplayData(
		nativeTestProvider,
		nativeTestModel,
		json.RawMessage(nativeTestPayload),
	))
	return msg
}

func requireNativeTestMessage(t *testing.T, msg model.Message) {
	t.Helper()
	payload, err := msg.ReplayDataFor(nativeTestProvider, nativeTestModel)
	require.NoError(t, err)
	require.JSONEq(t, nativeTestPayload, string(payload))
}

func TestStateCopiesPreserveNativeReplay(t *testing.T) {
	native := newNativeTestMessage(t)
	pointer := native
	state := State{
		StateKeyMessages: []model.Message{native},
		"message":        native,
		"pointer":        &pointer,
		"response": &model.Response{
			Choices: []model.Choice{{Message: native, Delta: native}},
		},
		"nested":       map[string]any{"messages": []any{native}},
		"record":       native.Replay,
		"record_value": *native.Replay,
	}
	requireRecord := func(t *testing.T, record *model.ReplayData) {
		t.Helper()
		bound := native
		bound.Replay = record
		requireNativeTestMessage(t, bound)
	}
	records := func(t *testing.T, s State) []*model.ReplayData {
		t.Helper()
		// JSON-safe copies keep element types but return slices as []any.
		var first model.Message
		switch messages := s[StateKeyMessages].(type) {
		case []model.Message:
			first = messages[0]
		case []any:
			first = messages[0].(model.Message)
		default:
			t.Fatalf("unexpected messages type %T", messages)
		}
		response := s["response"].(*model.Response)
		nested := s["nested"].(map[string]any)["messages"].([]any)
		return []*model.ReplayData{
			s["record"].(*model.ReplayData),
			first.Replay,
			s["message"].(model.Message).Replay,
			s["pointer"].(*model.Message).Replay,
			response.Choices[0].Message.Replay,
			response.Choices[0].Delta.Replay,
			nested[0].(model.Message).Replay,
		}
	}
	other := legacyCacheKeyMessage()
	require.NoError(t, other.SetReplayData(
		nativeTestProvider, nativeTestModel, json.RawMessage(`[{"type":"reasoning","id":"rs_other"}]`),
	))
	otherRecord, err := json.Marshal(other.Replay)
	require.NoError(t, err)
	check := func(t *testing.T, copied State) {
		t.Helper()
		value := copied["record_value"].(model.ReplayData)
		requireRecord(t, &value)
		for _, record := range records(t, copied) {
			requireRecord(t, record)
		}
	}
	checkIsolated := func(t *testing.T, copied State) {
		t.Helper()
		check(t, copied)
		// Decoding into a copied record, as restoring a checkpoint does, must
		// not reach the source state or any other copy.
		for _, record := range records(t, copied) {
			require.NoError(t, json.Unmarshal(otherRecord, record))
		}
		requireNativeTestMessage(t, native)
		for _, record := range records(t, state) {
			requireRecord(t, record)
		}
	}

	// Clone is a shallow map copy that shares every value.
	t.Run("Clone", func(t *testing.T) { check(t, state.Clone()) })
	t.Run("deepCopyAny", func(t *testing.T) { checkIsolated(t, deepCopyAny(state).(State)) })
	t.Run("safeClone", func(t *testing.T) { checkIsolated(t, state.safeClone()) })
	t.Run("checkpoint copy", func(t *testing.T) {
		checkpoint := (&Checkpoint{ChannelValues: map[string]any(state.safeClone())}).Copy()
		data, err := json.Marshal(checkpoint.ChannelValues[StateKeyMessages])
		require.NoError(t, err)
		var messages []model.Message
		require.NoError(t, json.Unmarshal(data, &messages))
		requireNativeTestMessage(t, messages[0])
	})
	t.Run("JSON", func(t *testing.T) {
		data, err := json.Marshal(state.safeClone())
		require.NoError(t, err)
		var decoded struct {
			Messages []model.Message `json:"messages"`
			Pointer  model.Message   `json:"pointer"`
		}
		require.NoError(t, json.Unmarshal(data, &decoded))
		requireNativeTestMessage(t, decoded.Messages[0])
		requireNativeTestMessage(t, decoded.Pointer)
	})
}

func TestDefaultCachePolicy_NativeReplayKeys(t *testing.T) {
	policy := DefaultCachePolicy()
	key := func(input any) string {
		t.Helper()
		out, err := policy.KeyFunc(sanitizeForCacheKey(input))
		require.NoError(t, err)
		return string(out)
	}
	native := newNativeTestMessage(t)
	input := func(msg model.Message) State {
		return State{StateKeyMessages: []model.Message{msg}, "message": msg}
	}

	require.Equal(t, key(input(native)), key(input(newNativeTestMessage(t))),
		"equal native state must produce a stable key")
	require.Equal(t, legacyCacheKeyGolden["message_direct"], key(legacyCacheKeyMessage()))
	require.NotEqual(t, key(input(legacyCacheKeyMessage())), key(input(native)),
		"native replay data is part of the cached input")

	other := legacyCacheKeyMessage()
	require.NoError(t, other.SetReplayData(
		nativeTestProvider,
		nativeTestModel,
		json.RawMessage(`[{"type":"reasoning","id":"rs_2"}]`),
	))
	require.NotEqual(t, key(input(native)), key(input(other)))
}

type nativeReplayTestModel struct {
	info     model.Info
	response *model.Response
	requests int
}

func (m *nativeReplayTestModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	m.requests++
	ch := make(chan *model.Response, 1)
	ch <- m.response
	close(ch)
	return ch, nil
}

func (m *nativeReplayTestModel) Info() model.Info {
	return m.info
}

type replacingTestPlugin struct {
	response *model.Response
}

func (p replacingTestPlugin) Name() string { return "replacing" }

func (p replacingTestPlugin) Register(r *plugin.Registry) {
	r.AfterModel(func(context.Context, *model.AfterModelArgs) (*model.AfterModelResult, error) {
		return &model.AfterModelResult{CustomResponse: p.response}, nil
	})
}

func injectedToolResponse() *model.Response {
	msg := legacyCacheKeyMessage()
	msg.ToolCalls[0].Function.Arguments = []byte(`{"q":"rm -rf"}`)
	return &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}
}

func TestProcessModelResponse_ValidatesNativeRecordBeforeDispatch(t *testing.T) {
	editArguments := func(rsp *model.Response) {
		rsp.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"q":"rm -rf"}`)
	}
	for _, tc := range []struct {
		name     string
		info     *model.Info
		legacy   bool
		noTool   bool
		plugin   *model.Response
		callback func(t *testing.T, rsp *model.Response) (*model.Response, error)
		wantErr  string
	}{
		{name: "unchanged record"},
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
			wantErr: "native message record of choice 0 was removed",
		},
		{
			name: "replaced without a record",
			callback: func(*testing.T, *model.Response) (*model.Response, error) {
				return injectedToolResponse(), nil
			},
			wantErr: "was removed",
		},
		{
			name:    "plugin replaced without a record",
			plugin:  injectedToolResponse(),
			wantErr: "was removed",
		},
		{
			name: "edited and rebound",
			callback: func(t *testing.T, rsp *model.Response) (*model.Response, error) {
				msg := &rsp.Choices[0].Message
				payload, err := msg.ReplayDataFor(nativeTestProvider, nativeTestModel)
				require.NoError(t, err)
				editArguments(rsp)
				require.NoError(t, msg.SetReplayData(nativeTestProvider, nativeTestModel, payload))
				return nil, nil
			},
			wantErr: "was removed",
		},
		{
			name: "choice index changed",
			callback: func(_ *testing.T, rsp *model.Response) (*model.Response, error) {
				rsp.Choices[0].Index = 1
				return nil, nil
			},
			wantErr: "was removed",
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
			name:    "record of another producer",
			info:    &model.Info{Name: nativeTestModel},
			wantErr: "invalid model response",
		},
		{
			name:   "legacy replacement",
			legacy: true,
			callback: func(*testing.T, *model.Response) (*model.Response, error) {
				return injectedToolResponse(), nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := nativeTestInfo
			if tc.info != nil {
				info = *tc.info
			}
			msg := newNativeTestMessage(t)
			if tc.noTool {
				msg = withoutNativeReplay([]model.Message{msg})[0]
				msg.ToolCalls = nil
				require.NoError(t, msg.SetReplayData(
					nativeTestProvider, nativeTestModel, json.RawMessage(nativeTestPayload),
				))
			}
			if tc.legacy {
				info = model.Info{Name: "legacy-model"}
				msg.Replay = nil
			}
			callbacks := model.NewCallbacks()
			if tc.callback != nil {
				callbacks.RegisterAfterModel(func(
					_ context.Context,
					_ *model.Request,
					rsp *model.Response,
					_ error,
				) (*model.Response, error) {
					return tc.callback(t, rsp)
				})
			}
			ctx := context.Background()
			if tc.plugin != nil {
				manager, err := plugin.NewManager(replacingTestPlugin{response: tc.plugin})
				require.NoError(t, err)
				ctx = agent.NewInvocationContext(ctx, &agent.Invocation{Plugins: manager})
			}
			config := modelResponseConfig{
				Response:       &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}},
				ModelCallbacks: callbacks,
				EventChan:      make(chan *event.Event, 1),
				InvocationID:   "inv",
				LLMModel:       &nativeReplayTestModel{info: info},
				Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}},
				Span:           oteltrace.SpanFromContext(context.Background()),
			}
			_, evt, err := processModelResponse(ctx, config)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Nil(t, evt)
				require.Empty(t, config.EventChan,
					"a rejected native response must not be emitted for dispatch")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, evt)
			if tc.legacy {
				require.JSONEq(t, `{"q":"rm -rf"}`,
					string(evt.Response.Choices[0].Message.ToolCalls[0].Function.Arguments),
					"legacy callbacks keep overriding responses")
			}
		})
	}
}

func TestLLMRunnerExecuteModel_ValidatesNativeHistory(t *testing.T) {
	tracer := oteltrace.NewNoopTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test")
	defer span.End()
	run := func(info model.Info, history []model.Message) (*nativeReplayTestModel, error) {
		llm := &nativeReplayTestModel{
			info: info,
			response: &model.Response{
				Done:    true,
				Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}},
			},
		}
		runner := &llmRunner{llmModel: llm, nodeID: "llm"}
		_, err := runner.executeModel(ctx, State{}, history, span, "")
		return llm, err
	}
	native := newNativeTestMessage(t)
	native.ToolCalls = nil
	require.NoError(t, native.SetReplayData(
		nativeTestProvider, nativeTestModel, json.RawMessage(nativeTestPayload),
	))
	history := []model.Message{model.NewUserMessage("hi"), native, model.NewUserMessage("next")}

	llm, err := run(nativeTestInfo, history)
	require.NoError(t, err)
	require.Equal(t, 1, llm.requests)

	llm, err = run(model.Info{Name: "other-model", ReplayProtocol: nativeTestProvider}, history)
	require.ErrorContains(t, err, "native replay message 1")
	require.Zero(t, llm.requests, "a foreign native record must not reach the model")

	legacy := withoutNativeReplay(history)
	llm, err = run(model.Info{Name: "legacy-model"}, legacy)
	require.NoError(t, err)
	require.Equal(t, 1, llm.requests)
}

func withoutNativeReplay(messages []model.Message) []model.Message {
	out := append([]model.Message(nil), messages...)
	for i := range out {
		out[i].Replay = nil
	}
	return out
}
