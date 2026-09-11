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

func TestUserMessageFromModelText(t *testing.T) {
	got, err := UserMessageFromModel("message-1", model.Message{
		Content: "hello",
	})
	require.NoError(t, err)
	assert.Equal(t, "message-1", got.ID)
	assert.Equal(t, types.RoleUser, got.Role)
	assert.Equal(t, "hello", got.Content)
}

func TestUserMessageFromModelContentParts(t *testing.T) {
	text := "part text"
	got, err := UserMessageFromModel("message-2", model.Message{
		Content: "content text",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{
				Type: model.ContentTypeImage,
				Image: &model.Image{
					URL:    " https://example.com/image.png ",
					Data:   []byte("image"),
					Format: " PNG ",
				},
			},
			{
				Type: model.ContentTypeAudio,
				Audio: &model.Audio{
					Data: []byte("audio"),
				},
			},
			{
				Type: model.ContentTypeVideo,
				Video: &model.Video{
					URL:    " https://example.com/video.mp4 ",
					Data:   []byte("video"),
					Format: " VIDEO/MP4 ",
				},
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     " demo.pdf ",
					URL:      " https://example.com/demo.pdf ",
					Data:     []byte("file"),
					FileID:   " file-1 ",
					MimeType: " PDF ",
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "message-2", got.ID)
	assert.Equal(t, types.RoleUser, got.Role)

	contents, ok := got.ContentInputContents()
	require.True(t, ok)
	require.Len(t, contents, 6)
	assert.Equal(t, types.InputContent{
		Type: types.InputContentTypeText,
		Text: "content text",
	}, contents[0])
	assert.Equal(t, types.InputContent{
		Type: types.InputContentTypeText,
		Text: text,
	}, contents[1])
	assert.Equal(t, types.InputContent{
		Type:     types.InputContentTypeBinary,
		MimeType: "image/png",
		URL:      "https://example.com/image.png",
		Data:     base64.StdEncoding.EncodeToString([]byte("image")),
	}, contents[2])
	assert.Equal(t, types.InputContent{
		Type:     types.InputContentTypeBinary,
		MimeType: "audio/*",
		Data:     base64.StdEncoding.EncodeToString([]byte("audio")),
	}, contents[3])
	assert.Equal(t, types.InputContent{
		Type:     types.InputContentTypeBinary,
		MimeType: "video/mp4",
		URL:      "https://example.com/video.mp4",
		Data:     base64.StdEncoding.EncodeToString([]byte("video")),
	}, contents[4])
	assert.Equal(t, types.InputContent{
		Type:     types.InputContentTypeBinary,
		MimeType: "application/pdf",
		ID:       "file-1",
		URL:      "https://example.com/demo.pdf",
		Data:     base64.StdEncoding.EncodeToString([]byte("file")),
		Filename: "demo.pdf",
	}, contents[5])
}

func TestUserMessageFromModelErrors(t *testing.T) {
	tests := []struct {
		name    string
		message model.Message
		wantErr string
	}{
		{
			name:    "empty message",
			message: model.Message{},
			wantErr: "content parts are empty",
		},
		{
			name: "nil text",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
			}}},
			wantErr: "text content part is nil",
		},
		{
			name: "nil image",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeImage,
			}}},
			wantErr: "image content part is nil",
		},
		{
			name: "empty image",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{},
			}}},
			wantErr: "image content part is empty",
		},
		{
			name: "nil audio",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeAudio,
			}}},
			wantErr: "audio content part is nil",
		},
		{
			name: "empty audio",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeAudio,
				Audio: &model.Audio{},
			}}},
			wantErr: "audio content part is empty",
		},
		{
			name: "nil video",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeVideo,
			}}},
			wantErr: "video content part is nil",
		},
		{
			name: "empty video",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeVideo,
				Video: &model.Video{},
			}}},
			wantErr: "video content part is empty",
		},
		{
			name: "nil file",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
			}}},
			wantErr: "file content part is nil",
		},
		{
			name: "empty file",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{},
			}}},
			wantErr: "file content part is empty",
		},
		{
			name: "unsupported type",
			message: model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentType("unknown"),
			}}},
			wantErr: "content part type unsupported: unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UserMessageFromModel("message", tt.message)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestBinaryMimeType(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		format string
		want   string
	}{
		{name: "application default", kind: "application", want: "application/octet-stream"},
		{name: "media default", kind: "image", want: "image/*"},
		{name: "short format", kind: "audio", format: " MP3 ", want: "audio/mp3"},
		{name: "full mime type", kind: "video", format: " VIDEO/MP4 ", want: "video/mp4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, binaryMimeType(tt.kind, tt.format))
		})
	}
}
