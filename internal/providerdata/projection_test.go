//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package providerdata

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type projectingModel struct{ infoModel }

func (m projectingModel) ProjectProviderData(data *model.ProviderData) (model.Message, error) {
	var projected model.Message
	err := json.Unmarshal(data.Payload, &projected)
	return projected, err
}

type requestProjectingModel struct{ projectingModel }

func (m requestProjectingModel) ProjectProviderDataWithRequest(request *model.Request, data *model.ProviderData) (model.Message, error) {
	result, err := m.ProjectProviderData(data)
	if err == nil {
		result.Content = request.Messages[0].Content + ":" + result.Content
	}
	return result, err
}

func TestReprojectResponseUsesRequestAwareProjection(t *testing.T) {
	producer := requestProjectingModel{projectingModel{infoModel{info: nativeInfo}}}
	request := &model.Request{Messages: []model.Message{model.NewUserMessage("request-binding")}}
	response := &model.Response{Done: true, Choices: []model.Choice{{Message: nativeMessage(t)}}}
	before := SnapshotResponse(response)
	response.Choices[0].Message.ProviderData.Payload = json.RawMessage(`{"content":"edited"}`)
	require.NoError(t, ReprojectResponse(before, response, producer, request))
	require.Equal(t, "request-binding:edited", response.Choices[0].Message.Content)
	legacy := producer.projectingModel
	candidates := []model.Model{legacy, producer}
	data := response.Choices[0].Message.ProviderData
	projected, err := ProjectFromCandidatesWithRequest(candidates, request, data)
	require.NoError(t, err)
	require.Equal(t, "request-binding:edited", projected.Content)
	projected, err = ProjectFromCandidates(candidates, data)
	require.NoError(t, err)
	require.Equal(t, "edited", projected.Content, "request-free callers retain their existing behavior")
	projected, err = ProjectFromCandidatesWithRequest([]model.Model{legacy}, request, data)
	require.NoError(t, err)
	require.Equal(t, "edited", projected.Content, "legacy-only candidates still project with a request")
}

func TestReprojectResponseUsesEditedPayload(t *testing.T) {
	producer := projectingModel{infoModel{info: nativeInfo}}
	message := nativeMessage(t)
	message.ProviderData.Payload = json.RawMessage(`{"content":"1"}`)
	message.ToolCalls = []model.ToolCall{{ID: "old"}}
	response := &model.Response{Done: true, Choices: []model.Choice{{Message: message}}}
	before := SnapshotResponse(response)
	// Edit bytes in place so a shared slice cannot hide the change.
	index := bytes.Index(message.ProviderData.Payload, []byte(`"1"`))
	message.ProviderData.Payload[index+1] = '9'
	require.NoError(t, ReprojectResponse(before, response, producer, nil))
	require.Equal(t, "9", response.Choices[0].Message.Content)
	require.Empty(t, response.Choices[0].Message.ToolCalls)
	require.Equal(t, "stop", *response.Choices[0].FinishReason)
	require.Same(t, message.ProviderData, response.Choices[0].Message.ProviderData)

	// A response can contain both a provider action and an ordinary tool call.
	response.Done = false
	before = SnapshotResponse(response)
	response.Choices[0].Message.ProviderData.Payload = json.RawMessage(
		`{"content":"calling","reasoning_content":"updated","tool_calls":[{"id":"new","type":"function","function":{"name":"f","arguments":"{\"a\":9}"}}]}`)
	require.NoError(t, ReprojectResponse(before, response, producer, nil))
	require.Equal(t, "calling", response.Choices[0].Message.Content)
	require.Equal(t, "updated", response.Choices[0].Message.ReasoningContent)
	require.Equal(t, "new", response.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, "tool_calls", *response.Choices[0].FinishReason)

	before = SnapshotResponse(response)
	response.Choices[0].Message.ProviderData.Payload = json.RawMessage(`{"content":"search pending"}`)
	require.NoError(t, ReprojectResponse(before, response, producer, nil))
	require.Empty(t, response.Choices[0].Message.ToolCalls)
	require.Nil(t, response.Choices[0].FinishReason, "the provider action is still pending")
}

func TestReprojectResponsePreservesProjectionOnlyEdits(t *testing.T) {
	response := &model.Response{Choices: []model.Choice{{Message: nativeMessage(t)}}}
	before := SnapshotResponse(response)
	response.Choices[0].Message.Content = "display edit"
	response.Choices[0].Message.ToolCalls = []model.ToolCall{{ID: "execution edit"}}
	// An unchanged native record does not require the optional capability.
	require.NoError(t, ReprojectResponse(before, response, infoModel{info: nativeInfo}, nil))
	require.Equal(t, "display edit", response.Choices[0].Message.Content)
	require.Equal(t, "execution edit", response.Choices[0].Message.ToolCalls[0].ID)

	legacy := &model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("chat")}}}
	require.Empty(t, SnapshotResponse(legacy).choices)
	require.NoError(t, ReprojectResponse(ResponseSnapshot{}, legacy, nil, nil))
	require.Equal(t, "chat", legacy.Choices[0].Message.Content)
}

func TestReprojectResponseValidatesBeforeApplyingAnyEdits(t *testing.T) {
	producer := projectingModel{infoModel{info: nativeInfo}}
	response := &model.Response{Choices: []model.Choice{{Message: nativeMessage(t)}, {Message: nativeMessage(t)}}}
	before := SnapshotResponse(response)
	response.Choices[0].Message.ProviderData.Payload = json.RawMessage(`{"content":"new"}`)
	response.Choices[1].Message.ProviderData.Payload = json.RawMessage(`{"tool_calls":"invalid"}`)
	require.Error(t, ReprojectResponse(before, response, producer, nil))
	require.Equal(t, "answer", response.Choices[0].Message.Content)
	require.ErrorContains(t, ReprojectResponse(before, response, infoModel{info: nativeInfo}, nil),
		"ProviderDataProjector")
}
