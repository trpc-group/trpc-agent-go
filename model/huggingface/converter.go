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
		N:                req.N,
		Stream:           req.Stream,
		Stop:             req.Stop,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		Seed:             req.Seed,
		ExtraFields:      make(map[string]interface{}),
	}

	// Convert messages
	for _, msg := range req.Messages {
		hfMsg, err := convertMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		hfReq.Messages = append(hfReq.Messages, hfMsg)
	}

	// Convert tools
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

	// Convert tool choice
	if req.ToolChoice != nil {
		hfReq.ToolChoice = req.ToolChoice
	}

	// Convert response format
	if req.ResponseFormat != nil {
		hfReq.ResponseFormat = &ResponseFormat{
			Type: string(*req.ResponseFormat),
		}
	}

	return hfReq, nil
}

// convertMessage converts a model.Message to a HuggingFace ChatMessage.
func convertMessage(msg model.Message) (ChatMessage, error) {
	hfMsg := ChatMessage{
		Role:       string(msg.Role),
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
		Refusal:    msg.Refusal,
	}

	// Convert content
	if len(msg.Content) > 0 {
		// Check if all content parts are text
		allText := true
		for _, part := range msg.Content {
			if part.Type != model.ContentTypeText {
				allText = false
				break
			}
		}

		if allText && len(msg.Content) == 1 {
			// Single text content - use string format
			hfMsg.Content = msg.Content[0].Text
		} else {
			// Multiple parts or non-text content - use array format
			contentParts := make([]ContentPart, 0, len(msg.Content))
			for _, part := range msg.Content {
				hfPart, err := convertContentPart(part)
				if err != nil {
					return ChatMessage{}, fmt.Errorf("failed to convert content part: %w", err)
				}
				contentParts = append(contentParts, hfPart)
			}
			hfMsg.Content = contentParts
		}
	}

	// Convert tool calls
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
		return ContentPart{
			Type: "text",
			Text: part.Text,
		}, nil
	case model.ContentTypeImageURL:
		if part.ImageURL == nil {
			return ContentPart{}, fmt.Errorf("image_url is nil for image_url content type")
		}
		return ContentPart{
			Type: "image_url",
			ImageURL: &ImageURL{
				URL:    part.ImageURL.URL,
				Detail: part.ImageURL.Detail,
			},
		}, nil
	default:
		return ContentPart{}, fmt.Errorf("unsupported content type: %s", part.Type)
	}
}

// convertTool converts a tool.Tool to a HuggingFace Tool.
func convertTool(t tool.Tool) (Tool, error) {
	// Get tool definition
	def := t.Definition()

	// Convert parameters to map
	var params map[string]any
	if def.InputSchema != nil {
		paramsJSON, err := json.Marshal(def.InputSchema)
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
			Name:        def.Name,
			Description: def.Description,
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
			Arguments: tc.Function.Arguments,
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

	// Convert choices
	for _, choice := range hfResp.Choices {
		resp.Choices = append(resp.Choices, convertChoice(choice))
	}

	// Convert usage
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
	return model.Choice{
		Index:        choice.Index,
		Message:      convertMessageToModel(choice.Message),
		FinishReason: choice.FinishReason,
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

	// Convert choices
	for _, choice := range chunk.Choices {
		resp.Choices = append(resp.Choices, model.Choice{
			Index:        choice.Index,
			Delta:        convertMessageToModel(choice.Delta),
			FinishReason: choice.FinishReason,
		})
	}

	// Convert usage
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
		Role:       model.Role(hfMsg.Role),
		Name:       hfMsg.Name,
		ToolCallID: hfMsg.ToolCallID,
		Refusal:    hfMsg.Refusal,
	}

	// Convert content
	switch content := hfMsg.Content.(type) {
	case string:
		// String content
		if content != "" {
			msg.Content = []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: content,
				},
			}
		}
	case []interface{}:
		// Array content
		msg.Content = make([]model.ContentPart, 0, len(content))
		for _, part := range content {
			if partMap, ok := part.(map[string]interface{}); ok {
				contentPart := convertContentPartToModel(partMap)
				msg.Content = append(msg.Content, contentPart)
			}
		}
	case []ContentPart:
		// Typed array content
		msg.Content = make([]model.ContentPart, 0, len(content))
		for _, part := range content {
			msg.Content = append(msg.Content, convertContentPartStructToModel(part))
		}
	}

	// Convert tool calls
	if len(hfMsg.ToolCalls) > 0 {
		msg.ToolCalls = make([]model.ToolCall, 0, len(hfMsg.ToolCalls))
		for _, tc := range hfMsg.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: model.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	return msg
}

// convertContentPartToModel converts a content part map to a model.ContentPart.
func convertContentPartToModel(partMap map[string]interface{}) model.ContentPart {
	partType, _ := partMap["type"].(string)

	switch partType {
	case "text":
		text, _ := partMap["text"].(string)
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: text,
		}
	case "image_url":
		imageURL, _ := partMap["image_url"].(map[string]interface{})
		url, _ := imageURL["url"].(string)
		detail, _ := imageURL["detail"].(string)
		return model.ContentPart{
			Type: model.ContentTypeImageURL,
			ImageURL: &model.ImageURL{
				URL:    url,
				Detail: detail,
			},
		}
	default:
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: fmt.Sprintf("unsupported content type: %s", partType),
		}
	}
}

// convertContentPartStructToModel converts a ContentPart struct to a model.ContentPart.
func convertContentPartStructToModel(part ContentPart) model.ContentPart {
	switch part.Type {
	case "text":
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: part.Text,
		}
	case "image_url":
		if part.ImageURL != nil {
			return model.ContentPart{
				Type: model.ContentTypeImageURL,
				ImageURL: &model.ImageURL{
					URL:    part.ImageURL.URL,
					Detail: part.ImageURL.Detail,
				},
			}
		}
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: "image_url is nil",
		}
	default:
		return model.ContentPart{
			Type: model.ContentTypeText,
			Text: fmt.Sprintf("unsupported content type: %s", part.Type),
		}
	}
}
