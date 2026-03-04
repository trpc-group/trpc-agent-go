//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package multimodal converts AG-UI multimodal content into internal model messages.
package multimodal

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// CustomEventNameUserMessage is the custom event name used to persist user input messages in the track stream.
	CustomEventNameUserMessage = "trpc-agent-go.user_message"
)

// UserMessageFromInputContents converts AG-UI multimodal input contents into a model user message.
func UserMessageFromInputContents(contents []types.InputContent) (model.Message, error) {
	if len(contents) == 0 {
		return model.Message{}, errors.New("input contents is empty")
	}
	message := model.Message{
		Role: model.RoleUser,
	}
	for _, part := range contents {
		contentPart, err := contentPartFromInputContent(part)
		if err != nil {
			return model.Message{}, err
		}
		if contentPart == nil {
			continue
		}
		message.ContentParts = append(message.ContentParts, *contentPart)
	}
	if len(message.ContentParts) == 0 {
		return model.Message{}, errors.New("no supported input contents")
	}
	return message, nil
}

func contentPartFromInputContent(part types.InputContent) (*model.ContentPart, error) {
	switch part.Type {
	case types.InputContentTypeText:
		text := part.Text
		return &model.ContentPart{
			Type: model.ContentTypeText,
			Text: &text,
		}, nil
	case types.InputContentTypeBinary:
		return contentPartFromBinaryInput(part)
	default:
		return nil, nil
	}
}

func contentPartFromBinaryInput(part types.InputContent) (*model.ContentPart, error) {
	if part.ID == "" && part.URL == "" && part.Data == "" {
		return nil, errors.New("binary input content requires at least one of id, url, or data")
	}
	mimeType := strings.ToLower(strings.TrimSpace(part.MimeType))
	if part.URL != "" {
		if strings.HasPrefix(mimeType, "image/") {
			return &model.ContentPart{
				Type: model.ContentTypeImage,
				Image: &model.Image{
					URL: part.URL,
				},
			}, nil
		}
		text := part.URL
		return &model.ContentPart{
			Type: model.ContentTypeText,
			Text: &text,
		}, nil
	}
	if part.Data != "" {
		payload, err := decodeBase64Payload(part.Data)
		if err != nil {
			return nil, fmt.Errorf("decode binary payload: %w", err)
		}
		if format, ok := strings.CutPrefix(mimeType, "audio/"); ok && format != "" {
			return &model.ContentPart{
				Type: model.ContentTypeAudio,
				Audio: &model.Audio{
					Data:   payload,
					Format: format,
				},
			}, nil
		}
		if format, ok := strings.CutPrefix(mimeType, "image/"); ok && format != "" {
			return &model.ContentPart{
				Type: model.ContentTypeImage,
				Image: &model.Image{
					Data:   payload,
					Format: format,
				},
			}, nil
		}
		filename := part.Filename
		return &model.ContentPart{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name:     filename,
				Data:     payload,
				MimeType: mimeType,
			},
		}, nil
	}
	return &model.ContentPart{
		Type: model.ContentTypeFile,
		File: fileFromBinaryID(part),
	}, nil
}

func fileFromBinaryID(part types.InputContent) *model.File {
	if part.Type != types.InputContentTypeBinary {
		return nil
	}
	file := &model.File{
		Name:   strings.TrimSpace(part.Filename),
		FileID: part.ID,
	}
	if file.Name == "" {
		file.Name = fileNameFromArtifactRef(part.ID)
	}
	return file
}

func fileNameFromArtifactRef(fileID string) string {
	s := strings.TrimSpace(fileID)
	if !strings.HasPrefix(s, fileref.ArtifactPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(s, fileref.ArtifactPrefix)
	name, _, err := codeexecutor.ParseArtifactRef(rest)
	if err != nil {
		return ""
	}
	base := path.Base(strings.TrimSpace(name))
	if base == "." || base == "/" || base == ".." {
		return ""
	}
	return base
}

func decodeBase64Payload(payload string) ([]byte, error) {
	if strings.HasPrefix(payload, "data:") {
		comma := strings.IndexByte(payload, ',')
		if comma < 0 {
			return nil, errors.New("base64 data URL is missing comma separator")
		}
		header := strings.ToLower(payload[:comma])
		if !strings.Contains(header, ";base64") {
			return nil, errors.New("data URL is not base64-encoded")
		}
		payload = payload[comma+1:]
	}
	if payload == "" {
		return nil, errors.New("base64 payload is empty")
	}
	return base64.StdEncoding.DecodeString(payload)
}
