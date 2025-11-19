//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	roleSystem                = "system"
	roleUser                  = "user"
	roleAssistant             = "assistant"
	roleTool                  = "tool"
	contentTypeText           = "text"
	contentTypeImageURL       = "image_url"
	objectChatCompletion      = "chat.completion"
	objectChatCompletionChunk = "chat.completion.chunk"
	finishReasonStop          = "stop"
	errorTypeInvalidRequest   = "invalid_request_error"
	errorTypeInternal         = "internal_error"
	responseIDPrefix          = "chatcmpl-"
)

// openAIRequest represents an OpenAI chat completion request.
// Note: This is similar to github.com/openai/openai-go.ChatCompletionNewParams,
// but we define our own type because the SDK uses union types (e.g., Messages
// is ChatCompletionMessageParamUnion) that don't work well for direct HTTP
// JSON unmarshal. Our type uses simple types for better HTTP compatibility.
type openAIRequest struct {
	Model            string          `json:"model"`
	Messages         []openAIMessage `json:"messages"`
	Temperature      *float64        `json:"temperature,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	Tools            []openAITool    `json:"tools,omitempty"`
	ToolChoice       any             `json:"tool_choice,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	User             string          `json:"user,omitempty"`
}

// openAIMessage represents a message in OpenAI format.
// Note: Similar to github.com/openai/openai-go.ChatCompletionMessageParamUnion,
// but simplified for HTTP JSON serialization.
type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"` // string or []contentPart
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

// openAITool represents a tool definition.
// Note: Similar to github.com/openai/openai-go.ChatCompletionToolParam.
type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

// openAIFunction represents a function definition.
// Note: Similar to github.com/openai/openai-go.ChatCompletionToolFunction.
type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// openAIToolCall represents a tool call in OpenAI format.
// Note: Similar to github.com/openai/openai-go.ChatCompletionMessageToolCall.
type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

// openAIToolCallFunction represents a function call.
// Note: Similar to github.com/openai/openai-go.ChatCompletionMessageToolCallFunction.
type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// contentPart represents a content part (for multimodal).
type contentPart struct {
	Type     string   `json:"type"`
	Text     string   `json:"text,omitempty"`
	ImageURL imageURL `json:"image_url,omitempty"`
}

// imageURL represents an image URL.
type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// openAIResponse represents a non-streaming OpenAI response.
// Note: This is similar to github.com/openai/openai-go.ChatCompletion,
// but we define our own type for HTTP JSON serialization compatibility.
// The SDK's ChatCompletion uses constant types and union types that don't
// work well for direct HTTP JSON marshal/unmarshal.
type openAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

// openAIChoice represents a choice in the response.
// Note: Similar to github.com/openai/openai-go.ChatCompletionChoice,
// but with optional FinishReason for better compatibility.
type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason *string       `json:"finish_reason,omitempty"`
}

// openAIUsage represents token usage.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openAIChunk represents a streaming chunk.
// Note: This is similar to github.com/openai/openai-go.ChatCompletionChunk,
// but we define our own type for HTTP JSON serialization compatibility.
type openAIChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []openAIChunkChoice `json:"choices"`
}

// openAIChunkChoice represents a choice in a streaming chunk.
// Note: Similar to github.com/openai/openai-go.ChatCompletionChunkChoice,
// but with optional FinishReason for better compatibility.
type openAIChunkChoice struct {
	Index        int           `json:"index"`
	Delta        openAIMessage `json:"delta"`
	FinishReason *string       `json:"finish_reason,omitempty"`
}

// converter converts between OpenAI format and trpc-agent-go format.
type converter struct {
	modelName string
}

// newConverter creates a new converter.
func newConverter(modelName string) *converter {
	return &converter{
		modelName: modelName,
	}
}

// convertRequest converts an OpenAI request to trpc-agent-go messages.
func (c *converter) convertRequest(ctx context.Context, req *openAIRequest) ([]model.Message, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages cannot be empty")
	}
	messages := make([]model.Message, 0, len(req.Messages))
	for _, msg := range req.Messages {
		converted, err := c.convertMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("convert message: %w", err)
		}
		messages = append(messages, *converted)
	}
	return messages, nil
}

