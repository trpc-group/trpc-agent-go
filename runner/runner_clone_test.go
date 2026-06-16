//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCloneChoicesDeepCopiesMutableFields(t *testing.T) {
	reason := "stop"
	index := 1
	text := "hello"
	choices := []model.Choice{
		{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "assistant content",
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &text},
					{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("image"), Format: "png"}},
					{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: []byte("audio"), Format: "wav"}},
					{Type: model.ContentTypeFile, File: &model.File{Name: "file.txt", Data: []byte("file")}},
				},
				ToolCalls: []model.ToolCall{
					{
						Type:  "function",
						ID:    "call-1",
						Index: &index,
						Function: model.FunctionDefinitionParam{
							Name:      "tool",
							Arguments: []byte(`{"value":"original"}`),
						},
						ExtraFields: map[string]any{
							"nested":     map[string]any{"key": "value"},
							"list":       []any{"first"},
							"typed_map":  map[string][]string{"key": {"value"}},
							"typed_list": []string{"first"},
						},
					},
				},
			},
			FinishReason: &reason,
			Logprobs: &model.Logprobs{
				Content: []model.TokenLogprob{
					{
						Token:   "tok",
						Logprob: -1,
						Bytes:   []int{1, 2},
						TopLogprobs: []model.TopLogprob{
							{Token: "alt", Logprob: -2, Bytes: []int{3, 4}},
						},
					},
				},
			},
		},
	}

	cloned := cloneChoices(choices)
	require.Len(t, cloned, 1)

	originalJSON, err := json.Marshal(choices)
	require.NoError(t, err)
	clonedJSON, err := json.Marshal(cloned)
	require.NoError(t, err)
	require.JSONEq(t, string(originalJSON), string(clonedJSON))

	*choices[0].FinishReason = "length"
	*choices[0].Message.ContentParts[0].Text = "changed"
	choices[0].Message.ContentParts[1].Image.Data[0] = 'X'
	choices[0].Message.ContentParts[2].Audio.Data[0] = 'X'
	choices[0].Message.ContentParts[3].File.Data[0] = 'X'
	*choices[0].Message.ToolCalls[0].Index = 9
	choices[0].Message.ToolCalls[0].Function.Arguments[0] = '['
	choices[0].Message.ToolCalls[0].ExtraFields["nested"].(map[string]any)["key"] = "changed"
	choices[0].Message.ToolCalls[0].ExtraFields["list"].([]any)[0] = "changed"
	choices[0].Message.ToolCalls[0].ExtraFields["typed_map"].(map[string][]string)["key"][0] = "changed"
	choices[0].Message.ToolCalls[0].ExtraFields["typed_list"].([]string)[0] = "changed"
	choices[0].Logprobs.Content[0].Bytes[0] = 9
	choices[0].Logprobs.Content[0].TopLogprobs[0].Bytes[0] = 9

	require.Equal(t, "stop", *cloned[0].FinishReason)
	require.Equal(t, "hello", *cloned[0].Message.ContentParts[0].Text)
	require.Equal(t, []byte("image"), cloned[0].Message.ContentParts[1].Image.Data)
	require.Equal(t, []byte("audio"), cloned[0].Message.ContentParts[2].Audio.Data)
	require.Equal(t, []byte("file"), cloned[0].Message.ContentParts[3].File.Data)
	require.Equal(t, 1, *cloned[0].Message.ToolCalls[0].Index)
	require.Equal(t, []byte(`{"value":"original"}`), cloned[0].Message.ToolCalls[0].Function.Arguments)
	require.Equal(t, "value", cloned[0].Message.ToolCalls[0].ExtraFields["nested"].(map[string]any)["key"])
	require.Equal(t, "first", cloned[0].Message.ToolCalls[0].ExtraFields["list"].([]any)[0])
	require.Equal(t, "value", cloned[0].Message.ToolCalls[0].ExtraFields["typed_map"].(map[string][]string)["key"][0])
	require.Equal(t, "first", cloned[0].Message.ToolCalls[0].ExtraFields["typed_list"].([]string)[0])
	require.Equal(t, []int{1, 2}, cloned[0].Logprobs.Content[0].Bytes)
	require.Equal(t, []int{3, 4}, cloned[0].Logprobs.Content[0].TopLogprobs[0].Bytes)
}

func TestCloneChoicesHandlesEmptyValues(t *testing.T) {
	require.Nil(t, cloneChoices(nil))
	require.Empty(t, cloneChoices([]model.Choice{}))

	choices := []model.Choice{
		{
			Index: 1,
			Message: model.Message{
				Role:         model.RoleAssistant,
				ContentParts: []model.ContentPart{},
			},
		},
	}
	cloned := cloneChoices(choices)
	require.Len(t, cloned, 1)
	require.Nil(t, cloned[0].Logprobs)
	require.Empty(t, cloned[0].Message.ContentParts)

	originalJSON, err := json.Marshal(choices)
	require.NoError(t, err)
	clonedJSON, err := json.Marshal(cloned)
	require.NoError(t, err)
	require.JSONEq(t, string(originalJSON), string(clonedJSON))
}

func TestCloneAnyValueCopiesJSONContainers(t *testing.T) {
	raw := json.RawMessage(`{"ok":true}`)
	clonedRaw := cloneAnyValue(raw).(json.RawMessage)
	raw[0] = '['
	require.Equal(t, json.RawMessage(`{"ok":true}`), clonedRaw)

	bytes := []byte("bytes")
	clonedBytes := cloneAnyValue(bytes).([]byte)
	bytes[0] = 'X'
	require.Equal(t, []byte("bytes"), clonedBytes)

	values := map[string]any{
		"list": []any{json.RawMessage(`{"nested":true}`)},
	}
	clonedMap := cloneAnyValue(values).(map[string]any)
	values["list"].([]any)[0].(json.RawMessage)[0] = '['
	require.Equal(t, json.RawMessage(`{"nested":true}`), clonedMap["list"].([]any)[0])

	require.Nil(t, cloneAnyValue(nil))
	require.Nil(t, cloneAnyMap(nil))
	require.Nil(t, cloneAnySlice(nil))
}

func TestCloneAnyValueCopiesTypedContainers(t *testing.T) {
	typedMap := map[string][]string{"key": {"value"}}
	clonedMap := cloneAnyValue(typedMap).(map[string][]string)
	typedMap["key"][0] = "changed"
	require.Equal(t, "value", clonedMap["key"][0])

	typedSlice := [][]string{{"value"}}
	clonedSlice := cloneAnyValue(typedSlice).([][]string)
	typedSlice[0][0] = "changed"
	require.Equal(t, "value", clonedSlice[0][0])

	typedArray := [1][]string{{"value"}}
	clonedArray := cloneAnyValue(typedArray).([1][]string)
	typedArray[0][0] = "changed"
	require.Equal(t, "value", clonedArray[0][0])

	var nilMap map[string][]string
	require.Nil(t, cloneAnyValue(nilMap).(map[string][]string))

	var nilSlice [][]string
	require.Nil(t, cloneAnyValue(nilSlice).([][]string))

	require.Equal(t, "primitive", cloneAnyValue("primitive"))
}
