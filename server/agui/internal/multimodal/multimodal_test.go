//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package multimodal

import (
	"encoding/base64"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestUserMessageFromInputContentsErrors(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, err := UserMessageFromInputContents(nil)
		assert.ErrorContains(t, err, "input contents is empty")
	})
	t.Run("unsupported only", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{Type: "unknown"}})
		assert.ErrorContains(t, err, "no supported input contents")
	})
	t.Run("binary requires payload", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{Type: types.InputContentTypeBinary, MimeType: "image/jpeg"}})
		assert.ErrorContains(t, err, "binary input content requires at least one of id, url, or data")
	})
	t.Run("binary data URL missing comma", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type:     types.InputContentTypeBinary,
			MimeType: "image/png",
			Data:     "data:image/png;base64",
		}})
		assert.ErrorContains(t, err, "decode binary payload")
		assert.ErrorContains(t, err, "base64 data URL is missing comma separator")
	})
	t.Run("binary data URL not base64", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type:     types.InputContentTypeBinary,
			MimeType: "image/png",
			Data:     "data:image/png," + base64.StdEncoding.EncodeToString([]byte{0x01}),
		}})
		assert.ErrorContains(t, err, "decode binary payload")
		assert.ErrorContains(t, err, "data URL is not base64-encoded")
	})
	t.Run("binary invalid base64", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type:     types.InputContentTypeBinary,
			MimeType: "audio/wav",
			Data:     "not-base64",
		}})
		assert.ErrorContains(t, err, "decode binary payload")
	})
	t.Run("binary empty base64", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type:     types.InputContentTypeBinary,
			MimeType: "audio/wav",
			Data:     " ",
		}})
		assert.ErrorContains(t, err, "decode binary payload")
		assert.ErrorContains(t, err, "illegal base64 data")
	})
}

func TestUserMessageFromInputContentsTextAndImageURL(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{Type: types.InputContentTypeText, Text: "hello"},
		{Type: types.InputContentTypeBinary, MimeType: "image/jpeg", URL: "https://example.com/a.jpg"},
	})
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, msg.Role)
	require.Len(t, msg.ContentParts, 2)

	require.NotNil(t, msg.ContentParts[0].Text)
	assert.Equal(t, model.ContentTypeText, msg.ContentParts[0].Type)
	assert.Equal(t, "hello", *msg.ContentParts[0].Text)

	assert.Equal(t, model.ContentTypeImage, msg.ContentParts[1].Type)
	require.NotNil(t, msg.ContentParts[1].Image)
	assert.Equal(t, "https://example.com/a.jpg", msg.ContentParts[1].Image.URL)
	assert.Empty(t, msg.ContentParts[1].Image.Detail)
	assert.Empty(t, msg.ContentParts[1].Image.Format)
}

func TestUserMessageFromInputContentsBinaryURLNonImageFallsBackToText(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{Type: types.InputContentTypeBinary, MimeType: "application/pdf", URL: "https://example.com/a.pdf"},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeText, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Text)
	assert.Equal(t, "https://example.com/a.pdf", *msg.ContentParts[0].Text)
}

func TestUserMessageFromInputContentsBinaryDataAudio(t *testing.T) {
	payload := []byte("hello")
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type:     types.InputContentTypeBinary,
			MimeType: "audio/wav",
			Data:     base64.StdEncoding.EncodeToString(payload),
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeAudio, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Audio)
	assert.Equal(t, payload, msg.ContentParts[0].Audio.Data)
	assert.Equal(t, "wav", msg.ContentParts[0].Audio.Format)
}

func TestUserMessageFromInputContentsBinaryDataImage(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type:     types.InputContentTypeBinary,
			MimeType: "image/png",
			Data:     base64.StdEncoding.EncodeToString(payload),
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Image)
	assert.Equal(t, payload, msg.ContentParts[0].Image.Data)
	assert.Equal(t, "png", msg.ContentParts[0].Image.Format)
	assert.Empty(t, msg.ContentParts[0].Image.Detail)
}

func TestUserMessageFromInputContentsBinaryDataImage_DataURL(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type:     types.InputContentTypeBinary,
			MimeType: "image/png",
			Data:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload),
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Image)
	assert.Equal(t, payload, msg.ContentParts[0].Image.Data)
	assert.Equal(t, "png", msg.ContentParts[0].Image.Format)
	assert.Empty(t, msg.ContentParts[0].Image.Detail)
}

func TestUserMessageFromInputContentsBinaryDataFile(t *testing.T) {
	payload := []byte("file payload")
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type:     types.InputContentTypeBinary,
			MimeType: " Application/PDF ",
			Filename: "demo.pdf",
			Data:     base64.StdEncoding.EncodeToString(payload),
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "demo.pdf", msg.ContentParts[0].File.Name)
	assert.Equal(t, payload, msg.ContentParts[0].File.Data)
	assert.Equal(t, "application/pdf", msg.ContentParts[0].File.MimeType)
}

func TestUserMessageFromInputContentsBinaryIDFile(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{Type: types.InputContentTypeBinary, ID: "file-123"},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "file-123", msg.ContentParts[0].File.FileID)
}
