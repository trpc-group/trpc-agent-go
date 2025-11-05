//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalset

import (
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
)

// EvalCase represents a single evaluation case.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalCase struct {
	// EvalID uniquely identifies this evaluation case.
	EvalID string `json:"eval_id,omitempty"`
	// Conversation contains the sequence of invocations.
	Conversation []*Invocation `json:"conversation,omitempty"`
	// SessionInput contains initialization data for the session.
	SessionInput *SessionInput `json:"session_input,omitempty"`
	// CreationTimestamp when this eval case was created.
	CreationTimestamp *epochtime.EpochTime `json:"creation_timestamp,omitempty"`
}

// Invocation represents a single invocation in a conversation.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type Invocation struct {
	// InvocationID uniquely identifies this invocation.
	InvocationID string `json:"invocation_id,omitempty"`
	// UserContent represents the user's input.
	UserContent *genai.Content `json:"user_content,omitempty"`
	// FinalResponse represents the agent's final response.
	FinalResponse *genai.Content `json:"final_response,omitempty"`
	// IntermediateData contains intermediate steps during execution.
	IntermediateData *IntermediateData `json:"intermediate_data,omitempty"`
	// CreationTimestamp when this invocation was created.
	CreationTimestamp *epochtime.EpochTime `json:"creation_timestamp,omitempty"`
}

// IntermediateData contains intermediate execution data.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type IntermediateData struct {
	// ToolUses represents tool calls made during execution.
	ToolUses []*genai.FunctionCall `json:"tool_uses,omitempty"`
	// ToolResponses represents tool responses made during execution.
	ToolResponses []*genai.FunctionResponse `json:"tool_responses,omitempty"`
	// IntermediateResponses represents intermediate responses, including text responses and tool responses.
	// For each intermediate response, the first element is the author string,
	// and the second element is the genai.Part slice.
	IntermediateResponses [][]any `json:"intermediate_responses,omitempty"`
}

// SessionInput represents values that help initialize a session.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type SessionInput struct {
	// AppName identifies the app.
	AppName string `json:"app_name,omitempty"`
	// UserID identifies the user.
	UserID string `json:"user_id,omitempty"`
	// State contains the initial state of the session.
	State map[string]any `json:"state,omitempty"`
}
