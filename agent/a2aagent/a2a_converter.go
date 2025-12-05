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
	// ConvertToEvents converts an A2A protocol type to multiple Events.
	// In non-streaming mode, A2A server returns a Task with history containing
	// intermediate messages (tool calls, tool responses, etc.) and artifacts for final response.
	ConvertToEvents(result protocol.MessageResult, agentName string, invocation *agent.Invocation) ([]*event.Event, error)

	// ConvertStreamingToEvents converts a streaming A2A protocol type to Events.
	ConvertStreamingToEvents(result protocol.StreamingMessageEvent, agentName string, invocation *agent.Invocation) ([]*event.Event, error)
}

// InvocationA2AConverter defines an interface for converting invocations to A2A protocol messages.
type InvocationA2AConverter interface {
	// ConvertToA2AMessage converts an invocation to an A2A protocol Message.
	ConvertToA2AMessage(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error)
}

type defaultA2AEventConverter struct {
}

func (d *defaultA2AEventConverter) ConvertToEvents(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	if result.Result == nil {
		return []*event.Event{event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		)}, nil
	}

	var events []*event.Event

	switch v := result.Result.(type) {
	case *protocol.Message:
		// Single message: build event from its parts
		if evt := d.buildRespEvent(false, v, agentName, invocation); evt != nil {
			events = append(events, evt)
		}
	case *protocol.Task:
		// Task with history: convert history messages first, then artifacts
		// History contains intermediate messages (tool calls, tool responses, etc.)
		for i := range v.History {
			if evt := d.buildRespEvent(false, &v.History[i], agentName, invocation); evt != nil {
				events = append(events, evt)
			}
		}
		// Artifacts contain the final response
		for i := range v.Artifacts {
			artifactMsg := &protocol.Message{
				Role:      protocol.MessageRoleAgent,
				MessageID: v.Artifacts[i].ArtifactID,
				Parts:     v.Artifacts[i].Parts,
			}
			if evt := d.buildRespEvent(false, artifactMsg, agentName, invocation); evt != nil {
				events = append(events, evt)
			}
		}
	default:
		// Handle unknown response types
		responseMsg := &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{protocol.NewTextPart("Received unknown response type")},
		}
		if evt := d.buildRespEvent(false, responseMsg, agentName, invocation); evt != nil {
			events = append(events, evt)
		}
	}

	if len(events) > 0 {
		// Mark the last event as done
		events[len(events)-1].Done = true
		events[len(events)-1].IsPartial = false
	}
	return events, nil
}

func (d *defaultA2AEventConverter) ConvertStreamingToEvents(
	result protocol.StreamingMessageEvent,
	agentName string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	if result.Result == nil {
		return []*event.Event{event.NewResponseEvent(
			invocation.InvocationID,
			agentName,
			&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: ""}}}},
		)}, nil
	}

	var events []*event.Event
	var responseMsg *protocol.Message
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
	case *protocol.TaskStatusUpdateEvent:
		responseMsg = convertTaskStatusToMessage(v)
	case *protocol.TaskArtifactUpdateEvent:
		responseMsg = convertTaskArtifactToMessage(v)
	default:
		log.Infof("unexpected event type: %T", result.Result)
		return nil, nil
	}

	if evt := d.buildRespEvent(true, responseMsg, agentName, invocation); evt != nil {
		events = append(events, evt)
	}
	return events, nil
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

// buildRespEvent converts A2A response to tRPC event (used for both streaming and non-streaming mode)
func (d *defaultA2AEventConverter) buildRespEvent(
	isStreaming bool,
	msg *protocol.Message,
	agentName string,
	invocation *agent.Invocation) *event.Event {

	// Parse A2A message parts to extract content and tool information
	parseResult := parseA2AMessageParts(msg)

	// Create event with appropriate response structure
	return buildEventResponse(isStreaming, msg.MessageID, parseResult, invocation, agentName)
}

// parseResult holds the parsed information from A2A message parts
type parseResult struct {
	// textContent holds plain text content from TextParts
	textContent string

	// toolCalls holds function call requests (assistant -> tool)
	toolCalls []model.ToolCall

	// toolResponses holds function response data (tool -> assistant)
	// Multiple tool responses can exist in a single message
	toolResponses []toolResponseData

	// codeExecution holds executable code content
	codeExecution string

	// codeExecutionResult holds code execution result content
	codeExecutionResult string

	// objectType holds the type of the object
	objectType string

	// tag holds the event tag from A2A message metadata
	tag string
}

// toolResponseData holds tool response information
type toolResponseData struct {
	id      string
	name    string
	content string
}

