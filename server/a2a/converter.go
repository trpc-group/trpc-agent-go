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
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// A2AMessageToAgentMessage defines an interface for converting A2A protocol messages to Agent messages.
type A2AMessageToAgentMessage interface {
	// ConvertToAgentMessage converts an A2A protocol message to an Agent message.
	ConvertToAgentMessage(ctx context.Context, message protocol.Message) (*model.Message, error)
}

// EventToA2AUnaryOptions is the options for the EventToA2AMessage.
type EventToA2AUnaryOptions struct {
	CtxID string
}

// EventToA2AStreamingOptions is the options for the EventToA2AMessage.
type EventToA2AStreamingOptions struct {
	CtxID  string
	TaskID string
}

// EventToA2AMessage defines an interface for converting Agent events to A2A protocol messages.
type EventToA2AMessage interface {
	// ConvertToA2AMessage converts an Agent event to an A2A protocol message.
	ConvertToA2AMessage(
		ctx context.Context,
		event *event.Event,
		options EventToA2AUnaryOptions,
	) (protocol.UnaryMessageResult, error)

	// ConvertStreaming converts an Agent event to an A2A protocol message for streaming.
	ConvertStreamingToA2AMessage(
		ctx context.Context,
		event *event.Event,
		options EventToA2AStreamingOptions,
	) (protocol.StreamingMessageResult, error)
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
			var textPart *protocol.TextPart
			if p, ok := part.(*protocol.TextPart); ok {
				textPart = p
			} else if p, ok := part.(protocol.TextPart); ok {
				textPart = &p
			} else {
				continue
			}
			// Only add to content string, not to contentParts
			// to avoid duplication when converting back to A2A message
			content += textPart.Text
		case protocol.KindFile:
			var filePart *protocol.FilePart
			if f, ok := part.(*protocol.FilePart); ok {
				filePart = f
			} else if f, ok := part.(protocol.FilePart); ok {
				filePart = &f
			} else {
				continue
			}
			// Convert FilePart to model.ContentPart
			switch fileData := filePart.File.(type) {
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
			var dataPart *protocol.DataPart
			if d, ok := part.(*protocol.DataPart); ok {
				dataPart = d
			} else if d, ok := part.(protocol.DataPart); ok {
				dataPart = &d
			} else {
				continue
			}
			dataStr := fmt.Sprintf("%s", dataPart.Data)
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
type defaultEventToA2AMessage struct {
	adkCompatibility bool // Enable ADK-compatible metadata keys (e.g., "adk_type" instead of "type")
}

// getMetadataTypeKey returns the appropriate metadata type key based on ADK compatibility setting
func (c *defaultEventToA2AMessage) getMetadataTypeKey() string {
	if c.adkCompatibility {
		return ia2a.GetADKMetadataKey(ia2a.DataPartMetadataTypeKey)
	}
	return ia2a.DataPartMetadataTypeKey
}

// ConvertToA2AMessage converts an Agent event to an A2A protocol message.
// For non-streaming responses, it returns the full content including
// tool calls.
func (c *defaultEventToA2AMessage) ConvertToA2AMessage(
	ctx context.Context,
	event *event.Event,
	options EventToA2AUnaryOptions,
) (protocol.UnaryMessageResult, error) {
	if event.Response == nil {
		return nil, nil
	}

	if event.Response.Error != nil {
		return nil, fmt.Errorf(
			"A2A server received error event from agent, "+
				"event ID: %s, error: %v",
			event.ID,
			event.Response.Error,
		)
	}

	// Additional safety check for choices array bounds.
	if len(event.Response.Choices) == 0 {
		log.DebugfContext(
			ctx,
			"no choices in response, event: %v",
			event.ID,
		)
		return nil, nil
	}

	// Check if this is a tool call event.
	if isToolCallEvent(event) {
		return c.convertToolCallToA2AMessage(event)
	}

	// Check if this is a code execution event.
	if isCodeExecutionEvent(event) {
		return c.convertCodeExecutionToA2AMessage(event)
	}

	// Fallback to plain content conversion.
	return c.convertContentToA2AMessage(ctx, event)
}

// convertCodeExecutionToA2AMessage converts code execution events to A2A DataPart messages.
// This handles both code execution and code execution result events.
func (c *defaultEventToA2AMessage) convertCodeExecutionToA2AMessage(
	evt *event.Event,
) (protocol.UnaryMessageResult, error) {
	if len(evt.Response.Choices) == 0 {
		return nil, nil
	}

	choice := evt.Response.Choices[0]
	if choice.Message.Content == "" {
		return nil, nil
	}

	var parts []protocol.Part
	var dataPart protocol.DataPart
	metadataTypeKey := c.getMetadataTypeKey()

	if evt.ContainsTag(event.CodeExecutionResultTag) {
		// Code execution result event
		if c.adkCompatibility {
			dataPart = protocol.NewDataPart(map[string]any{
				ia2a.CodeExecutionFieldOutcome: "",
				ia2a.CodeExecutionFieldOutput:  choice.Message.Content,
			})
		} else {
			dataPart = protocol.NewDataPart(map[string]any{
				ia2a.CodeExecutionFieldContent: choice.Message.Content,
			})
		}
		// set metadata type key
		dataPart.Metadata = map[string]any{
			metadataTypeKey: ia2a.DataPartMetadataTypeCodeExecutionResult,
		}
	} else if evt.ContainsTag(event.CodeExecutionTag) {
		// Code execution event
		if c.adkCompatibility {
			dataPart = protocol.NewDataPart(map[string]any{
				ia2a.CodeExecutionFieldCode:     choice.Message.Content,
				ia2a.CodeExecutionFieldLanguage: "unknown",
			})
		} else {
			dataPart = protocol.NewDataPart(map[string]any{
				ia2a.CodeExecutionFieldContent: choice.Message.Content,
			})
		}
		// set metadata type key
		dataPart.Metadata = map[string]any{
			metadataTypeKey: ia2a.DataPartMetadataTypeExecutableCode,
		}
	}

	parts = append(parts, &dataPart)
	msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)

	// Pass Tag field to A2A metadata for client to restore event tag
	msg.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: evt.Response.Object,
		ia2a.MessageMetadataTagKey:        evt.Tag,
	}
	return &msg, nil
}

