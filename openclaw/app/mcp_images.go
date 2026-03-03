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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	mcpContentTypeImage = "image"

	mcpMimeTypePNG  = "image/png"
	mcpMimeTypeJPG  = "image/jpg"
	mcpMimeTypeJPEG = "image/jpeg"
	mcpMimeTypeWebP = "image/webp"
	mcpMimeTypeGIF  = "image/gif"

	mcpImageDetailAuto = "auto"

	mcpImagesUserContent = "MCP tool returned image(s)."
)

type mcpContentItem struct {
	Type     string `json:"type,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type mcpImage struct {
	Data   []byte
	Format string
}

func mcpImageResultMessages(
	ctx context.Context,
	in *tool.ToolResultMessagesInput,
) (any, error) {
	if in == nil {
		return nil, nil
	}

	defaultMsg, ok := in.DefaultToolMessage.(model.Message)
	if !ok {
		return nil, nil
	}

	images := extractMCPImages(ctx, in.Result)
	if len(images) == 0 {
		return nil, nil
	}

	userMsg := model.Message{
		Role:    model.RoleUser,
		Content: mcpImagesUserContent,
	}
	for _, img := range images {
		userMsg.AddImageData(img.Data, mcpImageDetailAuto, img.Format)
	}

	return []model.Message{defaultMsg, userMsg}, nil
}

func extractMCPImages(ctx context.Context, result any) []mcpImage {
	if result == nil {
		return nil
	}

	body, err := json.Marshal(result)
	if err != nil {
		return nil
	}

	var items []mcpContentItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil
	}

	images := make([]mcpImage, 0, len(items))
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Type)) !=
			mcpContentTypeImage {
			continue
		}

		format, ok := mcpImageFormatFromMime(item.MimeType)
		if !ok {
			continue
		}

		data, err := base64.StdEncoding.DecodeString(item.Data)
		if err != nil {
			log.DebugContext(
				ctx,
				"Failed to decode MCP image data",
				"tool_result_item_mime",
				item.MimeType,
				"error",
				err,
			)
			continue
		}

		images = append(images, mcpImage{
			Data:   data,
			Format: format,
		})
	}

	if len(images) == 0 {
		return nil
	}
	return images
}

func mcpImageFormatFromMime(mime string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case mcpMimeTypePNG:
		return "png", true
	case mcpMimeTypeJPG:
		return "jpg", true
	case mcpMimeTypeJPEG:
		return "jpeg", true
	case mcpMimeTypeWebP:
		return "webp", true
	case mcpMimeTypeGIF:
		return "gif", true
	default:
		return "", false
	}
}