// parseA2AMessageParts processes all parts in the A2A message and extracts content and tool information
func parseA2AMessageParts(msg *protocol.Message) *parseResult {
	parts := msg.Parts
	var textContent strings.Builder
	result := &parseResult{}

	for _, part := range parts {
		switch part.GetKind() {
		case protocol.KindText:
			textContent.WriteString(processTextPart(part))
		case protocol.KindData:
			processDataPart(part, result)
		}
	}

	if msg.Metadata != nil {
		if objectType, ok := msg.Metadata[ia2a.MessageMetadataObjectTypeKey].(string); ok {
			result.objectType = objectType
		}
		if tag, ok := msg.Metadata[ia2a.MessageMetadataTagKey].(string); ok {
			result.tag = tag
		}
	}

	result.textContent = textContent.String()
	return result
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

// processDataPart processes a DataPart and updates the parseResult accordingly
func processDataPart(part protocol.Part, result *parseResult) {
	d, ok := part.(*protocol.DataPart)
	if !ok {
		return
	}

	// Use GetDataPartType to get the type with correct precedence (adk_type first, then type)
	// GetDataPartType handles nil metadata internally
	typeStr := ia2a.GetDataPartType(d.Metadata)
	if typeStr == "" {
		return
	}

	switch typeStr {
	case ia2a.DataPartMetadataTypeFunctionCall:
		if toolCall := processFunctionCall(d); toolCall != nil {
			result.toolCalls = append(result.toolCalls, *toolCall)
		}
	case ia2a.DataPartMetadataTypeFunctionResp:
		content, id, name := processFunctionResponse(d)
		result.toolResponses = append(result.toolResponses, toolResponseData{
			id:      id,
			name:    name,
			content: content,
		})
	case ia2a.DataPartMetadataTypeExecutableCode:
		result.codeExecution = processExecutableCode(d)
	case ia2a.DataPartMetadataTypeCodeExecutionResult:
		result.codeExecutionResult = processCodeExecutionResult(d)
	default:
		log.Debugf("unknown DataPart type: %s", typeStr)
	}
}

// processFunctionCall processes a function call DataPart and returns the ToolCall
func processFunctionCall(d *protocol.DataPart) *model.ToolCall {
	data, ok := d.Data.(map[string]any)
	if !ok {
		log.Warnf("DataPart data is not a map: %T", d.Data)
		return nil
	}

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

// processFunctionResponse processes a function response DataPart and returns the response content and metadata
func processFunctionResponse(d *protocol.DataPart) (content string, id string, name string) {
	data, ok := d.Data.(map[string]any)
	if !ok {
		log.Warnf("DataPart data is not a map: %T", d.Data)
		return
	}

	// Extract tool response metadata
	if toolID, ok := data[ia2a.ToolCallFieldID].(string); ok {
		id = toolID
	}
	if toolName, ok := data[ia2a.ToolCallFieldName].(string); ok {
		name = toolName
	}

	// Extract response content - server sends it as raw string
	if response, ok := data[ia2a.ToolCallFieldResponse]; ok {
		if responseStr, ok := response.(string); ok {
			content = responseStr
		} else {
			log.Debugf("Tool response is not a string: %T, skip", response)
		}
	}

	return
}

// extractStringField extracts a string value from data map, trying primary key first, then fallback key
func extractStringField(data map[string]any, primary, fallback string) string {
	if v, ok := data[primary].(string); ok {
		return v
	}
	if v, ok := data[fallback].(string); ok {
		return v
	}
	return ""
}

// processExecutableCode processes an executable code DataPart and returns the code content
func processExecutableCode(d *protocol.DataPart) string {
	data, ok := d.Data.(map[string]any)
	if !ok {
		return ""
	}
	return extractStringField(data, ia2a.CodeExecutionFieldCode, ia2a.CodeExecutionFieldContent)
}

// processCodeExecutionResult processes a code execution result DataPart and returns the result content
func processCodeExecutionResult(d *protocol.DataPart) string {
	data, ok := d.Data.(map[string]any)
	if !ok {
		return ""
	}
	return extractStringField(data, ia2a.CodeExecutionFieldOutput, ia2a.CodeExecutionFieldContent)
}

// buildEventResponse creates an event with the appropriate response structure
func buildEventResponse(
	isStreaming bool,
	messageID string,
	result *parseResult,
	invocation *agent.Invocation,
	agentName string,
) *event.Event {
	var opts []event.Option
	// Restore tag from A2A message metadata if present
	if result.tag != "" {
		opts = append(opts, event.WithTag(result.tag))
	}

	evt := event.New(invocation.InvocationID, agentName, opts...)

	if isStreaming {
		evt.Response = buildStreamingResponse(messageID, result)
	} else {
		evt.Response = buildNonStreamingResponse(messageID, result)
	}

	return evt
}

// buildStreamingResponse creates a response for streaming mode.
// In streaming mode:
// - Tool calls and tool responses use Message (not Delta) since they are complete units
// - Text content uses Delta for incremental updates
func buildStreamingResponse(messageID string, result *parseResult) *model.Response {
	now := time.Now()

	// Tool call: use Message (tool calls are complete units, not streamed incrementally)
	if len(result.toolCalls) > 0 {
		return &model.Response{
			ID: messageID,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:      model.RoleAssistant,
					Content:   result.textContent,
					ToolCalls: result.toolCalls,
				},
			}},
			Object:    model.ObjectTypeChatCompletion,
			Timestamp: now,
			Created:   now.Unix(),
			IsPartial: false,
			Done:      false,
		}
	}

	// Tool response: use Message (tool responses are complete units)
	if len(result.toolResponses) > 0 {
		choices := make([]model.Choice, 0, len(result.toolResponses))
		for _, resp := range result.toolResponses {
			choices = append(choices, model.Choice{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  resp.content,
					ToolID:   resp.id,
					ToolName: resp.name,
				},
			})
		}
		return &model.Response{
			ID:        messageID,
			Choices:   choices,
			Object:    model.ObjectTypeChatCompletion,
			Timestamp: now,
			Created:   now.Unix(),
			IsPartial: false,
			Done:      false,
		}
	}

	// Text content: use Delta for streaming incremental updates
	content := result.textContent
	if result.codeExecution != "" {
		content = result.codeExecution
	} else if result.codeExecutionResult != "" {
		content = result.codeExecutionResult
	}

	objectType := extractObjectType(result)
	if objectType == "" {
		objectType = model.ObjectTypeChatCompletionChunk
	}

	return &model.Response{
		ID: messageID,
		Choices: []model.Choice{{
			Delta: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
		Object:    objectType,
		Timestamp: now,
		Created:   now.Unix(),
		IsPartial: true,
		Done:      false,
	}
}

// extractObjectType determines the response object type from parseResult.
// Priority: 1) objectType from message metadata (for third-party framework compatibility,
// as some frameworks like ADK include object type in metadata)
// 2) Infer from content type (toolCalls, codeExecution, codeExecutionResult)
// 3) Return empty string to let caller use default value
func extractObjectType(result *parseResult) string {
	if result.objectType != "" {
		return result.objectType
	}

	if len(result.toolCalls) > 0 {
		return model.ObjectTypeChatCompletion
	}

	// Both code execution and code execution result use the same ObjectType.
	// The distinction is made via the Tag field.
	if len(result.codeExecution) > 0 || len(result.codeExecutionResult) > 0 {
		return model.ObjectTypePostprocessingCodeExecution
	}

	return ""
}

// buildNonStreamingResponse creates a response for non-streaming mode.
// In non-streaming mode, all content uses Message (not Delta).
func buildNonStreamingResponse(messageID string, result *parseResult) *model.Response {
	now := time.Now()

	var choices []model.Choice

	// Tool call: assistant requesting tool execution
	if len(result.toolCalls) > 0 {
		choices = append(choices, model.Choice{
			Message: model.Message{
				Role:      model.RoleAssistant,
				Content:   result.textContent,
				ToolCalls: result.toolCalls,
			},
		})
	}

	// Tool response: tool returning results
	if len(result.toolResponses) > 0 {
		for _, resp := range result.toolResponses {
			choices = append(choices, model.Choice{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  resp.content,
					ToolID:   resp.id,
					ToolName: resp.name,
				},
			})
		}
	}

	// Text content: final assistant response
	// Only add if no tool calls (tool calls already include text content)
	if len(result.toolCalls) == 0 && (result.textContent != "" || result.codeExecution != "" || result.codeExecutionResult != "") {
		content := result.textContent
		if result.codeExecution != "" {
			content = result.codeExecution
		} else if result.codeExecutionResult != "" {
			content = result.codeExecutionResult
		}
		choices = append(choices, model.Choice{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		})
	}

	// If no content at all, add empty assistant message
	if len(choices) == 0 {
		choices = []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "",
			},
		}}
	}

	objectType := extractObjectType(result)
	if objectType == "" {
		objectType = model.ObjectTypeChatCompletion
	}

	return &model.Response{
		ID:        messageID,
		Choices:   choices,
		Object:    objectType,
		Timestamp: now,
		Created:   now.Unix(),
		IsPartial: false,
		Done:      false,
	}
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
		Metadata:  task.Metadata,
	}
}

// convertTaskStatusToMessage converts a TaskStatusUpdateEvent to a Message
func convertTaskStatusToMessage(event *protocol.TaskStatusUpdateEvent) *protocol.Message {
	msg := &protocol.Message{
		Role:      protocol.MessageRoleAgent,
		Kind:      protocol.KindMessage,
		TaskID:    &event.TaskID,
		ContextID: &event.ContextID,
		Metadata:  event.Metadata,
	}
	if event.Status.Message != nil {
		msg.Parts = event.Status.Message.Parts
		msg.MessageID = event.Status.Message.MessageID
	}
	return msg
}

// convertTaskArtifactToMessage converts a TaskArtifactUpdateEvent to a Message.
func convertTaskArtifactToMessage(event *protocol.TaskArtifactUpdateEvent) *protocol.Message {
	msg := &protocol.Message{
		Role:      protocol.MessageRoleAgent,
		Kind:      protocol.KindMessage,
		MessageID: event.Artifact.ArtifactID,
		Parts:     event.Artifact.Parts,
		TaskID:    &event.TaskID,
		ContextID: &event.ContextID,
		Metadata:  event.Metadata,
	}
	return msg
}
