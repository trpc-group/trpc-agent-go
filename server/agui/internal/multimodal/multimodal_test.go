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
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
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
	t.Run("binary whitespace url requires payload", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type: types.InputContentTypeBinary,
			URL:  "  ",
		}})
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
	t.Run("typed input requires source", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type: types.InputContentTypeImage,
		}})
		assert.ErrorContains(t, err, "image input content requires source")
	})
	t.Run("typed input requires source value", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type: types.InputContentTypeImage,
			Source: &types.InputContentSource{
				Type:  types.InputContentSourceTypeURL,
				Value: " ",
			},
		}})
		assert.ErrorContains(t, err, "image input content source value is empty")
	})
	t.Run("typed data requires mime type", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type: types.InputContentTypeAudio,
			Source: &types.InputContentSource{
				Type:  types.InputContentSourceTypeData,
				Value: "AQID",
			},
		}})
		assert.ErrorContains(t, err, "audio data source requires mime type")
	})
	t.Run("typed data rejects invalid base64", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type: types.InputContentTypeVideo,
			Source: &types.InputContentSource{
				Type:     types.InputContentSourceTypeData,
				Value:    "not-base64",
				MimeType: "video/mp4",
			},
		}})
		assert.ErrorContains(t, err, "decode video payload")
	})
	t.Run("typed data requires matching mime type", func(t *testing.T) {
		_, err := UserMessageFromInputContents([]types.InputContent{{
			Type: types.InputContentTypeImage,
			Source: &types.InputContentSource{
				Type:     types.InputContentSourceTypeData,
				Value:    "AQID",
				MimeType: "audio/mpeg",
			},
		}})
		assert.ErrorContains(t, err, "image data source requires image mime type")
	})
}

func TestUserMessageFromInputContentsTextAndImageURL(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{Type: types.InputContentTypeText, Text: "hello"},
		{Type: types.InputContentTypeBinary, MimeType: "image/jpeg", URL: " https://example.com/a.jpg "},
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
	assert.Equal(t, "image/jpeg", msg.ContentParts[1].Image.Format)
}

func TestUserMessageFromInputContentsTypedImageData(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type: types.InputContentTypeImage,
			Source: &types.InputContentSource{
				Type:     types.InputContentSourceTypeData,
				Value:    base64.StdEncoding.EncodeToString(payload),
				MimeType: "image/png",
			},
			Metadata: map[string]any{"filename": "demo.png"},
		},
		{Type: types.InputContentTypeText, Text: "describe this image"},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 2)

	assert.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Image)
	assert.Equal(t, payload, msg.ContentParts[0].Image.Data)
	assert.Equal(t, "png", msg.ContentParts[0].Image.Format)

	assert.Equal(t, model.ContentTypeText, msg.ContentParts[1].Type)
	require.NotNil(t, msg.ContentParts[1].Text)
	assert.Equal(t, "describe this image", *msg.ContentParts[1].Text)
}

func TestUserMessageFromInputContentsTypedImageURL(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{{
		Type: types.InputContentTypeImage,
		Source: &types.InputContentSource{
			Type:  types.InputContentSourceTypeURL,
			Value: " https://example.com/a.jpg ",
		},
	}})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Image)
	assert.Equal(t, "https://example.com/a.jpg", msg.ContentParts[0].Image.URL)
}

func TestUserMessageFromInputContentsTypedAudioURL(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{{
		Type: types.InputContentTypeAudio,
		Source: &types.InputContentSource{
			Type:     types.InputContentSourceTypeURL,
			Value:    "https://example.com/audio.mp3",
			MimeType: "audio/mpeg",
		},
	}})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeAudio, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Audio)
	assert.Equal(t, "https://example.com/audio.mp3", msg.ContentParts[0].Audio.URL)
	assert.Equal(t, "audio/mpeg", msg.ContentParts[0].Audio.Format)
}

func TestUserMessageFromInputContentsTypedVideoData(t *testing.T) {
	payload := []byte("video")
	msg, err := UserMessageFromInputContents([]types.InputContent{{
		Type: types.InputContentTypeVideo,
		Source: &types.InputContentSource{
			Type:     types.InputContentSourceTypeData,
			Value:    base64.StdEncoding.EncodeToString(payload),
			MimeType: "video/mp4",
		},
	}})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeVideo, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Video)
	assert.Equal(t, payload, msg.ContentParts[0].Video.Data)
	assert.Equal(t, "mp4", msg.ContentParts[0].Video.Format)
}

func TestUserMessageFromInputContentsTypedVideoURL(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{{
		Type: types.InputContentTypeVideo,
		Source: &types.InputContentSource{
			Type:     types.InputContentSourceTypeURL,
			Value:    "https://example.com/video.mp4",
			MimeType: "video/mp4",
		},
	}})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeVideo, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Video)
	assert.Equal(t, "https://example.com/video.mp4", msg.ContentParts[0].Video.URL)
	assert.Equal(t, "video/mp4", msg.ContentParts[0].Video.Format)
}

