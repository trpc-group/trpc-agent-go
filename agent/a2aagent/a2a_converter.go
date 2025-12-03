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
	var evt *event.Event
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
		evt = d.buildRespEvent(false, responseMsg, agentName, invocation)
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
		evt = d.buildRespEvent(false, responseMsg, agentName, invocation)
	default:
		// Handle unknown response types
		responseMsg = &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{protocol.NewTextPart("Received unknown response type")},
		}
		evt = d.buildRespEvent(false, responseMsg, agentName, invocation)
	}
	evt.Done = true
	evt.IsPartial = false
	return evt, nil
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

	var evt *event.Event
	var responseMsg *protocol.Message
	switch v := result.Result.(type) {
	case *protocol.Message:
		responseMsg = v
		evt = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.Task:
		responseMsg = convertTaskToMessage(v)
		evt = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.TaskStatusUpdateEvent:
		responseMsg = convertTaskStatusToMessage(v)
		evt = d.buildRespEvent(true, responseMsg, agentName, invocation)
	case *protocol.TaskArtifactUpdateEvent:
		responseMsg = convertTaskArtifactToMessage(v)
		evt = d.buildRespEvent(true, responseMsg, agentName, invocation)
	default:
		log.Infof("unexpected event type: %T", result.Result)
	}
	return evt, nil
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

	// Determine object type based on result
	var objectType string
	if parseResult.codeExecution != "" {
		objectType = model.ObjectTypePostprocessingCodeExecution
	} else if parseResult.codeExecutionResult != "" {
		objectType = model.ObjectTypePostprocessingCodeExecutionResult
	} else {
		objectType = extractObjectType(msg.Metadata)
	}

	// Create event with appropriate response structure
	return buildEventResponse(isStreaming, msg.MessageID, parseResult, objectType, invocation, agentName)
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
}

// toolResponseData holds tool response information
type toolResponseData struct {
	id      string
	name    string
	content string
}

// getContent returns the effective content based on result type
func (r *parseResult) getContent() string {
	if len(r.toolResponses) > 0 {
		return r.toolResponses[0].content
	}
	if r.codeExecution != "" {
		return r.codeExecution
	}
	if r.codeExecutionResult != "" {
		return r.codeExecutionResult
	}
	return r.textContent
}

// parseA2AMessageParts processes all parts in the A2A message and extracts content and tool information
func parseA2AMessageParts(parts []protocol.Part) *parseResult {
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

// buildModelMessage creates a model.Message based on the parse result
func buildModelMessage(result *parseResult) model.Message {
	// tool call event (assistant requesting tool execution)
	if len(result.toolCalls) > 0 {
		return model.Message{
			Role:      model.RoleAssistant,
			Content:   result.textContent,
			ToolCalls: result.toolCalls,
		}
	}

	// code execution event - use code execution content
	if result.codeExecution != "" {
		return model.Message{
			Role:    model.RoleAssistant,
			Content: result.codeExecution,
		}
	}

	// code execution result event
	if result.codeExecutionResult != "" {
		return model.Message{
			Role:    model.RoleAssistant,
			Content: result.codeExecutionResult,
		}
	}

	// regular text content
	return model.Message{
		Role:    model.RoleAssistant,
		Content: result.textContent,
	}
}

// buildToolRespChoices creates model.Choice slice for multiple tool responses
func buildToolRespChoices(toolResponses []toolResponseData) []model.Choice {
	choices := make([]model.Choice, 0, len(toolResponses))
	for _, resp := range toolResponses {
		choices = append(choices, model.Choice{
			Message: model.Message{
				Role:     model.RoleTool,
				Content:  resp.content,
				ToolID:   resp.id,
				ToolName: resp.name,
			},
		})
	}
	return choices
}

// extractObjectType extracts the object_type from message metadata
func extractObjectType(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}

	if objectType, ok := metadata[ia2a.MessageMetadataObjectTypeKey].(string); ok {
		return objectType
	}

	return ""
}

// buildEventResponse creates an event with the appropriate response structure
func buildEventResponse(
	isStreaming bool,
	messageID string,
	result *parseResult,
	objectType string,
	invocation *agent.Invocation,
	agentName string,
) *event.Event {
	evt := event.New(invocation.InvocationID, agentName)

	// Build choices based on result type
	var choices []model.Choice
	if len(result.toolResponses) > 0 {
		choices = buildToolRespChoices(result.toolResponses)
	} else {
		choices = []model.Choice{{Message: buildModelMessage(result)}}
	}

	if isStreaming {
		evt.Response = buildStreamingResponse(messageID, choices, result, objectType)
	} else {
		evt.Response = buildNonStreamingResponse(messageID, choices, result, objectType)
	}

	return evt
}

// buildStreamingResponse creates a response for streaming mode
func buildStreamingResponse(messageID string, choices []model.Choice, result *parseResult, objectType string) *model.Response {
	now := time.Now()

	// tool call or tool response: use Message (not Delta)
	if len(result.toolCalls) > 0 || len(result.toolResponses) > 0 {
		// manaually set object type for tool call or tool response
		respObject := model.ObjectTypeChatCompletion
		if objectType != "" {
			respObject = objectType
		}
		return &model.Response{
			ID:        messageID,
			Choices:   choices,
			Timestamp: now,
			Created:   now.Unix(),
			IsPartial: false,
			Done:      false,
			Object:    respObject,
		}
	}

	// Regular content: use Delta in streaming mode
	// Convert Message to Delta for streaming
	deltaChoices := make([]model.Choice, len(choices))
	for i, c := range choices {
		deltaChoices[i] = model.Choice{Delta: c.Message}
	}

	respObject := model.ObjectTypeChatCompletionChunk
	if objectType != "" {
		respObject = objectType
	}
	return &model.Response{
		ID:        messageID,
		Choices:   deltaChoices,
		Timestamp: now,
		Created:   now.Unix(),
		IsPartial: true,
		Done:      false,
		Object:    respObject,
	}
}

// buildNonStreamingResponse creates a response for non-streaming mode
func buildNonStreamingResponse(messageID string, choices []model.Choice, result *parseResult, objectType string) *model.Response {
	now := time.Now()
	response := &model.Response{
		ID:        messageID,
		Choices:   choices,
		Timestamp: now,
		Created:   now.Unix(),
		IsPartial: false,
		Done:      true,
	}
	hasContent := len(choices) > 0 && choices[0].Message.Content != ""
	if hasContent || len(result.toolCalls) > 0 {
		if objectType != "" {
			response.Object = objectType
		} else {
			response.Object = model.ObjectTypeChatCompletion
		}
	}
	return response
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
