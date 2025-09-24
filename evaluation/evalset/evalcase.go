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
	"encoding/json"
	"time"
)

// EvalCase represents a single evaluation case.
type EvalCase struct {
	// EvalID uniquely identifies this evaluation case.
	EvalID string `json:"evalId"`
	// Conversation contains the sequence of invocations.
	Conversation []*Invocation `json:"conversation"`
	// SessionInput contains initialization data for the session.
	SessionInput *SessionInput `json:"sessionInput,omitempty"`
	// CreationTimestamp when this eval case was created.
	CreationTimestamp EpochTime `json:"creationTimestamp"`
}

// Invocation represents a single invocation in a conversation.
type Invocation struct {
	// InvocationID uniquely identifies this invocation.
	InvocationID string `json:"invocationId,omitempty"`
	// UserContent represents the user's input.
	UserContent *Content `json:"userContent"`
	// FinalResponse represents the agent's final response.
	FinalResponse *Content `json:"finalResponse,omitempty"`
	// IntermediateData contains intermediate steps during execution.
	IntermediateData *IntermediateData `json:"intermediateData,omitempty"`
	// CreationTimestamp when this invocation was created.
	CreationTimestamp EpochTime `json:"creationTimestamp"`
}

type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts,omitempty"`
}

type Part struct {
	Text string `json:"text,omitempty"`
}

// IntermediateData contains intermediate execution data.
type IntermediateData struct {
	// ToolUses represents tool calls made during execution.
	ToolUses []FunctionCall `json:"toolUses,omitempty"`
	// IntermediateResponses from sub-agents or intermediate steps.
	IntermediateResponses []IntermediateMessage `json:"intermediateResponses,omitempty"`
}

// SessionInput represents values that help initialize a session.
type SessionInput struct {
	// AppName identifies the app.
	AppName string `json:"appName"`
	// UserID identifies the user.
	UserID string `json:"userId"`
	// State contains the initial state of the session.
	State map[string]interface{} `json:"state,omitempty"`
}

// IntermediateMessage preserves author and parts for intermediate agent outputs.
type IntermediateMessage struct {
	// Author is typically the sub-agent name.
	Author string `json:"author"`
	// Parts are multimodal content parts (text/image/audio/file).
	Parts []Part `json:"parts,omitempty"`
}

// FunctionCall mirrors GenAI FunctionCall shape for ADK compatibility.
type FunctionCall struct {
	// ID is the unique id of the function call.
	ID string `json:"id,omitempty"`
	// Name is the name of the function to call.
	Name string `json:"name"`
	// Args are the function parameters as a JSON object.
	Args map[string]interface{} `json:"args,omitempty"`
}

// EpochTime wraps time.Time to (un)marshal as unix seconds (float) like ADK.
type EpochTime struct{ time.Time }

// MarshalJSON implements json.Marshaler to encode time as unix seconds (float).
func (t EpochTime) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte("0"), nil
	}
	unixSeconds := float64(t.Time.UnixNano()) / float64(time.Second)
	return json.Marshal(unixSeconds)
}

// UnmarshalJSON implements json.Unmarshaler to decode unix seconds (float).
func (t *EpochTime) UnmarshalJSON(b []byte) error {
	var unixSeconds float64
	if err := json.Unmarshal(b, &unixSeconds); err != nil {
		return err
	}
	t.Time = time.Unix(0, int64(unixSeconds*float64(time.Second))).UTC()
	return nil
}
