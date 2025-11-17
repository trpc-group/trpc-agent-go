//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"encoding/base64"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// A2A DataPart metadata keys and values for tool call transmission
	dataPartMetadataTypeKey          = "type"
	dataPartMetadataTypeFunctionCall = "function_call"
	dataPartMetadataTypeFunctionResp = "function_response"

	// Tool call data field keys
	toolCallFieldID       = "id"
	toolCallFieldType     = "type"
	toolCallFieldName     = "name"
	toolCallFieldArgs     = "args"
	toolCallFieldResponse = "response"
)

// A2AEventConverter defines an interface for converting A2A protocol types to Event.
type A2AEventConverter interface {
	// ConvertToEvent converts an A2A protocol type to an Event.

	ConvertToEvent(result protocol.MessageResult, agentName string, invocation *agent.Invocation) (*event.Event, error)

	// ConvertStreamingToEvent converts a streaming A2A protocol type to an Event.
	ConvertStreamingToEvent(result protocol.StreamingMessageEvent, agentName string, invocation *agent.Invocation) (*event.Event, error)
}

// InvocationA2AConverter defines an interface for converting invocations to A2A protocol messages.
type InvocationA2AConverter interface {
	// ConvertToA2AMessage converts an invocation to an A2A protocol Message.
	ConvertToA2AMessage(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error)
}

type defaultA2AEventConverter struct {
}

func (d *defaultA2AEventConverter) ConvertToEvent(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) (*event.Event, error) {
	if result.Result == nil {
		return event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		), nil
	}

	var responseMsg *protocol.Message
	var event *event.Event
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
		event = d.buildRespEvent(false, responseMsg, agentName, invocation)
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
		event = d.buildRespEvent(false, responseMsg, agentName, invocation)
	default:
		// Handle unknown response types
		responseMsg = &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{protocol.NewTextPart("Received unknown response type")},
		}
		event = d.buildRespEvent(false, responseMsg, agentName, invocation)
	}
	event.Done = true
	event.IsPartial = false
	return event, nil
}

func (d *defaultA2AEventConverter) ConvertStreamingToEvent(
	result protocol.StreamingMessageEvent,
	agentName string,
	invocation *agent.Invocation,
) (*event.Event, error) {
	if result.Result == nil {
		return event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		), nil
	}

	var event *event.Event
	var responseMsg *protocol.Message
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.TaskStatusUpdateEvent:
		responseMsg = convertTaskStatusToMessage(v)
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.TaskArtifactUpdateEvent:
		responseMsg = convertTaskArtifactToMessage(v)
		event = d.buildRespEvent(true, responseMsg, agentName, invocation)
	default:
		log.Infof("unexpected event type: %T", result.Result)
	}
	return event, nil
}

type defaultEventA2AConverter struct {
}

// ConvertToA2AMessage converts an event to an A2A protocol Message.
func (d *defaultEventA2AConverter) ConvertToA2AMessage(
	isStream bool,
	agentName string,
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	var parts []protocol.Part

	// Convert invocation.Message.Content (text) to TextPart
	if invocation.Message.Content != "" {
		parts = append(parts, protocol.NewTextPart(invocation.Message.Content))
	}

	// Convert invocation.Message.ContentParts to A2A Parts
	for _, contentPart := range invocation.Message.ContentParts {
		switch contentPart.Type {
		case model.ContentTypeText:
			if contentPart.Text != nil {
				parts = append(parts, protocol.NewTextPart(*contentPart.Text))
			}
		case model.ContentTypeImage:
			if contentPart.Image != nil {
				if len(contentPart.Image.Data) > 0 {
					// Handle inline image data
					parts = append(parts, protocol.NewFilePartWithBytes(
						"image",
						contentPart.Image.Format,
						base64.StdEncoding.EncodeToString(contentPart.Image.Data),
					))
				} else if contentPart.Image.URL != "" {
					// Handle image URL
					parts = append(parts, protocol.NewFilePartWithURI(
						"image",
						contentPart.Image.Format,
						contentPart.Image.URL,
					))
				}
			}
		case model.ContentTypeAudio:
			if contentPart.Audio != nil && contentPart.Audio.Data != nil {
				// Handle audio data as file with bytes
				parts = append(parts, protocol.NewFilePartWithBytes(
					"audio",
					contentPart.Audio.Format,
					base64.StdEncoding.EncodeToString(contentPart.Audio.Data),
				))
			}
		case model.ContentTypeFile:
			if contentPart.File != nil {
				if len(contentPart.File.Data) > 0 {
					fileName := contentPart.File.Name
					if fileName == "" {
						fileName = "file"
					}
					parts = append(parts, protocol.NewFilePartWithBytes(
						fileName,
						contentPart.File.MimeType,
						base64.StdEncoding.EncodeToString(contentPart.File.Data),
					))
				}
			}
		}
	}

	// If no content, create an empty text part to ensure message is not empty
	if len(parts) == 0 {
		parts = append(parts, protocol.NewTextPart(""))
	}
	message := protocol.NewMessage(protocol.MessageRoleUser, parts)
	sess := invocation.Session
	if sess != nil {
		message.ContextID = &sess.ID
	}
	return &message, nil
}

