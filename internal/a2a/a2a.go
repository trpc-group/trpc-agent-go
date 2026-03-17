//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package a2a provides shared constants and utilities for A2A protocol handling.
package a2a

const (
	// DataPartMetadataTypeKey is the metadata key for DataPart type.
	// Maps to A2A_DATA_PART_METADATA_TYPE_KEY in Python trpc-agent.
	DataPartMetadataTypeKey = "type"

	// DataPartMetadataTypeFunctionCall is the metadata value for function call DataPart.
	// Maps to A2A_DATA_PART_METADATA_TYPE_FUNCTION_CALL in Python trpc-agent.
	DataPartMetadataTypeFunctionCall = "function_call"

	// DataPartMetadataTypeFunctionResp is the metadata value for function response DataPart.
	// Maps to A2A_DATA_PART_METADATA_TYPE_FUNCTION_RESPONSE in Python trpc-agent.
	DataPartMetadataTypeFunctionResp = "function_response"

	// DataPartMetadataTypeExecutableCode is the metadata value for executable code DataPart.
	// Maps to A2A_DATA_PART_METADATA_TYPE_EXECUTABLE_CODE in Python trpc-agent.
	// Used by Google ADK for code execution scenarios.
	DataPartMetadataTypeExecutableCode = "executable_code"

	// DataPartMetadataTypeCodeExecutionResult is the metadata value for code execution result DataPart.
	// Maps to A2A_DATA_PART_METADATA_TYPE_CODE_EXECUTION_RESULT in Python trpc-agent.
	// Used by Google ADK for code execution scenarios.
	DataPartMetadataTypeCodeExecutionResult = "code_execution_result"

	// ToolCallFieldID is the data field key for tool call ID.
	ToolCallFieldID = "id"

	// ToolCallFieldType is the data field key for tool call type.
	ToolCallFieldType = "type"

	// ToolCallFieldName is the data field key for tool call name.
	ToolCallFieldName = "name"

	// ToolCallFieldArgs is the data field key for tool call arguments.
	ToolCallFieldArgs = "args"

	// ToolCallFieldResponse is the data field key for tool call response.
	ToolCallFieldResponse = "response"

	// CodeExecutionFieldCode is the data field key for executable code content.
	// Used in ADK mode for code execution scenarios.
	CodeExecutionFieldCode = "code"

	// CodeExecutionFieldLanguage is the data field key for code language.
	// Used in ADK mode for code execution scenarios.
	CodeExecutionFieldLanguage = "language"

	// CodeExecutionFieldOutput is the data field key for code execution output.
	// Used in ADK mode for code execution result.
	CodeExecutionFieldOutput = "output"

	// CodeExecutionFieldOutcome is the data field key for code execution outcome.
	// Used in ADK mode for code execution result (e.g., "OUTCOME_OK", "OUTCOME_FAILED").
	CodeExecutionFieldOutcome = "outcome"

	// CodeExecutionFieldContent is the data field key for raw content.
	// Used in non-ADK mode for code execution scenarios.
	CodeExecutionFieldContent = "content"

	// MessageMetadataObjectTypeKey is the metadata key for object type in A2A message.
	// This is used to preserve event object type (e.g., "postprocessing.code_execution") when converting
	// from agent events to A2A messages, allowing A2A clients to distinguish different event types.
	MessageMetadataObjectTypeKey = "object_type"

	// MessageMetadataTagKey is the metadata key for event tag in A2A message.
	// This is used to preserve event tag when converting from agent events to A2A messages,
	// allowing A2A clients to restore the tag information for business-specific labeling.
	MessageMetadataTagKey = "tag"

	// MessageMetadataResponseIDKey is the metadata key for the LLM response ID.
	// This preserves the original LLM Response.ID (e.g. OpenAI's "chatcmpl-xxx") across
	// A2A transport, enabling clients to group incremental chunks from the same LLM call.
	MessageMetadataResponseIDKey = "llm_response_id"

	// MessageMetadataStateDeltaKey is the metadata key for event state delta in A2A messages.
	// It carries a decoded form of Event.StateDelta so A2A peers can restore structured state updates.
	MessageMetadataStateDeltaKey = "state_delta"

	// TextPartMetadataThoughtKey is the metadata key for thought/reasoning content in TextPart.
	TextPartMetadataThoughtKey = "thought"

	// FilePartMetadataContentTypeKey is the metadata key for original content type in FilePart.
	// The client sets this to preserve the original ContentPart type ("image", "audio", "file")
	// across A2A transport, since the A2A protocol does not natively distinguish image/audio
	// from generic files within a FilePart.
	FilePartMetadataContentTypeKey = "content_type"

	// FilePartMetadataContentTypeImage marks a FilePart as originally ContentTypeImage.
	FilePartMetadataContentTypeImage = "image"

	// FilePartMetadataContentTypeAudio marks a FilePart as originally ContentTypeAudio.
	FilePartMetadataContentTypeAudio = "audio"

	// FilePartMetadataContentTypeFile marks a FilePart as originally ContentTypeFile.
	FilePartMetadataContentTypeFile = "file"

	// ADKMetadataKeyPrefix is the prefix for ADK-compatible metadata keys.
	// ADK uses "adk_" prefix for metadata keys like "adk_type", "adk_app_name", "adk_user_id", etc.
	// This ensures compatibility with ADK's part converter which expects "adk_type" instead of "type".
	ADKMetadataKeyPrefix = "adk_"

	// ExtensionTRPCA2AVersion is the URI for the trpc-a2a interaction version extension.
	// Declared in AgentCard.Capabilities.Extensions so that clients can detect which version of the
	// interaction protocol the server supports and apply compatible conversion logic.
	ExtensionTRPCA2AVersion = "trpc-a2a-version"

	// InteractionVersion is the current version of the trpc-agent-go interaction specification.
	// Bump this when the metadata schema, part encoding, or streaming conventions change in a
	// backward-incompatible way.
	InteractionVersion = "0.1"

	// MessageMetadataInteractionSpecVersionKey is the metadata key sent by the client in
	// request messages to declare which interaction spec version it supports.
	// The server can use this to apply version-specific conversion logic.
	MessageMetadataInteractionSpecVersionKey = "interaction_spec_version"
)

// GetADKMetadataKey returns the ADK-compatible metadata key with "adk_" prefix.
// For example, GetADKMetadataKey("app_name") returns "adk_app_name".
func GetADKMetadataKey(key string) string {
	if key == "" {
		return ""
	}
	return ADKMetadataKeyPrefix + key
}

// GetDataPartType retrieves the type from DataPart metadata with correct precedence.
// It checks "adk_type" first (ADK compatibility), then falls back to "type".
// This matches the behavior in ADK's part_converter.py.
func GetDataPartType(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}

	// Check for ADK-compatible key first ("adk_type")
	if typeVal, ok := metadata[GetADKMetadataKey(DataPartMetadataTypeKey)].(string); ok {
		return typeVal
	}

	// Fall back to standard key ("type")
	if typeVal, ok := metadata[DataPartMetadataTypeKey].(string); ok {
		return typeVal
	}

	return ""
}