// convertContentToA2AMessage converts message content to A2A message.
// It creates a message with text parts containing the content.
func (c *defaultEventToA2AMessage) convertContentToA2AMessage(
	ctx context.Context,
	event *event.Event,
) (protocol.UnaryMessageResult, error) {
	choice := event.Response.Choices[0]
	if choice.Message.Content != "" {
		var parts []protocol.Part
		parts = append(parts, protocol.NewTextPart(choice.Message.Content))
		msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)
		msg.Metadata = map[string]any{
			ia2a.MessageMetadataObjectTypeKey: event.Response.Object,
			ia2a.MessageMetadataTagKey:        event.Tag,
		}
		return &msg, nil
	}

	log.DebugfContext(
		ctx,
		"content is empty, event: %v",
		event,
	)
	return nil, nil
}

// ConvertStreamingToA2AMessage converts an Agent event to an A2A protocol message for streaming.
// For streaming responses, it returns delta content as task artifact updates and converts tool calls.
func (c *defaultEventToA2AMessage) ConvertStreamingToA2AMessage(
	ctx context.Context,
	evt *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	if evt.Response == nil {
		return nil, nil
	}

	if evt.Response.Error != nil {
		return nil, fmt.Errorf(
			"A2A server received error event from agent, "+
				"event ID: %s, error: %v",
			evt.ID,
			evt.Response.Error,
		)
	}

	// Additional safety check for choices array bounds
	if len(evt.Response.Choices) == 0 {
		log.DebugfContext(
			ctx,
			"no choices in response, event: %v",
			evt.ID,
		)
		return nil, nil
	}

	// Check if this is a tool call event
	if isToolCallEvent(evt) {
		return c.convertToolCallToA2AStreamingMessage(evt, options)
	}

	if isCodeExecutionEvent(evt) {
		return c.convertCodeExecutionToA2AStreamingMessage(evt, options)
	}

	return c.convertDeltaContentToA2AStreamingMessage(ctx, evt, options)
}

// convertDeltaContentToA2AStreamingMessage converts delta content to A2A streaming message.
// It creates a task artifact update event for incremental content updates.
func (c *defaultEventToA2AMessage) convertDeltaContentToA2AStreamingMessage(
	ctx context.Context,
	event *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	choice := event.Response.Choices[0]
	// Use delta content for streaming updates
	if choice.Delta.Content != "" {
		parts := []protocol.Part{protocol.NewTextPart(choice.Delta.Content)}
		// Send as task artifact update (not status update) for incremental content
		// This follows ADK pattern: artifacts for content, status for state changes
		taskArtifact := protocol.NewTaskArtifactUpdateEvent(
			options.TaskID,
			options.CtxID,
			protocol.Artifact{
				ArtifactID: event.Response.ID,
				Parts:      parts,
			},
			false,
		)
		taskArtifact.Metadata = map[string]any{
			ia2a.MessageMetadataObjectTypeKey: event.Response.Object,
			ia2a.MessageMetadataTagKey:        event.Tag,
		}
		return &taskArtifact, nil
	}

	log.DebugfContext(
		ctx,
		"delta content is empty, event: %v",
		event,
	)
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

func isCodeExecutionEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}

	// Check if the event object type is code execution related
	return evt.Response.Object == model.ObjectTypePostprocessingCodeExecution
}

