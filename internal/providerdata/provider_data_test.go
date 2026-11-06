//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package providerdata

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
	msg.ProviderData = &model.ProviderData{
		Protocol: nativeInfo.ProviderDataProtocol,
		Model:    nativeInfo.Name,
		Payload:  json.RawMessage(`[{"type":"reasoning","id":"rs_1"}]`),
	}
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
	require.False(t, MessagesHaveData(legacy))
	require.NoError(t, ValidateMessages(legacy, model.Info{Name: "legacy"}),
		"legacy histories never fail validation")

	native := append(append([]model.Message(nil), legacy...), nativeMessage(t))
	require.True(t, MessagesHaveData(native))
	require.NoError(t, ValidateMessages(native, nativeInfo))
	require.ErrorContains(t, ValidateMessages(native, model.Info{Name: nativeInfo.Name}),
		"provider data message 2")
	require.Error(t, ValidateMessages(native, model.Info{
		Name:                 "other-model",
		ProviderDataProtocol: nativeInfo.ProviderDataProtocol,
	}))

	native[2].Content = "edited"
	require.NoError(t, ValidateMessages(native, nativeInfo))
	native[2].Role = model.RoleUser
	require.ErrorContains(t, ValidateMessages(native, nativeInfo), "assistant message")
	native[2].Role = model.RoleAssistant
	native[2].ProviderData.Payload = json.RawMessage(`{`)
	require.ErrorContains(t, ValidateMessages(native, nativeInfo), "valid JSON object or array")
}

func TestValidateResponse(t *testing.T) {
	require.False(t, ResponseHasData(nil))
	require.NoError(t, ValidateResponse(nil, nativeInfo))

	legacy := &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("a")}}}
	require.False(t, ResponseHasData(legacy))
	require.NoError(t, ValidateResponse(legacy, model.Info{Name: "legacy"}))

	response := &model.Response{Choices: []model.Choice{
		{Message: model.NewAssistantMessage("legacy choice")},
		{Message: nativeMessage(t)},
	}}
	require.True(t, ResponseHasData(response))
	require.NoError(t, ValidateResponse(response, nativeInfo))
	require.ErrorContains(t, ValidateResponse(response, model.Info{Name: "legacy"}),
		"invalid model response")

	response.Choices[1].Message.ToolCalls = []model.ToolCall{{ID: "call_injected"}}
	require.NoError(t, ValidateResponse(response, nativeInfo),
		"ToolCalls is the current execution projection")

	delta := &model.Response{Choices: []model.Choice{{Delta: nativeMessage(t)}}}
	require.True(t, ResponseHasData(delta))
	require.NoError(t, ValidateResponse(delta, nativeInfo))
	delta.Choices[0].Delta.Content = "edited"
	require.NoError(t, ValidateResponse(delta, nativeInfo))
}
