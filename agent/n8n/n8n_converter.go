//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package n8n

import (
	"context"
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// AuthType defines the type of authentication to use for n8n webhook requests.
type AuthType string

const (
	// AuthNone indicates no authentication.
	AuthNone AuthType = "none"
	// AuthBasic indicates HTTP basic authentication.
	AuthBasic AuthType = "basic"
	// AuthHeader indicates custom header authentication.
	AuthHeader AuthType = "header"
)

// AuthConfig holds the authentication configuration for n8n webhook requests.
// For AuthBasic, set Username and Password.
// For AuthHeader, set HeaderName (defaults to "Authorization") and HeaderValue.
type AuthConfig struct {
	Username    string
	Password    string
	HeaderName  string
	HeaderValue string
}

// StreamingRespHandler processes streaming response chunks.
// The returned string is appended to the aggregated content used in the final event.
type StreamingRespHandler func(resp *model.Response) (string, error)

// RequestConverter defines an interface for converting invocations to n8n request payloads.
type RequestConverter interface {
	// ConvertToN8nRequest converts agent invocation to an n8n webhook request body.
	ConvertToN8nRequest(
		ctx context.Context,
		invocation *agent.Invocation,
	) (map[string]any, error)
}

// ResponseConverter defines an interface for converting n8n responses to events.
type ResponseConverter interface {
	// ConvertToEvent converts a non-streaming n8n response body to an Event.
	ConvertToEvent(
		body []byte,
		agentName string,
		invocation *agent.Invocation,
	) *event.Event

	// ConvertStreamingToEvent converts a streaming n8n response chunk to an Event.
	ConvertStreamingToEvent(
		data []byte,
		agentName string,
		invocation *agent.Invocation,
	) *event.Event
}

// defaultRequestConverter is the default implementation of RequestConverter.
type defaultRequestConverter struct{}

func (d *defaultRequestConverter) ConvertToN8nRequest(
	ctx context.Context,
	invocation *agent.Invocation,
) (map[string]any, error) {
	req := map[string]any{
		"query": invocation.Message.Content,
	}

	user := "anonymous"
	if invocation.Session != nil && invocation.Session.UserID != "" {
		user = invocation.Session.UserID
	}
	req["user"] = user

	// NOTE: Only the last image/file is kept when multiple are present,
	// because n8n webhook uses a flat key-value format that doesn't support arrays.
	// Users needing multi-file support should implement a custom RequestConverter.
	for _, contentPart := range invocation.Message.ContentParts {
		switch contentPart.Type {
		case model.ContentTypeText:
			if contentPart.Text != nil {
				if query, ok := req["query"].(string); ok && query != "" {
					req["query"] = query + "\n" + *contentPart.Text
				} else {
					req["query"] = *contentPart.Text
				}
			}
		case model.ContentTypeImage:
			if contentPart.Image != nil && contentPart.Image.URL != "" {
				req["image_url"] = contentPart.Image.URL
			}
		case model.ContentTypeFile:
			if contentPart.File != nil && contentPart.File.Name != "" {
				req["file_name"] = contentPart.File.Name
			}
		}
	}

	return req, nil
}

// defaultResponseConverter is the default implementation of ResponseConverter.
type defaultResponseConverter struct{}

func (d *defaultResponseConverter) ConvertToEvent(
	body []byte,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	content := extractContentFromBody(body)
	now := time.Now()

	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content,
	}

	return event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Choices:   []model.Choice{{Message: message, Delta: message}},
			Timestamp: now,
			Created:   now.Unix(),
			IsPartial: false,
			Done:      true,
		}),
	)
}

func (d *defaultResponseConverter) ConvertStreamingToEvent(
	data []byte,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	content := extractContentFromBody(data)
	if content == "" {
		return nil
	}

	now := time.Now()
	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content,
	}

	return event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Object:    model.ObjectTypeChatCompletionChunk,
			Choices:   []model.Choice{{Delta: message}},
			Timestamp: now,
			Created:   now.Unix(),
			IsPartial: true,
			Done:      false,
		}),
		event.WithObject(model.ObjectTypeChatCompletionChunk),
	)
}

// responseFieldPriority defines the order in which fields are checked
// when extracting content from an n8n webhook JSON response.
var responseFieldPriority = []string{"output", "answer", "text", "result"}

// extractContentFromBody tries to extract content from a JSON response body.
// It looks for known fields in priority order (output > answer > text > result).
// If the body is not valid JSON or no known field is found, it returns the raw body as a string.
func extractContentFromBody(body []byte) string {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return string(body)
	}

	for _, key := range responseFieldPriority {
		if val, ok := data[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}

	return string(body)
}