// convertToolCallToA2AMessage converts tool call events to A2A DataPart messages.
// This handles both tool call requests and tool call responses.
func (c *defaultEventToA2AMessage) convertToolCallToA2AMessage(
	event *event.Event,
) (protocol.UnaryMessageResult, error) {
	if len(event.Response.Choices) == 0 {
		return nil, nil
	}

	var parts []protocol.Part

	// Handle tool call requests (assistant making function calls)
	// OpenAI returns tool calls in a single choice with multiple ToolCalls
	choice := event.Response.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		for _, toolCall := range choice.Message.ToolCalls {
			// Convert ToolCall to map for DataPart
			toolCallData := map[string]any{
				ia2a.ToolCallFieldID:   toolCall.ID,
				ia2a.ToolCallFieldType: toolCall.Type,
				ia2a.ToolCallFieldName: toolCall.Function.Name,
				ia2a.ToolCallFieldArgs: string(toolCall.Function.Arguments),
			}

			// Create DataPart with metadata indicating this is a function call
			dataPart := protocol.NewDataPart(toolCallData)

			dataPart.Metadata = map[string]any{
				c.getMetadataTypeKey(): ia2a.DataPartMetadataTypeFunctionCall,
			}
			parts = append(parts, dataPart)
		}
	}

	// Handle tool call responses (tool returning results)
	// OpenAI returns each tool response in a separate choice
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool || choice.Message.ToolID != "" {
			// Convert tool response to DataPart
			toolResponseData := map[string]any{
				ia2a.ToolCallFieldName: choice.Message.ToolName,
				ia2a.ToolCallFieldID:   choice.Message.ToolID,
			}

			// Pass content as-is without parsing
			// Client will receive the raw response string and display it directly
			if choice.Message.Content != "" {
				toolResponseData[ia2a.ToolCallFieldResponse] = choice.Message.Content
			}

			// Create DataPart with metadata indicating this is a function response
			dataPart := protocol.NewDataPart(toolResponseData)

			dataPart.Metadata = map[string]any{
				c.getMetadataTypeKey(): ia2a.DataPartMetadataTypeFunctionResp,
			}
			parts = append(parts, dataPart)
		}
	}

	if len(parts) == 0 {
		return nil, nil
	}

	msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)
	msg.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: event.Response.Object,
		ia2a.MessageMetadataTagKey:        event.Tag,
	}
	return &msg, nil
}

// convertToolCallToA2AStreamingMessage converts tool call events to A2A streaming messages.
// For streaming mode, tool calls are sent as TaskArtifactUpdateEvent.
func (c *defaultEventToA2AMessage) convertToolCallToA2AStreamingMessage(
	event *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	// For streaming, we convert tool calls to task artifact updates
	// First get the message parts using the unary converter
	unaryResult, err := c.convertToolCallToA2AMessage(event)
	if err != nil || unaryResult == nil {
		return nil, err
	}

	msg, ok := unaryResult.(*protocol.Message)
	if !ok || len(msg.Parts) == 0 {
		return nil, nil
	}

	// Create a task artifact update with the tool call parts
	taskArtifact := protocol.NewTaskArtifactUpdateEvent(
		options.TaskID,
		options.CtxID,
		protocol.Artifact{
			ArtifactID: event.Response.ID,
			Parts:      msg.Parts,
		},
		false, // append=false for tool calls (complete events, not incremental)
	)
	taskArtifact.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: event.Response.Object,
		ia2a.MessageMetadataTagKey:        event.Tag,
	}
	return &taskArtifact, nil
}

// convertCodeExecutionToA2AStreamingMessage converts code execution events to A2A streaming messages.
// For streaming mode, code execution events are sent as TaskArtifactUpdateEvent.
func (c *defaultEventToA2AMessage) convertCodeExecutionToA2AStreamingMessage(
	evt *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	// For streaming, we convert code execution to task artifact updates
	// First get the message parts using the unary converter
	unaryResult, err := c.convertCodeExecutionToA2AMessage(evt)
	if err != nil || unaryResult == nil {
		return nil, err
	}

	msg, ok := unaryResult.(*protocol.Message)
	if !ok || len(msg.Parts) == 0 {
		return nil, nil
	}

	// Create a task artifact update with the code execution parts
	taskArtifact := protocol.NewTaskArtifactUpdateEvent(
		options.TaskID,
		options.CtxID,
		protocol.Artifact{
			ArtifactID: evt.Response.ID,
			Parts:      msg.Parts,
		},
		false, // append=false for code execution (complete events, not incremental)
	)
	taskArtifact.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: evt.Response.Object,
		ia2a.MessageMetadataTagKey:        evt.Tag,
	}
	return &taskArtifact, nil
}
