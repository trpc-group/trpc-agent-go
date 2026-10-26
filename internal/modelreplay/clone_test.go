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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCloneMessagePreservesNativeHistoryOwnership(t *testing.T) {
	text, index := "answer", 0
	message := model.Message{
		Role: model.RoleAssistant,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("image")}},
			{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: []byte("audio")}},
			{Type: model.ContentTypeVideo, Video: &model.Video{Data: []byte("video")}},
			{Type: model.ContentTypeFile, File: &model.File{Data: []byte("file")}},
			{ContentRef: &model.ContentRef{ArtifactName: "original"}},
		},
		ToolCalls: []model.ToolCall{{
			ID: "call", Index: &index,
			Function:    model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"a":1}`)},
			ExtraFields: map[string]any{"nested": map[string]any{"count": json.Number("9007199254740993")}},
		}},
	}
	const payload = `[{"type":"native","state":"opaque"}]`
	require.NoError(t, message.SetReplayData("test.native", "test-model", json.RawMessage(payload)))
	before, err := json.Marshal(message)
	require.NoError(t, err)
	cloned := CloneMessage(message)
	raw, err := cloned.ReplayDataFor("test.native", "test-model")
	require.NoError(t, err)
	require.JSONEq(t, payload, string(raw))

	*cloned.ContentParts[0].Text = "edited"
	cloned.ContentParts[1].Image.Data[0] = 'x'
	cloned.ContentParts[2].Audio.Data[0] = 'x'
	cloned.ContentParts[3].Video.Data[0] = 'x'
	cloned.ContentParts[4].File.Data[0] = 'x'
	cloned.ContentParts[5].ContentRef.ArtifactName = "edited"
	cloned.ToolCalls[0].Function.Arguments[5] = '9'
	*cloned.ToolCalls[0].Index = 7
	cloned.ToolCalls[0].ExtraFields["nested"].(map[string]any)["count"] = json.Number("1")
	require.NoError(t, json.Unmarshal([]byte(`{"version":1,"provider":"other.native","model":"other-model","payload":[],"projection_sha256":"`+
		"0000000000000000000000000000000000000000000000000000000000000000"+`"}`), cloned.Replay))

	after, err := json.Marshal(message)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "request edits must not rewrite the stored native record")
	raw, err = message.ReplayDataFor("test.native", "test-model")
	require.NoError(t, err)
	require.JSONEq(t, payload, string(raw))
}

func TestToolCallsForExecutionPreservesNativeArguments(t *testing.T) {
	message := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
		ID: "call", Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"a":1}`)},
	}}}
	require.NoError(t, message.SetReplayData("test.native", "test-model", json.RawMessage(`[]`)))
	execution := ToolCallsForExecution(message)
	execution[0].Function.Arguments[5] = '9'
	require.Equal(t, `{"a":9}`, string(execution[0].Function.Arguments))
	require.Equal(t, `{"a":1}`, string(message.ToolCalls[0].Function.Arguments))
	_, err := message.ReplayDataFor("test.native", "test-model")
	require.NoError(t, err)
}
