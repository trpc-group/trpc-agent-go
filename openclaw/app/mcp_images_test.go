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
		Content:  "[]",
	}

	in := &tool.ToolResultMessagesInput{
		ToolName:           "browser_page_screenshot",
		ToolCallID:         "tool-call-1",
		DefaultToolMessage: defaultMsg,
		Arguments:          []byte(`{}`),
		Result:             []mcpContentItem{{Type: "text"}},
		Declaration:        nil,
	}
	in.Result = append(in.Result.([]mcpContentItem), mcpContentItem{
		Type:     "image",
		Data:     encoded,
		MimeType: "image/png",
	})

	got, err := mcpImageResultMessages(context.Background(), in)
	require.NoError(t, err)

	msgs, ok := got.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 2)

	require.Equal(t, defaultMsg, msgs[0])
	require.Equal(t, model.RoleUser, msgs[1].Role)
	require.Equal(t, mcpImagesUserContent, msgs[1].Content)
	require.Len(t, msgs[1].ContentParts, 1)
	require.NotNil(t, msgs[1].ContentParts[0].Image)
	require.Equal(t, raw, msgs[1].ContentParts[0].Image.Data)
	require.Equal(t, "png", msgs[1].ContentParts[0].Image.Format)
	require.Equal(t, mcpImageDetailAuto, msgs[1].ContentParts[0].Image.Detail)
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

func TestMCPImageResultMessages_BadBase64FallsBack(t *testing.T) {
	t.Parallel()

	defaultMsg := model.Message{Role: model.RoleTool}
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
	require.Nil(t, got)
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
