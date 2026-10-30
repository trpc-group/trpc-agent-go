//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package modelreplay

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type infoModel struct {
	info model.Info
}

func (m infoModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return nil, nil
}

func (m infoModel) Info() model.Info {
	return m.info
}

var nativeInfo = model.Info{Name: "native-model", ReplayProtocol: "test.native"}

func nativeMessage(t *testing.T) model.Message {
	t.Helper()
	msg := model.NewAssistantMessage("answer")
	require.NoError(t, msg.SetReplayData(
		nativeInfo.ReplayProtocol,
		nativeInfo.Name,
		json.RawMessage(`[{"type":"reasoning","id":"rs_1"}]`),
	))
	return msg
}

func TestCandidateInfo(t *testing.T) {
	legacyA := infoModel{info: model.Info{Name: "chat-a"}}
	legacyB := infoModel{info: model.Info{Name: "chat-b"}}

	info, err := CandidateInfo([]model.Model{legacyA, legacyB})
	require.NoError(t, err, "heterogeneous legacy candidates remain supported")
	require.Equal(t, legacyA.info, info)

	info, err = CandidateInfo([]model.Model{infoModel{info: nativeInfo}, infoModel{info: nativeInfo}})
	require.NoError(t, err)
	require.Equal(t, nativeInfo, info)

	_, err = CandidateInfo(nil)
	require.Error(t, err)
	for name, candidates := range map[string][]model.Model{
		"native then legacy": {infoModel{info: nativeInfo}, legacyA},
		"legacy then native": {legacyA, infoModel{info: nativeInfo}},
		"another model name": {
			infoModel{info: nativeInfo},
			infoModel{info: model.Info{Name: "other", ReplayProtocol: nativeInfo.ReplayProtocol}},
		},
		"another protocol": {
			infoModel{info: nativeInfo},
			infoModel{info: model.Info{Name: nativeInfo.Name, ReplayProtocol: "other"}},
		},
	} {
		_, err := CandidateInfo(candidates)
		require.Error(t, err, name)
	}
}

func TestValidateMessages(t *testing.T) {
	legacy := []model.Message{model.NewUserMessage("q"), model.NewAssistantMessage("a")}
	require.False(t, MessagesHaveReplay(legacy))
	require.NoError(t, ValidateMessages(legacy, model.Info{Name: "legacy"}),
		"legacy histories never fail validation")

	native := append(append([]model.Message(nil), legacy...), nativeMessage(t))
	require.True(t, MessagesHaveReplay(native))
	require.NoError(t, ValidateMessages(native, nativeInfo))
	require.ErrorContains(t, ValidateMessages(native, model.Info{Name: nativeInfo.Name}),
		"native replay message 2")
	require.Error(t, ValidateMessages(native, model.Info{
		Name:           "other-model",
		ReplayProtocol: nativeInfo.ReplayProtocol,
	}))

	native[2].Content = "edited"
	require.ErrorContains(t, ValidateMessages(native, nativeInfo), "projection changed")
}

func TestValidateResponse(t *testing.T) {
	require.False(t, ResponseHasReplay(nil))
	require.NoError(t, ValidateResponse(nil, nativeInfo))

	legacy := &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("a")}}}
	require.False(t, ResponseHasReplay(legacy))
	require.NoError(t, ValidateResponse(legacy, model.Info{Name: "legacy"}))

	response := &model.Response{Choices: []model.Choice{
		{Message: model.NewAssistantMessage("legacy choice")},
		{Message: nativeMessage(t)},
	}}
	require.True(t, ResponseHasReplay(response))
	require.NoError(t, ValidateResponse(response, nativeInfo))
	require.ErrorContains(t, ValidateResponse(response, model.Info{Name: "legacy"}),
		"invalid model response")

	response.Choices[1].Message.ToolCalls = []model.ToolCall{{ID: "call_injected"}}
	require.ErrorContains(t, ValidateResponse(response, nativeInfo), "projection changed")

	delta := &model.Response{Choices: []model.Choice{{Delta: nativeMessage(t)}}}
	require.True(t, ResponseHasReplay(delta))
	require.NoError(t, ValidateResponse(delta, nativeInfo))
	delta.Choices[0].Delta.Content = "edited"
	require.Error(t, ValidateResponse(delta, nativeInfo))
}