// buildRespEvent converts A2A response to tRPC event
func (d *defaultA2AEventConverter) buildRespEvent(
	isStreaming bool,
	msg *protocol.Message,
	agentName string,
	invocation *agent.Invocation) *event.Event {

	// Convert A2A parts to model content
	var content strings.Builder
	var toolResponseRole model.Role
	var toolResponseID string
	var toolResponseName string
	var toolCalls []model.ToolCall

	// Process all parts in the A2A message
	for _, part := range msg.Parts {
		switch part.GetKind() {
		case protocol.KindText:
			p, ok := part.(*protocol.TextPart)
			if !ok {
				log.Warnf("unexpected part type: %T", part)
				continue
			}
			content.WriteString(p.Text)

		case protocol.KindData:
			// Handle DataPart - can receive both function_call and function_response
			d, ok := part.(*protocol.DataPart)
			if !ok {
				continue
			}

			// Check metadata type
			if d.Metadata != nil {
				typeVal, hasType := d.Metadata[dataPartMetadataTypeKey]
				if !hasType {
					continue
				}

				switch typeVal {
				case dataPartMetadataTypeFunctionCall:
					// Server is requesting Client to call a tool
					toolCall := convertDataPartToToolCall(d)
					if toolCall != nil {
						toolCalls = append(toolCalls, *toolCall)
					}

				case dataPartMetadataTypeFunctionResp:
					// Server is returning a tool response (after Client executed the tool)
					toolResponseRole = model.RoleTool
					data, ok := d.Data.(map[string]any)
					if ok {
						if id, ok := data[toolCallFieldID].(string); ok {
							toolResponseID = id
						}
						if name, ok := data[toolCallFieldName].(string); ok {
							toolResponseName = name
						}
					}
					convertDataPartToToolResponse(d, &content)
				}
			}
		}
	}

	// Create message based on what we received
	var message model.Message

	if len(toolCalls) > 0 {
		// This is a tool call request from server
		message = model.Message{
			Role:      model.RoleAssistant,
			Content:   content.String(),
			ToolCalls: toolCalls,
		}
	} else if toolResponseRole == model.RoleTool {
		// This is a tool response
		message = model.Message{
			Role:     model.RoleTool,
			Content:  content.String(),
			ToolID:   toolResponseID,
			ToolName: toolResponseName,
		}
	} else {
		// This is a regular assistant message
		message = model.Message{
			Role:    model.RoleAssistant,
			Content: content.String(),
		}
	}

	event := event.New(invocation.InvocationID, agentName)

	// Following trpc-agent-go convention:
	// - Tool calls always go in Message (never in Delta), even in streaming mode
	if isStreaming {
		if len(toolCalls) > 0 || toolResponseRole == model.RoleTool {
			// Tool call or tool response: use Message even in streaming mode
			// Done should always be false because:
			// - For tool calls: need to wait for tool execution
			// - For tool responses: need to wait for final assistant response
			event.Response = &model.Response{
				ID:        msg.MessageID,
				Choices:   []model.Choice{{Message: message}},
				Timestamp: time.Now(),
				Created:   time.Now().Unix(),
				IsPartial: false,
				Done:      false,
			}
			event.Response.Object = model.ObjectTypeChatCompletion
		} else {
			// Regular content: use Delta in streaming mode
			event.Response = &model.Response{
				ID:        msg.MessageID,
				Choices:   []model.Choice{{Delta: message}},
				Timestamp: time.Now(),
				Created:   time.Now().Unix(),
				IsPartial: true,
				Done:      false,
			}
			if message.Content != "" {
				event.Response.Object = model.ObjectTypeChatCompletionChunk
			}
		}
		return event
	}

	// Non-streaming: always use Message
	event.Response = &model.Response{
		ID:        msg.MessageID,
		Choices:   []model.Choice{{Message: message}},
		Timestamp: time.Now(),
		Created:   time.Now().Unix(),
		IsPartial: false,
		Done:      true,
	}
	if message.Content != "" || len(toolCalls) > 0 {
		event.Response.Object = model.ObjectTypeChatCompletion
	}
	return event
}

