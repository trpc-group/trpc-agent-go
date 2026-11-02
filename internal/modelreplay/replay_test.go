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

var nativeInfo = model.Info{Name: "native-model", ProviderDataProtocol: "test.native"}

func nativeMessage(t *testing.T) model.Message {
	t.Helper()
	msg := model.NewAssistantMessage("answer")
	require.NoError(t, msg.SetProviderData(
		nativeInfo.ProviderDataProtocol,
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
			infoModel{info: model.Info{Name: "other", ProviderDataProtocol: nativeInfo.ProviderDataProtocol}},
		},
		"another protocol": {
			infoModel{info: nativeInfo},
			infoModel{info: model.Info{Name: nativeInfo.Name, ProviderDataProtocol: "other"}},
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
		Name:                 "other-model",
		ProviderDataProtocol: nativeInfo.ProviderDataProtocol,
	}))

	native[2].Content = "edited"
	require.NoError(t, ValidateMessages(native, nativeInfo))
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
	require.NoError(t, ValidateResponse(response, nativeInfo),
		"ToolCalls is the current execution projection")

	delta := &model.Response{Choices: []model.Choice{{Delta: nativeMessage(t)}}}
	require.True(t, ResponseHasReplay(delta))
	require.NoError(t, ValidateResponse(delta, nativeInfo))
	delta.Choices[0].Delta.Content = "edited"
	require.NoError(t, ValidateResponse(delta, nativeInfo))
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
	require.NoError(t, msg.SetProviderData(
		nativeInfo.ProviderDataProtocol,
		nativeInfo.Name,
		json.RawMessage(`[{"type":"function_call","call_id":"call_1"}]`),
	))
	return &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: msg}}}
}

func TestValidateResponseAllowsFinalResponseEditsAndReplacements(t *testing.T) {
	t.Run("edited tool calls and choices", func(t *testing.T) {
		response := nativeToolResponse(t)
		response.Choices[0].Message.ToolCalls[0].Function.Arguments = []byte(`{"a":2}`)
		response.Choices[0].Index = 1
		response.Choices = append(response.Choices, model.Choice{
			Index: 2, Message: model.NewAssistantMessage("additional choice"),
		})
		require.NoError(t, ValidateResponse(response, nativeInfo))
	})

	t.Run("record removed", func(t *testing.T) {
		response := nativeToolResponse(t)
		response.Choices[0].Message.ProviderData = nil
		require.NoError(t, ValidateResponse(response, nativeInfo))
	})

	t.Run("response replaced without a record", func(t *testing.T) {
		response := &model.Response{Choices: []model.Choice{{
			Message: model.NewAssistantMessage("replacement"),
		}}}
		require.NoError(t, ValidateResponse(response, nativeInfo))
	})

	t.Run("foreign producer remains invalid", func(t *testing.T) {
		msg := model.NewAssistantMessage("foreign")
		require.NoError(t, msg.SetProviderData("other.native", nativeInfo.Name,
			json.RawMessage(`[]`)))
		response := &model.Response{Choices: []model.Choice{{Message: msg}}}
		require.ErrorContains(t, ValidateResponse(response, nativeInfo), "invalid model response")
	})
}
