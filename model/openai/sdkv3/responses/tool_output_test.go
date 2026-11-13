//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func imageToolCall() model.Message {
	return model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID: "call_image",
			Function: model.FunctionDefinitionParam{
				Name: "read_image", Arguments: []byte("{}"),
			},
		}},
	}
}

func TestToolOutputImagesReachHTTP(t *testing.T) {
	before, after := "before", "after"
	for _, tc := range []struct {
		name    string
		content string
		parts   []model.ContentPart
		want    string
	}{
		{
			name: "image only URL",
			parts: []model.ContentPart{{
				Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"},
			}},
			want: `[{"type":"input_image","image_url":"https://example.test/image.png","detail":"auto"}]`,
		},
		{
			name: "raw data with default format",
			parts: []model.ContentPart{{
				Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("png")},
			}},
			want: `[{"type":"input_image","image_url":"data:image/png;base64,cG5n","detail":"auto"}]`,
		},
		{
			name: "data URL is not encoded again",
			parts: []model.ContentPart{{
				Type: model.ContentTypeImage,
				Image: &model.Image{
					URL: "data:image/png;base64,cG5n", Data: []byte("unused"), Detail: " LOW ",
				},
			}},
			want: `[{"type":"input_image","image_url":"data:image/png;base64,cG5n","detail":"low"}]`,
		},
		{
			name:    "mixed ordered text and images",
			content: "prefix",
			parts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &before},
				{Type: model.ContentTypeText},
				{},
				{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("jpg"), Format: "jpeg", Detail: "HIGH"}},
				{Type: model.ContentTypeText, Text: &after},
				{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("webp"), Format: "image/webp", Detail: "original"}},
			},
			want: `[
				{"type":"input_text","text":"prefix"},
				{"type":"input_text","text":"before"},
				{"type":"input_image","image_url":"data:image/jpeg;base64,anBn","detail":"high"},
				{"type":"input_text","text":"after"},
				{"type":"input_image","image_url":"data:image/webp;base64,d2VicA==","detail":"original"}
			]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := model.NewToolMessage("call_image", "read_image", tc.content)
			msg.ContentParts = tc.parts
			req := &model.Request{Messages: []model.Message{imageToolCall(), msg}}
			original, err := json.Marshal(req.Messages)
			require.NoError(t, err)

			got := captureRequest(t, req)
			items := got.body["input"].([]any)
			require.Len(t, items, 2, "images belong to the function output, without an extra user message")
			requireJSONField(t, `{"type":"function_call_output","call_id":"call_image","output":`+tc.want+`}`, items[1])

			current, err := json.Marshal(req.Messages)
			require.NoError(t, err)
			require.Equal(t, string(original), string(current), "conversion must not mutate caller-owned messages")
		})
	}
}

func TestToolOutputTextWireCompatibility(t *testing.T) {
	part, empty := "tail", ""
	for _, tc := range []struct {
		name    string
		content string
		parts   []model.ContentPart
		want    string
	}{
		{name: "empty", want: ""},
		{name: "plain text", content: "done", want: "done"},
		{name: "JSON object", content: `{"data":"cG5n"}`, want: `{"data":"cG5n"}`},
		{
			name:    "provider-shaped JSON stays text",
			content: `[{"type":"input_image","image_url":"data:image/png;base64,cG5n"}]`,
			want:    `[{"type":"input_image","image_url":"data:image/png;base64,cG5n"}]`,
		},
		{
			name:    "text parts concatenate",
			content: "head",
			parts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &empty},
				{Type: model.ContentTypeText},
				{},
				{Type: model.ContentTypeText, Text: &part},
			},
			want: "headtail",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := model.NewToolMessage("call_image", "read_image", tc.content)
			msg.ContentParts = tc.parts
			got := captureRequest(t, &model.Request{Messages: []model.Message{imageToolCall(), msg}})
			items := got.body["input"].([]any)
			require.Len(t, items, 2)
			require.Equal(t, map[string]any{
				"type": "function_call_output", "call_id": "call_image", "output": tc.want,
			}, items[1])
		})
	}
}

func TestInvalidImageToolOutputFailsBeforeHTTP(t *testing.T) {
	srv, calls := rejectingServer(t)
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	image := model.ContentPart{
		Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"},
	}
	for _, tc := range []struct {
		name  string
		parts []model.ContentPart
		want  string
	}{
		{name: "nil image", parts: []model.ContentPart{{Type: model.ContentTypeImage}}, want: "image content part is empty"},
		{
			name:  "empty image",
			parts: []model.ContentPart{{Type: model.ContentTypeImage, Image: &model.Image{URL: " ", Data: []byte{}}}},
			want:  "image content part has no url or data",
		},
		{name: "file", parts: []model.ContentPart{image, {Type: model.ContentTypeFile}}, want: "got file"},
		{name: "audio", parts: []model.ContentPart{image, {Type: model.ContentTypeAudio}}, want: "got audio"},
		{name: "video", parts: []model.ContentPart{image, {Type: model.ContentTypeVideo}}, want: "got video"},
		{name: "unknown before image", parts: []model.ContentPart{{Type: "hologram"}, image}, want: "got hologram"},
		{
			name:  "legacy unsupported type",
			parts: []model.ContentPart{{Type: model.ContentTypeFile}},
			want:  "tool messages only support text content, got file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := model.NewToolMessage("call_image", "read_image", "description")
			msg.ContentParts = tc.parts
			ch, err := m.GenerateContent(context.Background(), &model.Request{
				Messages: []model.Message{imageToolCall(), msg},
			})
			require.ErrorContains(t, err, tc.want)
			require.ErrorContains(t, err, `tool result "call_image"`)
			require.Nil(t, ch)
		})
	}
	require.Zero(t, calls.Load(), "invalid output must fail before any provider request")
}

func TestImageToolOutputRetainsCallValidation(t *testing.T) {
	srv, calls := rejectingServer(t)
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	result := func(id string) model.Message {
		msg := model.NewToolMessage(id, "read_image", "")
		msg.AddImageURL("https://example.test/image.png", "auto")
		return msg
	}
	for _, tc := range []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{name: "empty ID", messages: []model.Message{imageToolCall(), result("")}, want: "empty call_id"},
		{name: "orphan", messages: []model.Message{result("call_image")}, want: "no preceding function call"},
		{name: "wrong ID", messages: []model.Message{imageToolCall(), result("other")}, want: "no preceding function call"},
		{name: "duplicate", messages: []model.Message{imageToolCall(), result("call_image"), result("call_image")}, want: "duplicate tool result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: tc.messages})
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, ch)
		})
	}
	require.Zero(t, calls.Load())
}
