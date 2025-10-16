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
	"encoding/json"
	"time"

	"google.golang.org/genai"
)

const (
	// zeroEpochLiteral is the literal for zero epoch.
	zeroEpochLiteral = "0"
	// nanosecondsPerSecond is the number of nanoseconds per second.
	nanosecondsPerSecond = float64(time.Second)
)

// EvalCase represents a single evaluation case.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvalCase struct {
	// EvalID uniquely identifies this evaluation case.
	EvalID string `json:"evalId"`
	// Conversation contains the sequence of invocations.
	Conversation []*Invocation `json:"conversation"`
	// SessionInput contains initialization data for the session.
	SessionInput *SessionInput `json:"sessionInput"`
	// CreationTimestamp when this eval case was created.
	CreationTimestamp *EpochTime `json:"creationTimestamp"`
}

// Invocation represents a single invocation in a conversation.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type Invocation struct {
	// InvocationID uniquely identifies this invocation.
	InvocationID string `json:"invocationId"`
	// UserContent represents the user's input.
	UserContent *genai.Content `json:"userContent"`
	// FinalResponse represents the agent's final response.
	FinalResponse *genai.Content `json:"finalResponse"`
	// IntermediateData contains intermediate steps during execution.
	IntermediateData *IntermediateData `json:"intermediateData"`
	// CreationTimestamp when this invocation was created.
	CreationTimestamp *EpochTime `json:"creationTimestamp"`
}

// IntermediateData contains intermediate execution data.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type IntermediateData struct {
	// ToolUses represents tool calls made during execution.
	ToolUses []*genai.FunctionCall `json:"toolUses"`
	// ToolResponses represents tool responses made during execution.
	ToolResponses []*genai.FunctionResponse `json:"toolResponses"`
	// IntermediateResponses represents intermediate responses, including text responses and tool responses.
	// For each intermediate response, the first element is the author string,
	// and the second element is the genai.Part slice.
	IntermediateResponses [][]interface{} `json:"intermediateResponses"`
}

// SessionInput represents values that help initialize a session.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type SessionInput struct {
	// AppName identifies the app.
	AppName string `json:"appName"`
	// UserID identifies the user.
	UserID string `json:"userId"`
	// State contains the initial state of the session.
	State map[string]interface{} `json:"state"`
}

// EpochTime wraps time.Time to (un)marshal as unix seconds (float) like ADK.
type EpochTime struct{ time.Time }

// MarshalJSON implements json.Marshaler to encode time as unix seconds (float).
func (t EpochTime) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte(zeroEpochLiteral), nil
	}
	unixSeconds := float64(t.Time.UnixNano()) / nanosecondsPerSecond
	return json.Marshal(unixSeconds)
}

// UnmarshalJSON implements json.Unmarshaler to decode unix seconds (float).
func (t *EpochTime) UnmarshalJSON(b []byte) error {
	var unixSeconds float64
	if err := json.Unmarshal(b, &unixSeconds); err != nil {
		return err
	}
	t.Time = time.Unix(0, int64(unixSeconds*nanosecondsPerSecond)).UTC()
	return nil
}