func nativeToolResponse(t *testing.T) *model.Response {
	t.Helper()
	msg := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type:     "function",
			ID:       "call_1",
			Function: model.FunctionDefinitionParam{Name: "add", Arguments: []byte(`{"a":1}`)},
		}},
	}
	require.NoError(t, msg.SetReplayData(
		nativeInfo.ReplayProtocol,
		nativeInfo.Name,
		json.RawMessage(`[{"type":"function_call","call_id":"call_1"}]`),
	))
	return &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: msg}}}
}

func TestResponseGuard(t *testing.T) {
	legacy := &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("a")}}}
	guard := GuardResponse(legacy)
	require.False(t, guard.Active(legacy))
	replacement := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: "call_override"}},
	}}}}
	require.False(t, guard.Active(replacement))
	require.NoError(t, guard.Validate(replacement, model.Info{Name: "legacy"}),
		"legacy callbacks keep replacing responses")
	require.False(t, GuardResponse(nil).Active(nil))
	require.NoError(t, GuardResponse(nil).Validate(nil, nativeInfo))

	editArguments := func(response *model.Response) {
		response.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"a":2}`)
	}
	for _, tc := range []struct {
		name    string
		change  func(t *testing.T, response *model.Response) *model.Response
		wantErr string
	}{
		{
			name: "unchanged",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				return response
			},
		},
		{
			name: "wrapped in a new response",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				return &model.Response{Choices: append([]model.Choice(nil), response.Choices...)}
			},
		},
		{
			name: "edited with the record retained",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				editArguments(response)
				return response
			},
			wantErr: "projection changed",
		},
		{
			name: "edited with the record dropped",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				editArguments(response)
				response.Choices[0].Message.Replay = nil
				return response
			},
			wantErr: "native message record of choice 0 was removed",
		},
		{
			name: "replaced without a record",
			change: func(_ *testing.T, _ *model.Response) *model.Response {
				return &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("x")}}}
			},
			wantErr: "was removed",
		},
		{
			name: "replaced without choices",
			change: func(_ *testing.T, _ *model.Response) *model.Response {
				return &model.Response{}
			},
			wantErr: "was removed",
		},
		{
			name: "edited and rebound",
			change: func(t *testing.T, response *model.Response) *model.Response {
				msg := &response.Choices[0].Message
				payload, err := msg.ReplayDataFor(nativeInfo.ReplayProtocol, nativeInfo.Name)
				require.NoError(t, err)
				editArguments(response)
				require.NoError(t, msg.SetReplayData(nativeInfo.ReplayProtocol, nativeInfo.Name, payload))
				return response
			},
			wantErr: "was removed",
		},
		{
			name: "record replaced by in-place decode",
			change: func(t *testing.T, response *model.Response) *model.Response {
				other := nativeMessage(t)
				raw, err := json.Marshal(other.Replay)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(raw, response.Choices[0].Message.Replay))
				return response
			},
			wantErr: "was removed",
		},
		{
			name: "choice moved",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				response.Choices = append([]model.Choice{{Message: model.NewAssistantMessage("x")}},
					response.Choices...)
				return response
			},
			wantErr: "was removed",
		},
		{
			name: "choice appended",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				response.Choices = append(response.Choices, model.Choice{
					Index: 1, Message: model.NewAssistantMessage("unbound output"),
				})
				return response
			},
			wantErr: "native response choices were added or removed",
		},
		{
			name: "choice index changed",
			change: func(_ *testing.T, response *model.Response) *model.Response {
				response.Choices[0].Index = 1
				return response
			},
			wantErr: "was removed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := nativeToolResponse(t)
			guard := GuardResponse(response)
			require.True(t, guard.Active(response))
			final := tc.change(t, response)
			require.True(t, guard.Active(final), "a captured record keeps validation active")
			err := guard.Validate(final, nativeInfo)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}

	t.Run("delta record dropped", func(t *testing.T) {
		response := &model.Response{Choices: []model.Choice{{Delta: nativeMessage(t)}}}
		guard := GuardResponse(response)
		response.Choices[0].Delta.Replay = nil
		require.ErrorContains(t, guard.Validate(response, nativeInfo), "native delta record of choice 0")
	})

	t.Run("record added by a callback", func(t *testing.T) {
		response := &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("a")}}}
		guard := GuardResponse(response)
		response.Choices[0].Message = nativeMessage(t)
		require.True(t, guard.Active(response))
		require.ErrorContains(t, guard.Validate(response, model.Info{Name: "legacy"}),
			"invalid model response")
	})
}