func TestUserMessageFromInputContentsTypedDocumentData(t *testing.T) {
	payload := []byte("document")
	msg, err := UserMessageFromInputContents([]types.InputContent{{
		Type: types.InputContentTypeDocument,
		Source: &types.InputContentSource{
			Type:     types.InputContentSourceTypeData,
			Value:    base64.StdEncoding.EncodeToString(payload),
			MimeType: "application/pdf",
		},
		Metadata: map[string]any{"filename": "demo.pdf"},
	}})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Empty(t, msg.ContentParts[0].File.Name)
	assert.Equal(t, payload, msg.ContentParts[0].File.Data)
	assert.Equal(t, "application/pdf", msg.ContentParts[0].File.MimeType)
}

func TestUserMessageFromInputContentsTypedDocumentURL(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{{
		Type: types.InputContentTypeDocument,
		Source: &types.InputContentSource{
			Type:     types.InputContentSourceTypeURL,
			Value:    "https://example.com/document.pdf",
			MimeType: "application/pdf",
		},
	}})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "https://example.com/document.pdf", msg.ContentParts[0].File.URL)
	assert.Equal(t, "application/pdf", msg.ContentParts[0].File.MimeType)
}

func TestUserMessageFromInputContentsBinaryURLFile(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type:     types.InputContentTypeBinary,
			MimeType: " Application/PDF ",
			Filename: "demo.pdf",
			URL:      "https://example.com/a.pdf",
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "demo.pdf", msg.ContentParts[0].File.Name)
	assert.Equal(t, "https://example.com/a.pdf", msg.ContentParts[0].File.URL)
	assert.Equal(t, "application/pdf", msg.ContentParts[0].File.MimeType)
}

func TestUserMessageFromInputContentsBinaryURLFileKeepsNonPDFMimeType(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type:     types.InputContentTypeBinary,
			MimeType: "application/json",
			Filename: "data.json",
			URL:      "https://example.com/data.json",
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "data.json", msg.ContentParts[0].File.Name)
	assert.Equal(t, "https://example.com/data.json", msg.ContentParts[0].File.URL)
	assert.Equal(t, "application/json", msg.ContentParts[0].File.MimeType)
}

func TestUserMessageFromInputContentsBinaryURLFileDoesNotInferNameFromURLPath(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type: types.InputContentTypeBinary,
			URL:  "https://example.com/files/report.pdf?sign=1",
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Empty(t, msg.ContentParts[0].File.Name)
	assert.Equal(t, "https://example.com/files/report.pdf?sign=1", msg.ContentParts[0].File.URL)
	assert.Empty(t, msg.ContentParts[0].File.MimeType)
}

func TestUserMessageFromInputContentsBinaryURLFileWithoutNameOrMimeType(t *testing.T) {
	msg, err := UserMessageFromInputContents([]types.InputContent{
		{Type: types.InputContentTypeBinary, URL: "https://example.com"},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Empty(t, msg.ContentParts[0].File.Name)
	assert.Equal(t, "https://example.com", msg.ContentParts[0].File.URL)
	assert.Empty(t, msg.ContentParts[0].File.MimeType)
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
		{
			Type:     types.InputContentTypeBinary,
			ID:       "file-123",
			Filename: "demo.pdf",
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "demo.pdf", msg.ContentParts[0].File.Name)
	assert.Equal(t, "file-123", msg.ContentParts[0].File.FileID)
}

func TestUserMessageFromInputContentsBinaryIDFile_ArtifactRef(t *testing.T) {
	const artifactRef = "artifact://uploads/demo.pdf@0"

	msg, err := UserMessageFromInputContents([]types.InputContent{
		{
			Type: types.InputContentTypeBinary,
			ID:   artifactRef,
		},
	})
	require.NoError(t, err)
	require.Len(t, msg.ContentParts, 1)
	assert.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	assert.Equal(t, "demo.pdf", msg.ContentParts[0].File.Name)
	assert.Equal(t, artifactRef, msg.ContentParts[0].File.FileID)
}

func TestFileFromBinaryID_NonBinaryNil(t *testing.T) {
	got := fileFromBinaryID(types.InputContent{
		Type: types.InputContentTypeText,
	})
	assert.Nil(t, got)
}

func TestFileNameFromArtifactRef_EdgeCases(t *testing.T) {
	assert.Equal(t, "", fileNameFromArtifactRef("file-123"))

	nameWithAt := fileref.ArtifactPrefix + "uploads/a@x"
	assert.Equal(t, "a@x", fileNameFromArtifactRef(nameWithAt))

	nameWithAtAndVersion := fileref.ArtifactPrefix +
		"uploads/skey=@crypt_abc.jpeg@0"
	assert.Equal(t, "skey=@crypt_abc.jpeg",
		fileNameFromArtifactRef(nameWithAtAndVersion))

	invalidBase := fileref.ArtifactPrefix + "..@0"
	assert.Equal(t, "", fileNameFromArtifactRef(invalidBase))
}
