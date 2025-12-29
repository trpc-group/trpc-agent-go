//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package huggingface

import (
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// convertRequest converts a model.Request to a HuggingFace ChatCompletionRequest.
func (m *Model) convertRequest(req *model.Request) (*ChatCompletionRequest, error) {
	hfReq := &ChatCompletionRequest{
		Model:            m.name,
		Messages:         make([]ChatMessage, 0, len(req.Messages)),
		MaxTokens:        req.MaxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		Stream:           req.Stream,
		Stop:             req.Stop,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		ExtraFields:      make(map[string]any),
	}

	// Convert messages.
	for _, msg := range req.Messages {
		hfMsg, err := convertMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		hfReq.Messages = append(hfReq.Messages, hfMsg)
	}

	// Convert tools.
	if len(req.Tools) > 0 {
		hfReq.Tools = make([]Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			hfTool, err := convertTool(t)
			if err != nil {
				return nil, fmt.Errorf("failed to convert tool: %w", err)
			}
			hfReq.Tools = append(hfReq.Tools, hfTool)
		}
	}

	// Convert structured output to response format.
	if req.StructuredOutput != nil && req.StructuredOutput.Type == model.StructuredOutputJSONSchema {
		hfReq.ResponseFormat = &ResponseFormat{
			Type: "json_object",
		}
	}

	return hfReq, nil
}

// convertMessage converts a model.Message to a HuggingFace ChatMessage.
func convertMessage(msg model.Message) (ChatMessage, error) {
	hfMsg := ChatMessage{
		Role: string(msg.Role),
	}

	// Handle tool message fields.
	if msg.Role == model.RoleTool {
		hfMsg.ToolCallID = msg.ToolID
		hfMsg.Name = msg.ToolName
	}

	// Convert content - prioritize Content string field.
	if msg.Content != "" {
		hfMsg.Content = msg.Content
	} else if len(msg.ContentParts) > 0 {
		// Check if all content parts are text.
		allText := true
		for _, part := range msg.ContentParts {
			if part.Type != model.ContentTypeText {
				allText = false
				break
			}
		}

		if allText && len(msg.ContentParts) == 1 && msg.ContentParts[0].Text != nil {
			// Single text content - use string format.
			hfMsg.Content = *msg.ContentParts[0].Text
		} else {
			// Multiple parts or non-text content - use array format.
			contentParts := make([]ContentPart, 0, len(msg.ContentParts))
			for _, part := range msg.ContentParts {
				hfPart, err := convertContentPart(part)
				if err != nil {
					return ChatMessage{}, fmt.Errorf("failed to convert content part: %w", err)
				}
				contentParts = append(contentParts, hfPart)
			}
			hfMsg.Content = contentParts
		}
	}

	// Convert tool calls.
	if len(msg.ToolCalls) > 0 {
		hfMsg.ToolCalls = make([]ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			hfTC, err := convertToolCall(tc)
			if err != nil {
				return ChatMessage{}, fmt.Errorf("failed to convert tool call: %w", err)
			}
			hfMsg.ToolCalls = append(hfMsg.ToolCalls, hfTC)
		}
	}

	return hfMsg, nil
}

// convertContentPart converts a model.ContentPart to a HuggingFace ContentPart.
func convertContentPart(part model.ContentPart) (ContentPart, error) {
	switch part.Type {
	case model.ContentTypeText:
		text := ""
		if part.Text != nil {
			text = *part.Text
		}
		return ContentPart{
			Type: "text",
			Text: text,
		}, nil
	case model.ContentTypeImage:
		if part.Image == nil {
			return ContentPart{}, fmt.Errorf("image is nil for image content type")
		}
		// Convert image to image_url format.
		url := part.Image.URL
		if url == "" && len(part.Image.Data) > 0 {
			// If data is provided, create a data URL.
			url = fmt.Sprintf("data:image/%s;base64,%s", part.Image.Format, part.Image.Data)
		}
		return ContentPart{
			Type: "image_url",
			ImageURL: &ImageURL{
				URL:    url,
				Detail: part.Image.Detail,
			},
		}, nil
	default:
		return ContentPart{}, fmt.Errorf("unsupported content type: %s", part.Type)
	}
}

// convertTool converts a tool.Tool to a HuggingFace Tool.
func convertTool(t tool.Tool) (Tool, error) {
	// Get tool declaration.
	decl := t.Declaration()

	// Convert parameters to map.
	var params map[string]any
	if decl.InputSchema != nil {
		paramsJSON, err := json.Marshal(decl.InputSchema)
		if err != nil {
			return Tool{}, fmt.Errorf("failed to marshal tool parameters: %w", err)
		}
		if err := json.Unmarshal(paramsJSON, &params); err != nil {
			return Tool{}, fmt.Errorf("failed to unmarshal tool parameters: %w", err)
		}
	}

	return Tool{
		Type: "function",
		Function: FunctionTool{
			Name:        decl.Name,
			Description: decl.Description,
			Parameters:  params,
		},
	}, nil
}

// convertToolCall converts a model.ToolCall to a HuggingFace ToolCall.
func convertToolCall(tc model.ToolCall) (ToolCall, error) {
	return ToolCall{
		ID:   tc.ID,
		Type: tc.Type,
		Function: FunctionCall{
			Name:      tc.Function.Name,
			Arguments: string(tc.Function.Arguments),
		},
	}, nil
}

// convertResponse converts a HuggingFace ChatCompletionResponse to a model.Response.
func (m *Model) convertResponse(hfResp *ChatCompletionResponse) *model.Response {
	resp := &model.Response{
		ID:      hfResp.ID,
		Object:  hfResp.Object,
		Created: hfResp.Created,
		Model:   hfResp.Model,
		Choices: make([]model.Choice, 0, len(hfResp.Choices)),
	}

	// Convert choices.
	for _, choice := range hfResp.Choices {
		resp.Choices = append(resp.Choices, convertChoice(choice))
	}

	// Convert usage.
	if hfResp.Usage != nil {
		resp.Usage = &model.Usage{
			PromptTokens:     hfResp.Usage.PromptTokens,
			CompletionTokens: hfResp.Usage.CompletionTokens,
			TotalTokens:      hfResp.Usage.TotalTokens,
		}
	}

	return resp
}

// convertChoice converts a HuggingFace ChatCompletionChoice to a model.Choice.
func convertChoice(choice ChatCompletionChoice) model.Choice {
	var finishReason *string
	if choice.FinishReason != "" {
		finishReason = &choice.FinishReason
	}
	return model.Choice{
		Index:        choice.Index,
		Message:      convertMessageToModel(choice.Message),
		FinishReason: finishReason,
	}
}

// convertChunk converts a HuggingFace ChatCompletionChunk to a model.Response.
func (m *Model) convertChunk(chunk *ChatCompletionChunk) *model.Response {
	resp := &model.Response{
		ID:      chunk.ID,
		Object:  chunk.Object,
		Created: chunk.Created,
		Model:   chunk.Model,
		Choices: make([]model.Choice, 0, len(chunk.Choices)),
	}

	// Convert choices.
	for _, choice := range chunk.Choices {
		var finishReason *string
		if choice.FinishReason != "" {
			fr := choice.FinishReason
			finishReason = &fr
		}
		resp.Choices = append(resp.Choices, model.Choice{
			Index:        choice.Index,
			Delta:        convertMessageToModel(choice.Delta),
			FinishReason: finishReason,
		})
	}

	// Convert usage.
	if chunk.Usage != nil {
		resp.Usage = &model.Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
		}
	}

	return resp
}

