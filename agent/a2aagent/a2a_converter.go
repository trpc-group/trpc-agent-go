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
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

	// Parse A2A message parts to extract content and tool information
	parseResult := parseA2AMessageParts(msg.Parts)

	// Build model message based on parsed result
	message := buildModelMessage(parseResult)

	// Create event with appropriate response structure
	return buildEventResponse(isStreaming, msg.MessageID, message, parseResult, invocation, agentName)
}

// parseResult holds the parsed information from A2A message parts
type parseResult struct {
	content          string
	toolCalls        []model.ToolCall
	toolResponseRole model.Role
	toolResponseID   string
	toolResponseName string
}

// parseA2AMessageParts processes all parts in the A2A message and extracts content and tool information
func parseA2AMessageParts(parts []protocol.Part) *parseResult {
	var content strings.Builder
	result := &parseResult{}

	for _, part := range parts {
		switch part.GetKind() {
		case protocol.KindText:
			textContent := processTextPart(part)
			content.WriteString(textContent)
		case protocol.KindData:
			dataContent, toolCall, toolResp := processDataPart(part)
			content.WriteString(dataContent)

			if toolCall != nil {
				result.toolCalls = append(result.toolCalls, *toolCall)
			}

			if toolResp != nil {
				result.toolResponseRole = model.RoleTool
				result.toolResponseID = toolResp.id
				result.toolResponseName = toolResp.name
			}
		}
	}

	result.content = content.String()
	return result
}

// toolResponseInfo holds tool response metadata
type toolResponseInfo struct {
	id   string
	name string
}

// processTextPart processes a TextPart and returns its content
func processTextPart(part protocol.Part) string {
	p, ok := part.(*protocol.TextPart)
	if !ok {
		log.Warnf("unexpected part type: %T", part)
		return ""
	}
	return p.Text
}

// processDataPart processes a DataPart and returns content, tool call, and tool response info
func processDataPart(part protocol.Part) (content string, toolCall *model.ToolCall, toolResp *toolResponseInfo) {
	d, ok := part.(*protocol.DataPart)
	if !ok {
		return
	}

	// Check metadata type
	if d.Metadata == nil {
		return
	}

	// Try both standard "type" and ADK-compatible "adk_type" metadata keys
	typeVal, hasType := d.Metadata[ia2a.DataPartMetadataTypeKey]
	if !hasType {
		typeVal, hasType = d.Metadata[ia2a.GetADKMetadataKey(ia2a.DataPartMetadataTypeKey)]
		if !hasType {
			return
		}
	}

	// Convert typeVal to string for comparison
	typeStr, ok := typeVal.(string)
	if !ok {
		return
	}

	switch typeStr {
	case ia2a.DataPartMetadataTypeFunctionCall:
		// Process function call
		toolCall = processFunctionCall(d)
	case ia2a.DataPartMetadataTypeFunctionResp:
		// Process function response
		var id, name string
		content, id, name = processFunctionResponse(d)
		if id != "" || name != "" {
			toolResp = &toolResponseInfo{id: id, name: name}
		}
	}

	return
}

// processFunctionCall processes a function call DataPart and returns the ToolCall
func processFunctionCall(d *protocol.DataPart) *model.ToolCall {
	return convertDataPartToToolCall(d)
}

// processFunctionResponse processes a function response DataPart and returns the response content and metadata
func processFunctionResponse(d *protocol.DataPart) (content string, id string, name string) {
	// Extract tool response metadata
	data, ok := d.Data.(map[string]any)
	if ok {
		if toolID, ok := data[ia2a.ToolCallFieldID].(string); ok {
			id = toolID
		}
		if toolName, ok := data[ia2a.ToolCallFieldName].(string); ok {
			name = toolName
		}
	}

	// Extract and return response content
	content = convertDataPartToToolResponse(d)
	return
}

// buildModelMessage creates a model.Message based on the parse result
func buildModelMessage(result *parseResult) model.Message {
	// tool call event
	if len(result.toolCalls) > 0 {
		return model.Message{
			Role:      model.RoleAssistant,
			Content:   result.content,
			ToolCalls: result.toolCalls,
		}
	}

	// tool call resp event
	if result.toolResponseRole == model.RoleTool {
		return model.Message{
			Role:     model.RoleTool,
			Content:  result.content,
			ToolID:   result.toolResponseID,
			ToolName: result.toolResponseName,
		}
	}

	return model.Message{
		Role:    model.RoleAssistant,
		Content: result.content,
	}
}

// buildEventResponse creates an event with the appropriate response structure
func buildEventResponse(
	isStreaming bool,
	messageID string,
	message model.Message,
	result *parseResult,
	invocation *agent.Invocation,
	agentName string,
) *event.Event {
	evt := event.New(invocation.InvocationID, agentName)

	if isStreaming {
		evt.Response = buildStreamingResponse(messageID, message, result)
	} else {
		evt.Response = buildNonStreamingResponse(messageID, message, result)
	}

	return evt
}

// buildStreamingResponse creates a response for streaming mode
func buildStreamingResponse(messageID string, message model.Message, result *parseResult) *model.Response {
	// tool calls resp
	if len(result.toolCalls) > 0 || result.toolResponseRole == model.RoleTool {
		return &model.Response{
			ID:        messageID,
			Choices:   []model.Choice{{Message: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: false,
			Done:      false,
			Object:    model.ObjectTypeChatCompletion,
		}
	}

	// Regular content: use Delta in streaming mode
	response := &model.Response{
		ID:        messageID,
		Choices:   []model.Choice{{Delta: message}},
		Timestamp: time.Now(),
		Created:   time.Now().Unix(),
		IsPartial: true,
		Done:      false,
	}
	if message.Content != "" {
		response.Object = model.ObjectTypeChatCompletionChunk
	}
	return response
}

// buildNonStreamingResponse creates a response for non-streaming mode
func buildNonStreamingResponse(messageID string, message model.Message, result *parseResult) *model.Response {
	response := &model.Response{
		ID:        messageID,
		Choices:   []model.Choice{{Message: message}},
		Timestamp: time.Now(),
		Created:   time.Now().Unix(),
		IsPartial: false,
		Done:      true,
	}
	if message.Content != "" || len(result.toolCalls) > 0 {
		response.Object = model.ObjectTypeChatCompletion
	}
	return response
}

// convertDataPartToToolResponse converts a DataPart with function_response metadata to content
func convertDataPartToToolResponse(dataPart *protocol.DataPart) string {
	data, ok := dataPart.Data.(map[string]any)
	if !ok {
		log.Warnf("DataPart data is not a map: %T", dataPart.Data)
		return ""
	}

	// Extract response content - server sends it as raw string
	if response, ok := data[ia2a.ToolCallFieldResponse]; ok {
		if responseStr, ok := response.(string); ok {
			return responseStr
		}
		log.Debugf("Tool response is not a string: %T, skip")
	}

	return ""
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

	if id, ok := data[ia2a.ToolCallFieldID].(string); ok {
		toolCall.ID = id
	}

	if toolType, ok := data[ia2a.ToolCallFieldType].(string); ok {
		toolCall.Type = toolType
	}

	if name, ok := data[ia2a.ToolCallFieldName].(string); ok {
		toolCall.Function.Name = name
	}

	if args, ok := data[ia2a.ToolCallFieldArgs].(string); ok {
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