// convertDataPartToToolResponse converts a DataPart with function_response metadata to content
func convertDataPartToToolResponse(dataPart *protocol.DataPart, content *strings.Builder) {
	data, ok := dataPart.Data.(map[string]any)
	if !ok {
		log.Warnf("DataPart data is not a map: %T", dataPart.Data)
		return
	}

	// Extract response content - server sends it as raw string
	if response, ok := data[toolCallFieldResponse]; ok {
		if responseStr, ok := response.(string); ok {
			content.WriteString(responseStr)
		} else {
			log.Infof("Tool response is not a string: %T, skip")
		}
	}
}

// convertDataPartToToolCall converts a DataPart with function_call metadata to ToolCall
func convertDataPartToToolCall(dataPart *protocol.DataPart) *model.ToolCall {
	data, ok := dataPart.Data.(map[string]any)
	if !ok {
		log.Warnf("DataPart data is not a map: %T", dataPart.Data)
		return nil
	}

	// Extract tool call fields
	var toolCall model.ToolCall

	if id, ok := data[toolCallFieldID].(string); ok {
		toolCall.ID = id
	}

	if toolType, ok := data[toolCallFieldType].(string); ok {
		toolCall.Type = toolType
	}

	if name, ok := data[toolCallFieldName].(string); ok {
		toolCall.Function.Name = name
	}

	if args, ok := data[toolCallFieldArgs].(string); ok {
		toolCall.Function.Arguments = []byte(args)
	}

	// Validate that we have at least a name
	if toolCall.Function.Name == "" {
		log.Warnf("Tool call missing function name")
		return nil
	}

	return &toolCall
}

// convertTaskToMessage converts a Task to a Message
func convertTaskToMessage(task *protocol.Task) *protocol.Message {
	var (
		parts     []protocol.Part
		messageID string
	)
	// Add artifacts if any
	for _, artifact := range task.Artifacts {
		parts = append(parts, artifact.Parts...)
		messageID = artifact.ArtifactID
	}

	return &protocol.Message{
		Role:      protocol.MessageRoleAgent,
		Kind:      protocol.KindMessage,
		MessageID: messageID,
		Parts:     parts,
		TaskID:    &task.ID,
		ContextID: &task.ContextID,
	}
}

// convertTaskStatusToMessage converts a TaskStatusUpdateEvent to a Message
func convertTaskStatusToMessage(event *protocol.TaskStatusUpdateEvent) *protocol.Message {
	msg := &protocol.Message{
		Role:      protocol.MessageRoleAgent,
		Kind:      protocol.KindMessage,
		TaskID:    &event.TaskID,
		ContextID: &event.ContextID,
	}
	if event.Status.Message != nil {
		msg.Parts = event.Status.Message.Parts
		msg.MessageID = event.Status.Message.MessageID
	}
	return msg
}

// convertTaskArtifactToMessage converts a TaskArtifactUpdateEvent to a Message.
func convertTaskArtifactToMessage(event *protocol.TaskArtifactUpdateEvent) *protocol.Message {
	return &protocol.Message{
		Role:      protocol.MessageRoleAgent,
		Kind:      protocol.KindMessage,
		MessageID: event.Artifact.ArtifactID,
		Parts:     event.Artifact.Parts,
		TaskID:    &event.TaskID,
		ContextID: &event.ContextID,
	}
}
