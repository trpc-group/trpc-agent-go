//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestMCPImageResultMessages_ReturnsImages(t *testing.T) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)

	defaultMsg := model.Message{
		Role:     model.RoleTool,
		ToolID:   "tool-call-1",
		ToolName: "browser_page_screenshot",
		Content:  "contains raw screenshot",
	}

	result := map[string]any{
		"action":         "screenshot",
		"screenshotPath": "/tmp/screenshot.png",
		"content": []mcpContentItem{
			{Type: "text", Data: "visible text"},
			{
				Type:     "image",
				Data:     encoded,
				MimeType: "image/png",
			},
		},
	}
	in := &tool.ToolResultMessagesInput{
		ToolName:           "browser_page_screenshot",
		ToolCallID:         "tool-call-1",
		DefaultToolMessage: defaultMsg,
		Arguments:          []byte(`{}`),
		Result:             result,
		Declaration:        nil,
	}

	got, err := mcpImageResultMessages(context.Background(), in)
	require.NoError(t, err)

	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 2)

	require.Equal(t, defaultMsg.Role, msgs[0].Role)
	require.Equal(t, defaultMsg.ToolID, msgs[0].ToolID)
	require.Equal(t, defaultMsg.ToolName, msgs[0].ToolName)
	require.NotContains(t, msgs[0].Content, encoded)
	require.Contains(t, msgs[0].Content, mcpImageDataOmitted)
	require.Contains(t, msgs[0].Content, "visible text")
	require.Equal(t, model.RoleUser, msgs[1].Role)
	require.Contains(t, msgs[1].Content, mcpImagesUserContent)
	require.Contains(t, msgs[1].Content, "/tmp/screenshot.png")
	require.Contains(t, msgs[1].Content, "image_inspect")
	require.Len(t, msgs[1].ContentParts, 1)
	require.NotNil(t, msgs[1].ContentParts[0].Image)
	require.Equal(t, raw, msgs[1].ContentParts[0].Image.Data)
	require.Equal(t, "png", msgs[1].ContentParts[0].Image.Format)
	require.Equal(t, mcpImageDetailAuto, msgs[1].ContentParts[0].Image.Detail)
}

func TestMCPImageResultMessages_DoesNotEscapeVisibleJSONText(
	t *testing.T,
) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)
	in := &tool.ToolResultMessagesInput{
		DefaultToolMessage: model.Message{Role: model.RoleTool},
		Result: map[string]any{
			"content": []mcpContentItem{
				{Type: "text", Data: "a < b & c > d"},
				{
					Type:     "image",
					Data:     encoded,
					MimeType: "image/png",
				},
			},
		},
	}

	got, err := mcpImageResultMessages(context.Background(), in)
	require.NoError(t, err)

	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.NotEmpty(t, msgs)
	require.Contains(t, msgs[0].Content, "a < b & c > d")
	require.NotContains(t, msgs[0].Content, `\u003c`)
	require.NotContains(t, msgs[0].Content, `\u003e`)
	require.NotContains(t, msgs[0].Content, `\u0026`)
}

func TestMCPImageResultMessages_ConsumesAttachmentBudget(t *testing.T) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)
	defaultMsg := model.Message{
		Role:    model.RoleTool,
		ToolID:  "tool-call-1",
		Content: "[]",
	}
	in := &tool.ToolResultMessagesInput{
		ToolName:           "browser_page_screenshot",
		ToolCallID:         "tool-call-1",
		DefaultToolMessage: defaultMsg,
		Result: []mcpContentItem{
			{
				Type:     "image",
				Data:     encoded,
				MimeType: "image/png",
			},
			{
				Type:     "image",
				Data:     encoded,
				MimeType: "image/png",
			},
		},
	}
	ctx := tool.WithToolResultAttachmentBudget(context.Background(), 1)

	got, err := mcpImageResultMessages(ctx, in)
	require.NoError(t, err)

	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 2)
	require.Len(t, msgs[1].ContentParts, 1)

	got, err = mcpImageResultMessages(ctx, in)
	require.NoError(t, err)
	msgs, ok = got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 1)
	require.NotContains(t, msgs[0].Content, encoded)
	require.Contains(t, msgs[0].Content, mcpImageDataOmitted)
}

func TestMCPImageResultMessages_NoImagesFallsBack(t *testing.T) {
	t.Parallel()

	defaultMsg := model.Message{Role: model.RoleTool}
	in := &tool.ToolResultMessagesInput{
		DefaultToolMessage: defaultMsg,
		Result:             []mcpContentItem{{Type: "text"}},
	}

	got, err := mcpImageResultMessages(context.Background(), in)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMCPImageResultMessages_BadBase64SanitizesToolMessage(t *testing.T) {
	t.Parallel()

	defaultMsg := model.Message{
		Role:    model.RoleTool,
		ToolID:  "tool-call-1",
		Content: "contains raw image",
	}
	in := &tool.ToolResultMessagesInput{
		DefaultToolMessage: defaultMsg,
		Result: []mcpContentItem{{
			Type:     "image",
			Data:     "not base64",
			MimeType: "image/png",
		}},
	}

	got, err := mcpImageResultMessages(context.Background(), in)
	require.NoError(t, err)
	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 1)
	require.NotContains(t, msgs[0].Content, "not base64")
	require.Contains(t, msgs[0].Content, mcpImageDataOmitted)
}

