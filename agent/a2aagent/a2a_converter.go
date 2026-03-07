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
	"encoding/json"
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
		// Status updates (submitted/completed) are control signals, not user-facing content.
		return nil, nil
	case *protocol.TaskArtifactUpdateEvent:
		if v.IsFinal() {
			// Final artifact chunk is either an aggregated result or a termination signal,
			// not incremental content for the user.
			return nil, nil
		}
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
	parts := d.buildA2AParts(invocation)

	if len(parts) == 0 {
		parts = append(parts, protocol.NewTextPart(""))
	}
	message := protocol.NewMessage(protocol.MessageRoleUser, parts)
	sess := invocation.Session
	if sess != nil {
		message.ContextID = &sess.ID
	}

	message.Metadata = make(map[string]any)
	if invocation.InvocationID != "" {
		message.Metadata["invocation_id"] = invocation.InvocationID
	}
	if sess != nil && sess.UserID != "" {
		message.Metadata["user_id"] = sess.UserID
	}
	message.Metadata[ia2a.MessageMetadataInteractionSpecVersionKey] = ia2a.InteractionVersion

	return &message, nil
}

// buildA2AParts converts invocation message content and content parts to A2A protocol parts.
func (d *defaultEventA2AConverter) buildA2AParts(invocation *agent.Invocation) []protocol.Part {
	var parts []protocol.Part

	if invocation.Message.Content != "" {
		parts = append(parts, protocol.NewTextPart(invocation.Message.Content))
	}

	for _, contentPart := range invocation.Message.ContentParts {
		parts = appendContentPart(parts, contentPart)
	}

	return parts
}

// appendContentPart converts a single model.ContentPart and appends it to parts.
func appendContentPart(parts []protocol.Part, cp model.ContentPart) []protocol.Part {
	switch cp.Type {
	case model.ContentTypeText:
		return appendTextPart(parts, cp)
	case model.ContentTypeImage:
		return appendImagePart(parts, cp)
	case model.ContentTypeAudio:
		return appendAudioPart(parts, cp)
	case model.ContentTypeFile:
		return appendFilePart(parts, cp)
	default:
		return parts
	}
}

func appendTextPart(parts []protocol.Part, cp model.ContentPart) []protocol.Part {
	if cp.Text == nil {
		return parts
	}
	return append(parts, protocol.NewTextPart(*cp.Text))
}

func appendImagePart(parts []protocol.Part, cp model.ContentPart) []protocol.Part {
	if cp.Image == nil {
		return parts
	}
	if len(cp.Image.Data) > 0 {
		return append(parts, protocol.NewFilePartWithBytes(
			"image",
			cp.Image.Format,
			base64.StdEncoding.EncodeToString(cp.Image.Data),
		))
	}
	if cp.Image.URL != "" {
		return append(parts, protocol.NewFilePartWithURI(
			"image",
			cp.Image.Format,
			cp.Image.URL,
		))
	}
	return parts
}

func appendAudioPart(parts []protocol.Part, cp model.ContentPart) []protocol.Part {
	if cp.Audio == nil || cp.Audio.Data == nil {
		return parts
	}
	return append(parts, protocol.NewFilePartWithBytes(
		"audio",
		cp.Audio.Format,
		base64.StdEncoding.EncodeToString(cp.Audio.Data),
	))
}

func appendFilePart(parts []protocol.Part, cp model.ContentPart) []protocol.Part {
	if cp.File == nil || len(cp.File.Data) == 0 {
		return parts
	}
	fileName := cp.File.Name
	if fileName == "" {
		fileName = "file"
	}
	return append(parts, protocol.NewFilePartWithBytes(
		fileName,
		cp.File.MimeType,
		base64.StdEncoding.EncodeToString(cp.File.Data),
	))
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
	return buildEventResponse(isStreaming, msg.MessageID, parseResult, invocation, agentName, msg.Role)
}

