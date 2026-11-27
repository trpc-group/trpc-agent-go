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
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestGenerateJSONSchemaToolResultContent(t *testing.T) {
	schema := GenerateJSONSchema(toolResultContentType)
	require.Empty(t, schema.Type)
	require.Len(t, schema.AnyOf, 2)
	arraySchema := schema.AnyOf[0]
	require.NotNil(t, arraySchema)
	require.Equal(t, "array", arraySchema.Type)
	require.Empty(t, arraySchema.AnyOf)
	nullJSON, err := json.Marshal(schema.AnyOf[1])
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"null"}`, string(nullJSON))

	partSchema := arraySchema.Items
	require.NotNil(t, partSchema)
	require.Equal(t, "object", partSchema.Type)
	require.Equal(t, []string{"type"}, partSchema.Required)
	for _, property := range []string{"type", "text"} {
		propertyJSON, err := json.Marshal(partSchema.Properties[property])
		require.NoError(t, err)
		require.JSONEq(t, `{"type":"string"}`, string(propertyJSON))
	}
	for _, media := range []string{"image", "audio", "video", "file"} {
		t.Run(media, func(t *testing.T) {
			mediaSchema := partSchema.Properties[media]
			require.NotNil(t, mediaSchema)
			require.Equal(t, "object", mediaSchema.Type)
			require.Contains(t, mediaSchema.Required, "data")
			dataJSON, err := json.Marshal(mediaSchema.Properties["data"])
			require.NoError(t, err)
			require.JSONEq(t, `{
				"anyOf":[{"type":"string"},{"type":"null"}]
			}`, string(dataJSON))
		})
	}
	imageURLJSON, err := json.Marshal(
		partSchema.Properties["image"].Properties["url"],
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"string"}`, string(imageURLJSON))
}

func TestToolResultContentJSON(t *testing.T) {
	text := "description"
	cases := []struct {
		name   string
		result model.ToolResultContent
		want   string
	}{
		{
			name: "text",
			result: model.ToolResultContent{{
				Type: model.ContentTypeText,
				Text: &text,
			}},
			want: `[{"type":"text","text":"description"}]`,
		},
		{
			name: "raw image bytes",
			result: model.ToolResultContent{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{Data: []byte{0, 1, 255}, Format: "png"},
			}},
			want: `[{
				"type":"image",
				"image":{"url":"","data":"AAH/","format":"png"}
			}]`,
		},
		{
			name: "image url and nil bytes",
			result: model.ToolResultContent{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.test/image.png"},
			}},
			want: `[{
				"type":"image",
				"image":{"url":"https://example.test/image.png","data":null}
			}]`,
		},
		{
			name: "empty image bytes",
			result: model.ToolResultContent{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{Data: []byte{}},
			}},
			want: `[{"type":"image","image":{"url":"","data":""}}]`,
		},
		{
			name: "mixed content order",
			result: model.ToolResultContent{
				{Type: model.ContentTypeText, Text: &text},
				{
					Type:  model.ContentTypeImage,
					Image: &model.Image{Data: []byte{0, 1, 255}},
				},
				{Type: model.ContentTypeText, Text: &text},
			},
			want: `[
				{"type":"text","text":"description"},
				{"type":"image","image":{"url":"","data":"AAH/"}},
				{"type":"text","text":"description"}
			]`,
		},
		{
			name: "other media bytes",
			result: model.ToolResultContent{
				{
					Type:  model.ContentTypeAudio,
					Audio: &model.Audio{Data: []byte{0, 1, 255}, Format: "wav"},
				},
				{
					Type:  model.ContentTypeVideo,
					Video: &model.Video{Data: []byte{0, 1, 255}, Format: "mp4"},
				},
				{
					Type: model.ContentTypeFile,
					File: &model.File{Name: "sample.bin", Data: []byte{0, 1, 255}},
				},
			},
			want: `[
				{"type":"audio","audio":{"data":"AAH/","format":"wav"}},
				{"type":"video","video":{"data":"AAH/","format":"mp4"}},
				{"type":"file","file":{
					"filename":"sample.bin","data":"AAH/","file_id":""
				}}
			]`,
		},
		{
			name: "other media nil bytes",
			result: model.ToolResultContent{
				{Type: model.ContentTypeAudio, Audio: &model.Audio{}},
				{Type: model.ContentTypeVideo, Video: &model.Video{}},
				{Type: model.ContentTypeFile, File: &model.File{}},
			},
			want: `[
				{"type":"audio","audio":{"data":null,"format":""}},
				{"type":"video","video":{"data":null}},
				{"type":"file","file":{"filename":"","data":null,"file_id":""}}
			]`,
		},
		{
			name:   "empty content marker",
			result: model.ToolResultContent{{Type: model.ContentTypeText}},
			want:   `[{"type":"text"}]`,
		},
		{name: "empty slice", result: model.ToolResultContent{}, want: `[]`},
		{name: "nil slice", result: nil, want: `null`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.result)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(data))
		})
	}
}

func TestToolResultContentSchemaAdjustmentsAreScoped(t *testing.T) {
	ordinaryBytes := GenerateJSONSchema(byteSliceType)
	require.Equal(t, "array", ordinaryBytes.Type)
	require.Equal(t, "integer", ordinaryBytes.Items.Type)
	require.Empty(t, ordinaryBytes.AnyOf)

	ordinaryParts := GenerateJSONSchema(
		reflect.TypeOf([]model.ContentPart(nil)),
	)
	require.Equal(t, "array", ordinaryParts.Type)
	require.Empty(t, ordinaryParts.AnyOf)
	imageSchema := ordinaryParts.Items.Properties["image"]
	require.NotNil(t, imageSchema)
	ordinaryImageBytes := imageSchema.Properties["data"]
	require.Equal(t, "array", ordinaryImageBytes.Type)
	require.Equal(t, "integer", ordinaryImageBytes.Items.Type)
	require.Empty(t, ordinaryImageBytes.AnyOf)
}