// convertMessage converts a single OpenAI message to model.Message.
func (c *converter) convertMessage(msg openAIMessage) (*model.Message, error) {
	role, err := c.convertRole(msg.Role)
	if err != nil {
		return nil, err
	}
	result := &model.Message{
		Role: role,
	}
	// Handle content.
	if msg.Content != nil {
		switch v := msg.Content.(type) {
		case string:
			result.Content = v
		case []any:
			// Multimodal content.
			for _, part := range v {
				partMap, ok := part.(map[string]any)
				if !ok {
					continue
				}
				partType, _ := partMap["type"].(string)
				switch partType {
				case contentTypeText:
					if text, ok := partMap["text"].(string); ok {
						if result.Content == "" {
							result.Content = text
						} else {
							result.Content += "\n" + text
						}
					}
				case contentTypeImageURL:
					if imageURL, ok := partMap["image_url"].(map[string]any); ok {
						url, _ := imageURL["url"].(string)
						detail, _ := imageURL["detail"].(string)
						if url != "" {
							result.AddImageURL(url, detail)
						}
					}
				}
			}
		}
	}
	// Handle tool calls.
	if len(msg.ToolCalls) > 0 {
		result.ToolCalls = make([]model.ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			argsBytes := []byte(tc.Function.Arguments)
			result.ToolCalls = append(result.ToolCalls, model.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: model.FunctionDefinitionParam{
					Name:      tc.Function.Name,
					Arguments: argsBytes,
				},
			})
		}
	}
	// Handle tool response.
	if msg.ToolCallID != "" {
		result.ToolID = msg.ToolCallID
		result.ToolName = msg.Name
		if content, ok := msg.Content.(string); ok {
			result.Content = content
		}
	}
	return result, nil
}

// convertRole converts OpenAI role to model.Role.
func (c *converter) convertRole(role string) (model.Role, error) {
	switch role {
	case roleSystem:
		return model.RoleSystem, nil
	case roleUser:
		return model.RoleUser, nil
	case roleAssistant:
		return model.RoleAssistant, nil
	case roleTool:
		return model.RoleTool, nil
	default:
		return "", fmt.Errorf("invalid role: %s", role)
	}
}

// convertEventToResponse converts an event to OpenAI response format.
func (c *converter) convertEventToResponse(ctx context.Context, evt *event.Event, isStreaming bool) (any, error) {
	if evt == nil || evt.Response == nil {
		return nil, nil
	}
	if evt.Response.Error != nil {
		return nil, fmt.Errorf("API error: %s", evt.Response.Error.Message)
	}
	if isStreaming {
		return c.convertToChunk(evt)
	}
	return c.convertToResponse(evt)
}

// convertToResponse converts an event to a non-streaming response.
func (c *converter) convertToResponse(evt *event.Event) (*openAIResponse, error) {
	if len(evt.Response.Choices) == 0 {
		return nil, nil
	}
	choice := evt.Response.Choices[0]
	msg, err := c.convertModelMessageToOpenAI(choice.Message)
	if err != nil {
		return nil, err
	}
	finishReason := finishReasonStop
	if choice.FinishReason != nil {
		finishReason = *choice.FinishReason
	}
	response := &openAIResponse{
		ID:      evt.ID,
		Object:  objectChatCompletion,
		Created: evt.Response.Created,
		Model:   c.modelName,
		Choices: []openAIChoice{
			{
				Index:        0,
				Message:      *msg,
				FinishReason: &finishReason,
			},
		},
	}
	if evt.Usage != nil {
		response.Usage = &openAIUsage{
			PromptTokens:     evt.Usage.PromptTokens,
			CompletionTokens: evt.Usage.CompletionTokens,
			TotalTokens:      evt.Usage.TotalTokens,
		}
	}
	return response, nil
}

// convertToChunk converts an event to a streaming chunk.
func (c *converter) convertToChunk(evt *event.Event) (*openAIChunk, error) {
	if len(evt.Response.Choices) == 0 {
		return nil, nil
	}
	choice := evt.Response.Choices[0]
	delta, err := c.convertModelMessageToOpenAI(choice.Delta)
	if err != nil {
		return nil, err
	}
	// Skip empty deltas unless there's a finish reason.
	contentStr := ""
	if delta.Content != nil {
		if str, ok := delta.Content.(string); ok {
			contentStr = str
		}
	}
	if contentStr == "" && len(delta.ToolCalls) == 0 && delta.Role == "" {
		if choice.FinishReason == nil {
			return nil, nil
		}
	}
	var finishReason *string
	if choice.FinishReason != nil {
		finishReason = choice.FinishReason
	}
	chunk := &openAIChunk{
		ID:      evt.ID,
		Object:  objectChatCompletionChunk,
		Created: evt.Response.Created,
		Model:   c.modelName,
		Choices: []openAIChunkChoice{
			{
				Index:        0,
				Delta:        *delta,
				FinishReason: finishReason,
			},
		},
	}
	return chunk, nil
}