// convertMessageToModel converts a HuggingFace ChatMessage to a model.Message.
func convertMessageToModel(hfMsg ChatMessage) model.Message {
	msg := model.Message{
		Role: model.Role(hfMsg.Role),
	}

	// Handle tool message fields.
	if hfMsg.ToolCallID != "" {
		msg.ToolID = hfMsg.ToolCallID
		msg.ToolName = hfMsg.Name
	}

	// Convert content.
	switch content := hfMsg.Content.(type) {
	case string:
		// String content.
		if content != "" {
			msg.Content = content
		}
	case []any:
		// Array content.
		msg.ContentParts = make([]model.ContentPart, 0, len(content))
		for _, part := range content {
			if partMap, ok := part.(map[string]any); ok {
				contentPart := convertContentPartToModel(partMap)
				msg.ContentParts = append(msg.ContentParts, contentPart)
			}
		}
	case []ContentPart:
		// Typed array content.
		msg.ContentParts = make([]model.ContentPart, 0, len(content))
		for _, part := range content {
			msg.ContentParts = append(msg.ContentParts, convertContentPartStructToModel(part))
		}
	}

	// Convert tool calls.
	if len(hfMsg.ToolCalls) > 0 {
		msg.ToolCalls = make([]model.ToolCall, 0, len(hfMsg.ToolCalls))
		for _, tc := range hfMsg.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: model.FunctionDefinitionParam{
					Name:      tc.Function.Name,
					Arguments: []byte(tc.Function.Arguments),
				},
			})
		}
	}

	return msg
}

// convertContentPartToModel converts a content part map to a model.ContentPart.
func convertContentPartToModel(partMap map[string]any) model.ContentPart {
	partType, _ := partMap["type"].(string)

	switch partType {
	case "text":
		text, _ := partMap["text"].(string)
		textPtr := &text
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: textPtr,
		}
	case "image_url":
		imageURL, _ := partMap["image_url"].(map[string]any)
		url, _ := imageURL["url"].(string)
		detail, _ := imageURL["detail"].(string)
		return model.ContentPart{
			Type: model.ContentTypeImage,
			Image: &model.Image{
				URL:    url,
				Detail: detail,
			},
		}
	default:
		text := fmt.Sprintf("unsupported content type: %s", partType)
		textPtr := &text
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: textPtr,
		}
	}
}

// convertContentPartStructToModel converts a ContentPart struct to a model.ContentPart.
func convertContentPartStructToModel(part ContentPart) model.ContentPart {
	switch part.Type {
	case "text":
		textPtr := &part.Text
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: textPtr,
		}
	case "image_url":
		if part.ImageURL != nil {
			return model.ContentPart{
				Type: model.ContentTypeImage,
				Image: &model.Image{
					URL:    part.ImageURL.URL,
					Detail: part.ImageURL.Detail,
				},
			}
		}
		text := "image_url is nil"
		textPtr := &text
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: textPtr,
		}
	default:
		text := fmt.Sprintf("unsupported content type: %s", part.Type)
		textPtr := &text
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: textPtr,
		}
	}
}
