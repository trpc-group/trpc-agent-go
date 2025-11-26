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
	DataPartMetadataTypeKey = "type"

	// DataPartMetadataTypeFunctionCall is the metadata value for function call DataPart.
	DataPartMetadataTypeFunctionCall = "function_call"

	// DataPartMetadataTypeFunctionResp is the metadata value for function response DataPart.
	DataPartMetadataTypeFunctionResp = "function_response"

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

	// ADKMetadataKeyPrefix is the prefix for ADK-compatible metadata keys.
	// ADK uses "adk_" prefix for metadata keys like "adk_type", "adk_app_name", "adk_user_id", etc.
	// This ensures compatibility with ADK's part converter which expects "adk_type" instead of "type".
	ADKMetadataKeyPrefix = "adk_"
)

// GetADKMetadataKey returns the ADK-compatible metadata key with "adk_" prefix.
// For example, GetADKMetadataKey("app_name") returns "adk_app_name".
func GetADKMetadataKey(key string) string {
	if key == "" {
		return ""
	}
	return ADKMetadataKeyPrefix + key
}