// convertModelMessageToOpenAI converts model.Message to openAIMessage.
func (c *converter) convertModelMessageToOpenAI(msg model.Message) (*openAIMessage, error) {
	result := &openAIMessage{
		Role: string(msg.Role),
	}
	if msg.Content != "" {
		result.Content = msg.Content
	}
	if len(msg.ToolCalls) > 0 {
		result.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: openAIToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: string(tc.Function.Arguments),
				},
			})
		}
	}
	if msg.ToolID != "" {
		result.ToolCallID = msg.ToolID
		result.Name = msg.ToolName
		result.Role = roleTool
	}
	return result, nil
}

// aggregateStreamingEvents aggregates streaming events into a final response.
func (c *converter) aggregateStreamingEvents(events []*event.Event) (*openAIResponse, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("no events to aggregate")
	}
	// Find the final event with usage.
	var finalEvent *event.Event
	var allContent strings.Builder
	var toolCalls []model.ToolCall
	for _, evt := range events {
		if evt.Response != nil && evt.Usage != nil {
			finalEvent = evt
		}
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				allContent.WriteString(choice.Delta.Content)
			}
			// Handle non-streaming message content (for compatibility).
			if choice.Message.Content != "" && allContent.Len() == 0 {
				allContent.WriteString(choice.Message.Content)
			}
			// Handle streaming delta tool calls.
			if len(choice.Delta.ToolCalls) > 0 {
				toolCalls = append(toolCalls, choice.Delta.ToolCalls...)
			}
			// Handle non-streaming message tool calls (for compatibility).
			if len(choice.Message.ToolCalls) > 0 && len(toolCalls) == 0 {
				toolCalls = append(toolCalls, choice.Message.ToolCalls...)
			}
		}
	}
	if finalEvent == nil {
		// Use the last event if no event with usage found.
		finalEvent = events[len(events)-1]
	}
	// Build the aggregated message.
	msg := model.Message{
		Role:      model.RoleAssistant,
		Content:   allContent.String(),
		ToolCalls: toolCalls,
	}
	openAIMsg, err := c.convertModelMessageToOpenAI(msg)
	if err != nil {
		return nil, err
	}
	finishReason := finishReasonStop
	if finalEvent.Response != nil && len(finalEvent.Response.Choices) > 0 {
		if finalEvent.Response.Choices[0].FinishReason != nil {
			finishReason = *finalEvent.Response.Choices[0].FinishReason
		}
	}
	response := &openAIResponse{
		ID:      finalEvent.ID,
		Object:  objectChatCompletion,
		Created: time.Now().Unix(),
		Model:   c.modelName,
		Choices: []openAIChoice{
			{
				Index:        0,
				Message:      *openAIMsg,
				FinishReason: &finishReason,
			},
		},
	}
	if finalEvent.Usage != nil {
		response.Usage = &openAIUsage{
			PromptTokens:     finalEvent.Usage.PromptTokens,
			CompletionTokens: finalEvent.Usage.CompletionTokens,
			TotalTokens:      finalEvent.Usage.TotalTokens,
		}
	}
	return response, nil
}

// generateResponseID generates a unique response ID.
func generateResponseID() string {
	return responseIDPrefix + uuid.New().String()
}

// openAIError represents an OpenAI error response.
type openAIError struct {
	Error openAIErrorDetail `json:"error"`
}

// openAIErrorDetail represents error details.
type openAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// formatError formats an error as OpenAI error response.
func formatError(err error, errorType string) *openAIError {
	if errorType == "" {
		errorType = errorTypeInvalidRequest
	}
	return &openAIError{
		Error: openAIErrorDetail{
			Message: err.Error(),
			Type:    errorType,
		},
	}
}

// parseFloat64 parses a float64 from interface{}.
func parseFloat64(v any) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case float64:
		return &val, nil
	case float32:
		f := float64(val)
		return &f, nil
	case int:
		f := float64(val)
		return &f, nil
	case int64:
		f := float64(val)
		return &f, nil
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, err
		}
		return &f, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// parseInt parses an int from interface{}.
func parseInt(v any) (*int, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case int:
		return &val, nil
	case int64:
		i := int(val)
		return &i, nil
	case float64:
		i := int(val)
		return &i, nil
	case string:
		i, err := strconv.Atoi(val)
		if err != nil {
			return nil, err
		}
		return &i, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to int", v)
	}
}