// parseResult holds the parsed information from A2A message parts
type parseResult struct {
	// textContent holds plain text content from TextParts
	textContent string

	// reasoningContent holds thought/reasoning content from TextParts with thought metadata
	reasoningContent string

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

	// responseID holds the original LLM Response.ID from A2A message metadata
	responseID string
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
	var reasoningContent strings.Builder
	result := &parseResult{}

	for _, part := range parts {
		switch part.GetKind() {
		case protocol.KindText:
			text, isThought := processTextPart(part)
			if isThought {
				reasoningContent.WriteString(text)
			} else {
				textContent.WriteString(text)
			}
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
		if responseID, ok := msg.Metadata[ia2a.MessageMetadataResponseIDKey].(string); ok {
			result.responseID = responseID
		}
	}

	result.textContent = textContent.String()
	result.reasoningContent = reasoningContent.String()
	return result
}

// processTextPart processes a TextPart and returns its content and whether it's a thought
func processTextPart(part protocol.Part) (text string, isThought bool) {
	p, ok := part.(*protocol.TextPart)
	if !ok {
		log.Warnf("unexpected part type: %T", part)
		return "", false
	}

	// Check if this is a thought/reasoning content by looking at metadata
	// Support both "thought" and "adk_thought" keys for ADK compatibility
	if p.Metadata != nil {
		if thought, ok := p.Metadata[ia2a.TextPartMetadataThoughtKey]; ok {
			if thoughtBool, ok := thought.(bool); ok && thoughtBool {
				return p.Text, true
			}
		}
		adkThoughtKey := ia2a.GetADKMetadataKey(ia2a.TextPartMetadataThoughtKey)
		if thought, ok := p.Metadata[adkThoughtKey]; ok {
			if thoughtBool, ok := thought.(bool); ok && thoughtBool {
				return p.Text, true
			}
		}
	}

	return p.Text, false
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

	if args, ok := data[ia2a.ToolCallFieldArgs]; ok {
		switch v := args.(type) {
		case string:
			toolCall.Function.Arguments = []byte(v)
		case map[string]any:
			if jsonBytes, err := json.Marshal(v); err == nil {
				toolCall.Function.Arguments = jsonBytes
			} else {
				log.Warnf("Failed to marshal tool call arguments: %v", err)
			}
		default:
			log.Warnf("Tool call arguments has unexpected type: %T", v)
		}
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

// convertA2ARoleToModelRole converts A2A protocol role to internal model role
func convertA2ARoleToModelRole(role protocol.MessageRole) model.Role {
	switch role {
	case protocol.MessageRoleUser:
		return model.RoleUser
	case protocol.MessageRoleAgent:
		return model.RoleAssistant
	default:
		// Default to assistant for unknown roles
		return model.RoleAssistant
	}
}

// buildEventResponse creates an event with the appropriate response structure
func buildEventResponse(
	isStreaming bool,
	messageID string,
	result *parseResult,
	invocation *agent.Invocation,
	agentName string,
	role protocol.MessageRole,
) *event.Event {
	var opts []event.Option
	// Restore tag from A2A message metadata if present
	if result.tag != "" {
		opts = append(opts, event.WithTag(result.tag))
	}

	evt := event.New(invocation.InvocationID, agentName, opts...)

	// Use llm_response_id from metadata when available (preserves original LLM Response.ID),
	// fall back to messageID (which is ArtifactID in streaming, or Message.MessageID in unary).
	respID := messageID
	if result.responseID != "" {
		respID = result.responseID
	}

	if isStreaming {
		evt.Response = buildStreamingResponse(respID, result, role)
	} else {
		evt.Response = buildNonStreamingResponse(respID, result, role)
	}

	return evt
}

// buildStreamingResponse creates a response for streaming mode.
// In streaming mode:
// - Tool calls and tool responses use Message (not Delta) since they are complete units
// - Text content uses Delta for incremental updates
func buildStreamingResponse(messageID string, result *parseResult, role protocol.MessageRole) *model.Response {
	now := time.Now()

	// Tool call: use Message (tool calls are complete units, not streamed incrementally)
	if len(result.toolCalls) > 0 {
		return &model.Response{
			ID: messageID,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:             model.RoleAssistant,
					Content:          result.textContent,
					ReasoningContent: result.reasoningContent,
					ToolCalls:        result.toolCalls,
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
			Object:    model.ObjectTypeToolResponse,
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

	// Convert A2A protocol role to internal model role
	internalRole := convertA2ARoleToModelRole(role)
	return &model.Response{
		ID: messageID,
		Choices: []model.Choice{{
			Delta: model.Message{
				Role:             internalRole,
				Content:          content,
				ReasoningContent: result.reasoningContent,
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

	if len(result.toolResponses) > 0 {
		return model.ObjectTypeToolResponse
	}

	if len(result.codeExecution) > 0 || len(result.codeExecutionResult) > 0 {
		return model.ObjectTypePostprocessingCodeExecution
	}

	return ""
}

// buildNonStreamingResponse creates a response for non-streaming mode.
// In non-streaming mode, all content uses Message (not Delta).
func buildNonStreamingResponse(messageID string, result *parseResult, role protocol.MessageRole) *model.Response {
	now := time.Now()
	var choices []model.Choice
	// Tool call: assistant requesting tool execution
	if len(result.toolCalls) > 0 {
		choices = append(choices, model.Choice{
			Message: model.Message{
				Role:             model.RoleAssistant,
				Content:          result.textContent,
				ReasoningContent: result.reasoningContent,
				ToolCalls:        result.toolCalls,
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
	if len(result.toolCalls) == 0 && (result.textContent != "" || result.reasoningContent != "" || result.codeExecution != "" || result.codeExecutionResult != "") {
		content := result.textContent
		if result.codeExecution != "" {
			content = result.codeExecution
		} else if result.codeExecutionResult != "" {
			content = result.codeExecutionResult
		}
		internalRole := convertA2ARoleToModelRole(role)
		choices = append(choices, model.Choice{
			Message: model.Message{
				Role:             internalRole,
				Content:          content,
				ReasoningContent: result.reasoningContent,
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
