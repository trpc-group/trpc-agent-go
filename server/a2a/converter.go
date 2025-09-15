//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// A2AMessageToAgentMessage defines an interface for converting A2A protocol messages to Agent messages.
type A2AMessageToAgentMessage interface {
	// ConvertToAgentMessage converts an A2A protocol message to an Agent message.
	ConvertToAgentMessage(ctx context.Context, message protocol.Message) (*model.Message, error)
}

// EventToA2AMessage defines an interface for converting Agent events to A2A protocol messages.
type EventToA2AMessage interface {
	// ConvertToA2AMessage converts an Agent event to an A2A protocol message.
	ConvertToA2AMessage(ctx context.Context, event *event.Event) (*protocol.Message, error)

	// ConvertStreaming converts an Agent event to an A2A protocol message for streaming.
	ConvertStreamingToA2AMessage(ctx context.Context, event *event.Event) (*protocol.Message, error)
}

// defaultA2AMessageToAgentMessage is the default implementation of A2AMessageToAgentMessageConverter.
type defaultA2AMessageToAgentMessage struct{}

// ConvertToAgentMessage converts an A2A protocol message to an Agent message.
func (c *defaultA2AMessageToAgentMessage) ConvertToAgentMessage(
	ctx context.Context,
	message protocol.Message,
) (*model.Message, error) {
	var content string
	var contentParts []model.ContentPart

	// Process all parts in the A2A message
	for _, part := range message.Parts {
		switch part.GetKind() {
		case protocol.KindText:
			p, ok := part.(*protocol.TextPart)
			if !ok {
				continue
			}
			content += p.Text
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentTypeText,
				Text: &p.Text,
			})
		case protocol.KindFile:
			f, ok := part.(*protocol.FilePart)
			if !ok {
				continue
			}
			// Convert FilePart to model.ContentPart
			switch fileData := f.File.(type) {
			case *protocol.FileWithBytes:
				// Handle file with bytes data
				fileName := ""
				mimeType := ""
				if fileData.Name != nil {
					fileName = *fileData.Name
				}
				if fileData.MimeType != nil {
					mimeType = *fileData.MimeType
				}
				contentParts = append(contentParts, model.ContentPart{
					Type: model.ContentTypeFile,
					File: &model.File{
						Name:     fileName,
						Data:     []byte(fileData.Bytes),
						MimeType: mimeType,
					},
				})
			case *protocol.FileWithURI:
				// Handle file with URI
				fileName := ""
				mimeType := ""
				if fileData.Name != nil {
					fileName = *fileData.Name
				}
				if fileData.MimeType != nil {
					mimeType = *fileData.MimeType
				}
				contentParts = append(contentParts, model.ContentPart{
					Type: model.ContentTypeFile,
					File: &model.File{
						Name:     fileName,
						FileID:   fileData.URI,
						MimeType: mimeType,
					},
				})
			}
		case protocol.KindData:
			d, ok := part.(*protocol.DataPart)
			if !ok {
				continue
			}
			// Convert DataPart to text
			dataStr := fmt.Sprintf("%s", d.Data)
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentTypeText,
				Text: &dataStr,
			})
		}
	}

	// Create message with both content and content parts
	msg := model.Message{
		Role:         model.RoleUser,
		Content:      content,
		ContentParts: contentParts,
	}

	return &msg, nil
}

// defaultEventToA2AMessage is the default implementation of EventToA2AMessageConverter.
type defaultEventToA2AMessage struct{}

// ConvertToA2AMessage converts an Agent event to an A2A protocol message.
// For non-streaming responses, it returns the full content and filters out toolcall events.
func (c *defaultEventToA2AMessage) ConvertToA2AMessage(
	ctx context.Context,
	event *event.Event,
) (*protocol.Message, error) {
	if event.Response == nil {
		return nil, nil
	}

	if event.Response.Error != nil {
		return nil, fmt.Errorf("A2A server received error event from agent, event: %v", event.ID)
	}

	// Filter out toolcall events for non-streaming responses
	if isToolCallEvent(event) || len(event.Response.Choices) == 0 {
		return nil, nil
	}

	if event.Response.Choices[0].Message.Content != "" {
		var parts []protocol.Part
		parts = append(parts, protocol.NewTextPart(event.Response.Choices[0].Message.Content))
		msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)
		return &msg, nil
	}

	log.Debugf("content is empty, event: %v", event)
	return nil, nil
}

// ConvertStreamingToA2AMessage converts an Agent event to an A2A protocol message for streaming.
// For streaming responses, it returns delta content and filters out tool call events.
func (c *defaultEventToA2AMessage) ConvertStreamingToA2AMessage(
	ctx context.Context,
	event *event.Event,
) (*protocol.Message, error) {
	if event.Response == nil {
		return nil, nil
	}

	if event.Response.Error != nil {
		return nil, fmt.Errorf("A2A server received error event from agent, event: %v", event.ID)
	}

	// Filter out tool call events for streaming responses
	if isToolCallEvent(event) || len(event.Response.Choices) == 0 {
		return nil, nil
	}

	var parts []protocol.Part
	// Use delta choice.Message.Content for non-streaming events mixed in streaming
	if event.Response.Choices[0].Delta.Content != "" {
		parts = append(parts, protocol.NewTextPart(event.Response.Choices[0].Delta.Content))
		msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)
		return &msg, nil
	}

	log.Debugf("delta content is empty, event: %v", event)
	return nil, nil
}

// isToolCallEvent checks if an event is related to tool calls.
// It filters out both tool call requests and tool call responses.
func isToolCallEvent(event *event.Event) bool {
	if event == nil || event.Response == nil || len(event.Response.Choices) == 0 {
		return false
	}

	// Check if this event contains tool calls in the response choices
	for _, choice := range event.Response.Choices {
		// Check for tool call requests (assistant making tool calls)
		if len(choice.Message.ToolCalls) > 0 {
			return true
		}
		// Check for tool call responses (tool returning results)
		if choice.Message.Role == model.RoleTool {
			return true
		}
		// Check for tool ID in the message (indicates tool response)
		if choice.Message.ToolID != "" {
			return true
		}
	}

	return false
}