func TestMCPImageResultMessages_NilInputFallsBack(t *testing.T) {
	t.Parallel()

	got, err := mcpImageResultMessages(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMCPImageResultMessages_BadDefaultMessageFallsBack(t *testing.T) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)

	in := &tool.ToolResultMessagesInput{
		DefaultToolMessage: "not a model.Message",
		Result: []mcpContentItem{{
			Type:     "image",
			Data:     encoded,
			MimeType: "image/png",
		}},
	}

	got, err := mcpImageResultMessages(context.Background(), in)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestSanitizedMCPImageToolMessageFallbacks(t *testing.T) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)
	msg := model.Message{
		Role:    model.RoleTool,
		Content: "original content",
	}
	items := []mcpContentItem{
		{Type: "text", Data: "visible <text>"},
		{Type: "image", Data: encoded, MimeType: "image/png"},
		{Type: "image", Data: "keep-me", MimeType: "image/tiff"},
	}

	got := sanitizedMCPImageToolMessage(msg, items, items)
	require.NotContains(t, got.Content, encoded)
	require.Contains(t, got.Content, mcpImageDataOmitted)
	require.Contains(t, got.Content, "visible <text>")
	require.Contains(t, got.Content, "keep-me")
	require.NotContains(t, got.Content, `\u003c`)

	got = sanitizedMCPImageToolMessage(msg, func() {}, items)
	require.NotContains(t, got.Content, encoded)
	require.Contains(t, got.Content, mcpImageDataOmitted)

	got = sanitizedMCPImageToolMessage(msg, nil, nil)
	require.Equal(t, msg, got)
}

func TestMCPImageSavedPathFallbacks(t *testing.T) {
	t.Parallel()

	require.Empty(t, mcpImageSavedPath(func() {}))
	require.Empty(t, mcpImageSavedPath(map[string]any{
		"screenshotPath": " \t ",
	}))
	require.Empty(t, mcpImageSavedPath(map[string]any{
		"content": make(chan int),
	}))
}

func TestSanitizedMCPResultJSONFallbacks(t *testing.T) {
	t.Parallel()

	items := []mcpContentItem{{Type: "text", Data: "visible"}}

	content, ok := sanitizedMCPResultJSON(func() {}, items)
	require.False(t, ok)
	require.Empty(t, content)

	content, ok = sanitizedMCPResultJSON([]mcpContentItem{}, items)
	require.False(t, ok)
	require.Empty(t, content)
}

func TestMarshalModelVisibleJSONError(t *testing.T) {
	t.Parallel()

	body, err := marshalModelVisibleJSON(func() {})
	require.Error(t, err)
	require.Nil(t, body)
}

func TestExtractMCPImages_NilResultReturnsNil(t *testing.T) {
	t.Parallel()

	images := extractMCPImages(context.Background(), nil)
	require.Nil(t, images)
}

func TestExtractMCPImages_MarshalErrorReturnsNil(t *testing.T) {
	t.Parallel()

	images := extractMCPImages(context.Background(), func() {})
	require.Nil(t, images)
}

func TestExtractMCPImages_UnmarshalErrorReturnsNil(t *testing.T) {
	t.Parallel()

	images := extractMCPImages(context.Background(), "not an array")
	require.Nil(t, images)
}

func TestExtractMCPImages_UnwrapsNestedContent(t *testing.T) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)

	images := extractMCPImages(context.Background(), map[string]any{
		"action": "screenshot",
		"content": []mcpContentItem{{
			Type:     "image",
			Data:     encoded,
			MimeType: "image/png",
		}},
	})
	require.Len(t, images, 1)
	require.Equal(t, raw, images[0].Data)
	require.Equal(t, "png", images[0].Format)
}

func TestExtractMCPImages_UnsupportedMimeIsSkipped(t *testing.T) {
	t.Parallel()

	raw := []byte("fake image bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)

	images := extractMCPImages(context.Background(), []mcpContentItem{{
		Type:     "image",
		Data:     encoded,
		MimeType: "image/tiff",
	}})
	require.Nil(t, images)
}

func TestUnwrapMCPResultContent_FallsBackToOriginalResult(
	t *testing.T,
) {
	t.Parallel()

	plain := map[string]any{"message": "hello"}
	require.Equal(t, plain, unwrapMCPResultContent(plain))

	raw := func() {}
	got := unwrapMCPResultContent(raw)
	require.NotNil(t, got)
	_, ok := got.(func())
	require.True(t, ok)

	type invalidEnvelope struct {
		Content json.RawMessage `json:"content"`
	}
	got = unwrapMCPResultContent(invalidEnvelope{
		Content: json.RawMessage("{"),
	})
	_, ok = got.(invalidEnvelope)
	require.True(t, ok)
}

func TestMCPImageFormatFromMime_Unsupported(t *testing.T) {
	t.Parallel()

	_, ok := mcpImageFormatFromMime("application/octet-stream")
	require.False(t, ok)
}

func TestMCPImageFormatFromMime_Supported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mime   string
		format string
	}{
		{mime: "image/png", format: "png"},
		{mime: "image/jpg", format: "jpg"},
		{mime: "image/jpeg", format: "jpeg"},
		{mime: "image/webp", format: "webp"},
		{mime: "image/gif", format: "gif"},
	}

	for _, tt := range tests {
		format, ok := mcpImageFormatFromMime(tt.mime)
		require.True(t, ok)
		require.Equal(t, tt.format, format)
	}
}
