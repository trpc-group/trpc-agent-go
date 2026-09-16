//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package statecopy

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestMessagesAreIsolated(t *testing.T) {
	require.Nil(t, Messages(nil))
	zero := Message(model.Message{})
	require.Nil(t, zero.ContentParts)
	require.Nil(t, zero.ToolCalls)

	text := "text"
	index := 1
	original := []model.Message{{
		ContentParts: []model.ContentPart{
			{Text: &text},
			{
				Image: &model.Image{Data: []byte("image")},
				ContentRef: &model.ContentRef{
					ArtifactName: "original",
				},
			},
			{Audio: &model.Audio{Data: []byte("audio")}},
			{Video: &model.Video{Data: []byte("video")}},
			{File: &model.File{Data: []byte("file")}},
		},
		ToolCalls: []model.ToolCall{{
			Index: &index,
			Function: model.FunctionDefinitionParam{
				Arguments: []byte("arguments"),
			},
			ExtraFields: map[string]any{
				"nested": map[string]any{"value": "original"},
			},
		}},
	}}

	cloned := Messages(original)
	*cloned[0].ContentParts[0].Text = "mutated"
	cloned[0].ContentParts[1].Image.Data[0] = 'X'
	cloned[0].ContentParts[1].ContentRef.ArtifactName = "mutated"
	cloned[0].ContentParts[2].Audio.Data[0] = 'X'
	cloned[0].ContentParts[3].Video.Data[0] = 'X'
	cloned[0].ContentParts[4].File.Data[0] = 'X'
	cloned[0].ToolCalls[0].Function.Arguments[0] = 'X'
	*cloned[0].ToolCalls[0].Index = 2
	cloned[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["value"] =
		"mutated"

	require.Equal(t, "text", *original[0].ContentParts[0].Text)
	require.Equal(t, "image", string(original[0].ContentParts[1].Image.Data))
	require.Equal(t, "original",
		original[0].ContentParts[1].ContentRef.ArtifactName,
	)
	require.Equal(t, "audio", string(original[0].ContentParts[2].Audio.Data))
	require.Equal(t, "video", string(original[0].ContentParts[3].Video.Data))
	require.Equal(t, "file", string(original[0].ContentParts[4].File.Data))
	require.Equal(t, "arguments", string(
		original[0].ToolCalls[0].Function.Arguments,
	))
	require.Equal(t, 1, *original[0].ToolCalls[0].Index)
	require.Equal(t, "original",
		original[0].ToolCalls[0].ExtraFields["nested"].(map[string]any)["value"],
	)
}
