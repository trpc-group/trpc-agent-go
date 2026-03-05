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
	// Enable ADK-compatible metadata keys (for example, "adk_type" instead
	// of "type").
	adkCompatibility   bool
	streamingEventType StreamingEventType
}

// setMetadata writes value under the standard key, and additionally under the
// ADK-prefixed key when ADK compatibility is enabled.
func (c *defaultEventToA2AMessage) setMetadata(m map[string]any, key string, value any) {
	m[key] = value
	if c.adkCompatibility {
		m[ia2a.GetADKMetadataKey(key)] = value
	}
}

// setPartTypeMetadata sets the DataPart type metadata.
func (c *defaultEventToA2AMessage) setPartTypeMetadata(dataPart *protocol.DataPart, typeValue string) {
	if dataPart.Metadata == nil {
		dataPart.Metadata = make(map[string]any)
	}
	c.setMetadata(dataPart.Metadata, ia2a.DataPartMetadataTypeKey, typeValue)
}

// setThoughtMetadata sets the thought metadata on a TextPart.
func (c *defaultEventToA2AMessage) setThoughtMetadata(textPart *protocol.TextPart) {
	if textPart.Metadata == nil {
		textPart.Metadata = make(map[string]any)
	}
	c.setMetadata(textPart.Metadata, ia2a.TextPartMetadataThoughtKey, true)
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

	var dataPart protocol.DataPart

	if evt.ContainsTag(event.CodeExecutionResultTag) {
		dataPart = protocol.NewDataPart(map[string]any{
			ia2a.CodeExecutionFieldOutput:  choice.Message.Content,
			ia2a.CodeExecutionFieldOutcome: "",
		})
		c.setPartTypeMetadata(&dataPart, ia2a.DataPartMetadataTypeCodeExecutionResult)
	} else if evt.ContainsTag(event.CodeExecutionTag) {
		dataPart = protocol.NewDataPart(map[string]any{
			ia2a.CodeExecutionFieldCode:     choice.Message.Content,
			ia2a.CodeExecutionFieldLanguage: "unknown",
		})
		c.setPartTypeMetadata(&dataPart, ia2a.DataPartMetadataTypeExecutableCode)
	} else {
		return nil, nil
	}

	parts := []protocol.Part{&dataPart}
	msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)

	msg.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: evt.Response.Object,
		ia2a.MessageMetadataTagKey:        evt.Tag,
		ia2a.MessageMetadataResponseIDKey: evt.Response.ID,
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

	var parts []protocol.Part

	// Add reasoning content as a separate TextPart with thought metadata
	// Following ADK pattern: thought content is stored in TextPart metadata
	if choice.Message.ReasoningContent != "" {
		reasoningPart := protocol.NewTextPart(choice.Message.ReasoningContent)
		c.setThoughtMetadata(&reasoningPart)
		parts = append(parts, reasoningPart)
	}

	// Add main content
	if choice.Message.Content != "" {
		parts = append(parts, protocol.NewTextPart(choice.Message.Content))
	}

	if len(parts) > 0 {
		msg := protocol.NewMessage(protocol.MessageRoleAgent, parts)
		msg.Metadata = map[string]any{
			ia2a.MessageMetadataObjectTypeKey: event.Response.Object,
			ia2a.MessageMetadataTagKey:        event.Tag,
			ia2a.MessageMetadataResponseIDKey: event.Response.ID,
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

// ConvertStreamingToA2AMessage converts an Agent event to an A2A protocol
// message for streaming.
//
// For streaming responses, it converts delta content, tool calls, and code
// execution events into A2A streaming results. The concrete A2A type can be
// configured via WithStreamingEventType.
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

func (c *defaultEventToA2AMessage) convertPartsToA2AStreamingResult(
	evt *event.Event,
	options EventToA2AStreamingOptions,
	parts []protocol.Part,
) protocol.StreamingMessageResult {
	if evt == nil || evt.Response == nil || len(parts) == 0 {
		return nil
	}

	if c.streamingEventType == StreamingEventTypeMessage {
		ctxID := options.CtxID
		taskID := options.TaskID
		msg := protocol.NewMessageWithContext(
			protocol.MessageRoleAgent,
			parts,
			&taskID,
			&ctxID,
		)
		if evt.Response.ID != "" {
			msg.MessageID = evt.Response.ID
		}
		msg.Metadata = map[string]any{
			ia2a.MessageMetadataObjectTypeKey: evt.Response.Object,
			ia2a.MessageMetadataTagKey:        evt.Tag,
			ia2a.MessageMetadataResponseIDKey: evt.Response.ID,
		}
		return &msg
	}

	taskArtifact := protocol.NewTaskArtifactUpdateEvent(
		options.TaskID,
		options.CtxID,
		protocol.Artifact{
			ArtifactID: evt.Response.ID,
			Parts:      parts,
		},
		false,
	)
	taskArtifact.Metadata = map[string]any{
		ia2a.MessageMetadataObjectTypeKey: evt.Response.Object,
		ia2a.MessageMetadataTagKey:        evt.Tag,
		ia2a.MessageMetadataResponseIDKey: evt.Response.ID,
	}
	return &taskArtifact
}

// convertDeltaContentToA2AStreamingMessage converts delta content to an A2A
// streaming result.
func (c *defaultEventToA2AMessage) convertDeltaContentToA2AStreamingMessage(
	ctx context.Context,
	event *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	choice := event.Response.Choices[0]

	var parts []protocol.Part

	// Add reasoning content as a separate TextPart with thought metadata
	if choice.Delta.ReasoningContent != "" {
		reasoningPart := protocol.NewTextPart(choice.Delta.ReasoningContent)
		c.setThoughtMetadata(&reasoningPart)
		parts = append(parts, reasoningPart)
	}

	// Add main delta content
	if choice.Delta.Content != "" {
		parts = append(parts, protocol.NewTextPart(choice.Delta.Content))
	}

	if len(parts) > 0 {
		return c.convertPartsToA2AStreamingResult(event, options, parts), nil
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

			dataPart := protocol.NewDataPart(toolCallData)
			c.setPartTypeMetadata(&dataPart, ia2a.DataPartMetadataTypeFunctionCall)
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

			dataPart := protocol.NewDataPart(toolResponseData)
			c.setPartTypeMetadata(&dataPart, ia2a.DataPartMetadataTypeFunctionResp)
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
		ia2a.MessageMetadataResponseIDKey: event.Response.ID,
	}
	return &msg, nil
}

// convertToolCallToA2AStreamingMessage converts tool call events to A2A streaming messages.
func (c *defaultEventToA2AMessage) convertToolCallToA2AStreamingMessage(
	event *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	// First get the message parts using the unary converter
	unaryResult, err := c.convertToolCallToA2AMessage(event)
	if err != nil || unaryResult == nil {
		return nil, err
	}

	msg, ok := unaryResult.(*protocol.Message)
	if !ok || len(msg.Parts) == 0 {
		return nil, nil
	}

	return c.convertPartsToA2AStreamingResult(
		event,
		options,
		msg.Parts,
	), nil
}

// convertCodeExecutionToA2AStreamingMessage converts code execution events to A2A streaming messages.
func (c *defaultEventToA2AMessage) convertCodeExecutionToA2AStreamingMessage(
	evt *event.Event,
	options EventToA2AStreamingOptions,
) (protocol.StreamingMessageResult, error) {
	// First get the message parts using the unary converter
	unaryResult, err := c.convertCodeExecutionToA2AMessage(evt)
	if err != nil || unaryResult == nil {
		return nil, err
	}

	msg, ok := unaryResult.(*protocol.Message)
	if !ok || len(msg.Parts) == 0 {
		return nil, nil
	}

	return c.convertPartsToA2AStreamingResult(
		evt,
		options,
		msg.Parts,
	), nil
}
