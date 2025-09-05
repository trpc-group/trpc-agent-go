//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalset provides evaluation set for evaluation.
package evalset

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalCase represents a single evaluation case.
type EvalCase struct {
	// EvalID uniquely identifies this evaluation case.
	EvalID string `json:"eval_id"`
	// Conversation contains the sequence of invocations.
	Conversation []Invocation `json:"conversation"`
	// SessionInput contains initialization data for the session.
	SessionInput *SessionInput `json:"session_input,omitempty"`
	// CreationTimestamp when this eval case was created.
	CreationTimestamp time.Time `json:"creation_timestamp"`
}

// Invocation represents a single invocation in a conversation.
type Invocation struct {
	// InvocationID uniquely identifies this invocation.
	InvocationID string `json:"invocation_id"`
	// UserContent represents the user's input.
	UserContent string `json:"user_content"`
	// FinalResponse represents the agent's final response.
	FinalResponse string `json:"final_response,omitempty"`
	// IntermediateData contains intermediate steps during execution.
	IntermediateData *IntermediateData `json:"intermediate_data,omitempty"`
	// CreationTimestamp when this invocation was created.
	CreationTimestamp time.Time `json:"creation_timestamp"`
}

// IntermediateData contains intermediate execution data.
type IntermediateData struct {
	// ToolUses represents tool calls made during execution.
	ToolUses []*model.ToolCall `json:"tool_uses"`
	// ToolResponses represents responses from tool calls.
	ToolResponses []*model.Response `json:"tool_responses"`
	// IntermediateResponses from sub-agents or intermediate steps.
	IntermediateResponses []*model.Response `json:"intermediate_responses"`
}

// SessionInput represents values that help initialize a session.
type SessionInput struct {
	// AppName is the name of the application.
	AppName string `json:"app_name"`
	// UserID identifies the user.
	UserID string `json:"user_id"`
	// State contains the initial state of the session.
	State map[string]interface{} `json:"state"`
}
