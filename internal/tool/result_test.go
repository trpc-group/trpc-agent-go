//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestResultMessageTextOnly(t *testing.T) {
	first := "first"
	last := "last"
	result := model.ToolResultContent{
		{Type: model.ContentTypeText, Text: &first},
		{Type: model.ContentTypeText},
		{Type: model.ContentTypeText, Text: &last},
	}

	message, ok := ResultMessage(result)

	require.True(t, ok)
	require.Equal(t, model.RoleTool, message.Role)
	require.Equal(t, "firstlast", message.Content)
	require.Nil(t, message.ContentParts)
	require.Equal(t, "first", first)
	require.Equal(t, "last", last)
}

func TestResultMessageMixedContentKeepsOrderedPartsAndCopies(t *testing.T) {
	text := "description"
	result := model.ToolResultContent{
		{Type: model.ContentTypeText, Text: &text},
		{
			Type:  model.ContentTypeImage,
			Image: &model.Image{Data: []byte("image")},
		},
		{
			Type:  model.ContentTypeAudio,
			Audio: &model.Audio{Data: []byte("audio")},
		},
		{
			Type:  model.ContentTypeVideo,
			Video: &model.Video{Data: []byte("video")},
		},
		{
			Type: model.ContentTypeFile,
			File: &model.File{Data: []byte("file")},
		},
		{
			Type:       model.ContentType("future"),
			ContentRef: &model.ContentRef{ArtifactName: "artifact"},
		},
		{
			Type:       model.ContentTypeText,
			Text:       &text,
			ContentRef: &model.ContentRef{ArtifactName: "text-artifact"},
		},
	}

	message, ok := ResultMessage(result)

	require.True(t, ok)
	require.Equal(t, model.RoleTool, message.Role)
	require.Empty(t, message.Content)
	require.Equal(t, len(result), len(message.ContentParts))
	for i := range result {
		require.Equal(t, result[i].Type, message.ContentParts[i].Type)
	}
	require.Equal(t, "description", *message.ContentParts[0].Text)
	require.Equal(t, "image", string(message.ContentParts[1].Image.Data))
	require.Equal(t, "audio", string(message.ContentParts[2].Audio.Data))
	require.Equal(t, "video", string(message.ContentParts[3].Video.Data))
	require.Equal(t, "file", string(message.ContentParts[4].File.Data))
	require.Equal(t, "artifact", message.ContentParts[5].ContentRef.ArtifactName)
	require.Equal(t, "text-artifact", message.ContentParts[6].ContentRef.ArtifactName)

	*message.ContentParts[0].Text = "changed"
	message.ContentParts[1].Image.Data[0] = 'X'
	message.ContentParts[2].Audio.Data[0] = 'X'
	message.ContentParts[3].Video.Data[0] = 'X'
	message.ContentParts[4].File.Data[0] = 'X'
	message.ContentParts[5].ContentRef.ArtifactName = "changed"
	message.ContentParts[6].ContentRef.ArtifactName = "changed"

	require.Equal(t, "description", *result[0].Text)
	require.Equal(t, "image", string(result[1].Image.Data))
	require.Equal(t, "audio", string(result[2].Audio.Data))
	require.Equal(t, "video", string(result[3].Video.Data))
	require.Equal(t, "file", string(result[4].File.Data))
	require.Equal(t, "artifact", result[5].ContentRef.ArtifactName)
	require.Equal(t, "text-artifact", result[6].ContentRef.ArtifactName)
}

func TestResultMessageNilAndEmptyContent(t *testing.T) {
	empty := ""
	for name, result := range map[string]model.ToolResultContent{
		"nil":          nil,
		"empty":        {},
		"empty text":   {{Type: model.ContentTypeText, Text: &empty}},
		"missing text": {{Type: model.ContentTypeText}},
	} {
		t.Run(name, func(t *testing.T) {
			message, ok := ResultMessage(result)

			require.True(t, ok)
			require.Equal(t, model.RoleTool, message.Role)
			require.Empty(t, message.Content)
			require.Equal(t, []model.ContentPart{{Type: model.ContentTypeText, Text: &empty}}, message.ContentParts)
			require.True(t, model.HasPayload(message))
		})
	}
}

func TestResultMessageOnlyAcceptsExactValueType(t *testing.T) {
	content := model.ToolResultContent{{Type: model.ContentTypeText}}
	for name, result := range map[string]any{
		"raw content parts": []model.ContentPart(content),
		"pointer":           &content,
		"map":               map[string]any{"content": "value"},
		"nil":               nil,
	} {
		t.Run(name, func(t *testing.T) {
			message, ok := ResultMessage(result)

			require.False(t, ok)
			require.Equal(t, model.Message{}, message)
		})
	}
}
