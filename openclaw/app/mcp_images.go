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
	"bytes"
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
	mcpImageDataOmitted  = "[omitted: image data]"
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

	items := extractMCPContentItems(in.Result)
	if countMCPImageItems(items) == 0 {
		return nil, nil
	}
	candidates := countMCPImageCandidates(items)
	defaultMsg = sanitizedMCPImageToolMessage(
		defaultMsg,
		in.Result,
	)
	if candidates == 0 {
		return []model.Message{defaultMsg}, nil
	}
	allowed := tool.ReserveToolResultAttachments(ctx, candidates)
	if allowed <= 0 {
		return []model.Message{defaultMsg}, nil
	}
	images := extractMCPImagesUpTo(ctx, items, allowed)
	if len(images) == 0 {
		return []model.Message{defaultMsg}, nil
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

func sanitizedMCPImageToolMessage(
	msg model.Message,
	result any,
) model.Message {
	content, ok := sanitizedMCPResultJSON(result)
	if !ok {
		msg.Content = mcpImageDataOmitted
		return msg
	}
	if content != "" {
		msg.Content = content
	}
	return msg
}

func sanitizedMCPResultJSON(
	result any,
) (string, bool) {
	body, err := marshalModelVisibleJSON(result)
	if err != nil {
		return "", false
	}
	if content, ok := sanitizeMCPContentJSON(body); ok {
		return string(content), true
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", false
	}
	if _, ok := envelope["content"]; !ok {
		return "", false
	}

	itemBytes, ok := sanitizeMCPContentJSON(envelope["content"])
	if !ok {
		return "", false
	}
	envelope["content"] = itemBytes

	sanitized, err := marshalModelVisibleJSON(envelope)
	if err != nil {
		return "", false
	}
	return string(sanitized), true
}

func sanitizeMCPContentJSON(body []byte) ([]byte, bool) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, false
	}
	found := false
	for _, item := range items {
		var contentType string
		if err := json.Unmarshal(item["type"], &contentType); err != nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(contentType)) !=
			mcpContentTypeImage {
			continue
		}
		omitted, err := json.Marshal(mcpImageDataOmitted)
		if err != nil {
			return nil, false
		}
		item["data"] = omitted
		found = true
	}
	if !found {
		return nil, false
	}
	sanitized, err := marshalModelVisibleJSON(items)
	if err != nil {
		return nil, false
	}
	return sanitized, true
}

func marshalModelVisibleJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func extractMCPImages(ctx context.Context, result any) []mcpImage {
	return extractMCPImagesUpTo(
		ctx,
		extractMCPContentItems(result),
		maxMCPImagesNoLimit,
	)
}

const maxMCPImagesNoLimit = int(^uint(0) >> 1)

func extractMCPContentItems(result any) []mcpContentItem {
	payload := unwrapMCPResultContent(result)
	if payload == nil {
		return nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}

	var items []mcpContentItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil
	}
	return items
}

func countMCPImageCandidates(items []mcpContentItem) int {
	var count int
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Type)) !=
			mcpContentTypeImage {
			continue
		}
		if _, ok := mcpImageFormatFromMime(item.MimeType); ok {
			count++
		}
	}
	return count
}

func countMCPImageItems(items []mcpContentItem) int {
	var count int
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Type)) ==
			mcpContentTypeImage {
			count++
		}
	}
	return count
}

func extractMCPImagesUpTo(
	ctx context.Context,
	items []mcpContentItem,
	maxImages int,
) []mcpImage {
	if maxImages <= 0 {
		return nil
	}

	capacity := len(items)
	if capacity > maxImages {
		capacity = maxImages
	}
	images := make([]mcpImage, 0, capacity)
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
		if len(images) >= maxImages {
			break
		}
	}

	if len(images) == 0 {
		return nil
	}
	return images
}

func unwrapMCPResultContent(result any) any {
	if result == nil {
		return nil
	}

	body, err := json.Marshal(result)
	if err != nil {
		return result
	}

	var envelope struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return result
	}
	if len(envelope.Content) == 0 {
		return result
	}

	var payload any
	if err := json.Unmarshal(envelope.Content, &payload); err != nil {
		return result
	}
	return payload
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
