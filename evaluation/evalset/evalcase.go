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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalCase represents a single evaluation case.
type EvalCase struct {
	// EvalID uniquely identifies this evaluation case.
	EvalID string `json:"evalId,omitempty"`
	// Conversation contains the sequence of invocations.
	Conversation []*Invocation `json:"conversation,omitempty"`
	// SessionInput contains initialization data for the session.
	SessionInput *SessionInput `json:"sessionInput,omitempty"`
	// CreationTimestamp when this eval case was created.
	CreationTimestamp *epochtime.EpochTime `json:"creationTimestamp,omitempty"`
}

// Invocation represents a single invocation in a conversation.
type Invocation struct {
	// InvocationID uniquely identifies this invocation.
	InvocationID string `json:"invocationId,omitempty"`
	// UserContent represents the user's input.
	UserContent *model.Message `json:"userContent,omitempty"`
	// FinalResponse represents the agent's final response.
	FinalResponse *model.Message `json:"finalResponse,omitempty"`
	// Tools represents the tool calls and responses.
	Tools []*Tool `json:"tools,omitempty"`
	// IntermediateResponses contains intermediate responses during execution.
	IntermediateResponses []*model.Message `json:"intermediateResponses,omitempty"`
	// CreationTimestamp when this invocation was created.
	CreationTimestamp *epochtime.EpochTime `json:"creationTimestamp,omitempty"`
}

// Tool represents a single tool invocation and its execution result.
type Tool struct {
	ID        string         `json:"id,omitempty"`        // Tool call ID.
	Name      string         `json:"name,omitempty"`      // Tool name.
	Arguments map[string]any `json:"arguments,omitempty"` // Tool call parameters.
	Result    map[string]any `json:"result,omitempty"`    // Tool execution result.
}

// SessionInput represents values that help initialize a session.
type SessionInput struct {
	// AppName identifies the app.
	AppName string `json:"appName,omitempty"`
	// UserID identifies the user.
	UserID string `json:"userId,omitempty"`
	// State contains the initial state of the session.
	State map[string]any `json:"state,omitempty"`
}
