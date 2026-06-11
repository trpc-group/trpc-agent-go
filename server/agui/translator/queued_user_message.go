//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func queuedUserMessageFromContentParts(
	messageID string,
	message model.Message,
) (aguitypes.Message, error) {
	contents, err := inputContentsFromMessage(message)
	if err != nil {
		return aguitypes.Message{}, err
	}
	return aguitypes.Message{
		ID:      messageID,
		Role:    aguitypes.RoleUser,
		Content: contents,
	}, nil
}

func inputContentsFromMessage(message model.Message) ([]aguitypes.InputContent, error) {
	contents := make([]aguitypes.InputContent, 0, len(message.ContentParts)+1)
	if message.Content != "" {
		contents = append(contents, aguitypes.InputContent{
			Type: aguitypes.InputContentTypeText,
			Text: message.Content,
		})
	}
	for _, part := range message.ContentParts {
		content, err := inputContentFromPart(part)
		if err != nil {
			return nil, err
		}
		contents = append(contents, content)
	}
	if len(contents) == 0 {
		return nil, errors.New("queued user message content parts are empty")
	}
	return contents, nil
}

func inputContentFromPart(part model.ContentPart) (aguitypes.InputContent, error) {
	switch part.Type {
	case model.ContentTypeText:
		if part.Text == nil {
			return aguitypes.InputContent{}, errors.New("queued user message text content part is nil")
		}
		return aguitypes.InputContent{
			Type: aguitypes.InputContentTypeText,
			Text: *part.Text,
		}, nil
	case model.ContentTypeImage:
		return inputContentFromImage(part.Image)
	case model.ContentTypeAudio:
		return inputContentFromAudio(part.Audio)
	case model.ContentTypeFile:
		return inputContentFromFile(part.File)
	default:
		return aguitypes.InputContent{}, fmt.Errorf(
			"queued user message content part type unsupported: %s",
			part.Type,
		)
	}
}

func inputContentFromImage(image *model.Image) (aguitypes.InputContent, error) {
	if image == nil {
		return aguitypes.InputContent{}, errors.New("queued user message image content part is nil")
	}
	content := aguitypes.InputContent{
		Type:     aguitypes.InputContentTypeBinary,
		MimeType: binaryMimeType("image", image.Format),
		URL:      strings.TrimSpace(image.URL),
	}
	if len(image.Data) > 0 {
		content.Data = base64.StdEncoding.EncodeToString(image.Data)
	}
	if content.URL == "" && content.Data == "" {
		return aguitypes.InputContent{}, errors.New("queued user message image content part is empty")
	}
	return content, nil
}

func inputContentFromAudio(audio *model.Audio) (aguitypes.InputContent, error) {
	if audio == nil {
		return aguitypes.InputContent{}, errors.New("queued user message audio content part is nil")
	}
	if len(audio.Data) == 0 {
		return aguitypes.InputContent{}, errors.New("queued user message audio content part is empty")
	}
	return aguitypes.InputContent{
		Type:     aguitypes.InputContentTypeBinary,
		MimeType: binaryMimeType("audio", audio.Format),
		Data:     base64.StdEncoding.EncodeToString(audio.Data),
	}, nil
}

func inputContentFromFile(file *model.File) (aguitypes.InputContent, error) {
	if file == nil {
		return aguitypes.InputContent{}, errors.New("queued user message file content part is nil")
	}
	// File parts may carry any full MIME type; the application kind only
	// supplies defaults for missing or short formats such as "pdf".
	content := aguitypes.InputContent{
		Type:     aguitypes.InputContentTypeBinary,
		MimeType: binaryMimeType("application", file.MimeType),
		ID:       strings.TrimSpace(file.FileID),
		URL:      strings.TrimSpace(file.URL),
		Filename: strings.TrimSpace(file.Name),
	}
	if len(file.Data) > 0 {
		content.Data = base64.StdEncoding.EncodeToString(file.Data)
	}
	if content.ID == "" && content.URL == "" && content.Data == "" {
		return aguitypes.InputContent{}, errors.New("queued user message file content part is empty")
	}
	return content, nil
}

func binaryMimeType(kind, format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		if kind == "application" {
			return "application/octet-stream"
		}
		return kind + "/*"
	}
	if strings.Contains(format, "/") {
		return format
	}
	return kind + "/" + format
}
