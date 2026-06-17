//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package debuglog

import "time"

type entry struct {
	Time               time.Time      `json:"time"`
	Sequence           uint64         `json:"sequence"`
	Plugin             string         `json:"plugin"`
	Phase              string         `json:"phase"`
	InvocationID       string         `json:"invocation_id,omitempty"`
	ParentInvocationID string         `json:"parent_invocation_id,omitempty"`
	AgentName          string         `json:"agent_name,omitempty"`
	SessionID          string         `json:"session_id,omitempty"`
	UserID             string         `json:"user_id,omitempty"`
	ModelName          string         `json:"model_name,omitempty"`
	ToolName           string         `json:"tool_name,omitempty"`
	ToolCallID         string         `json:"tool_call_id,omitempty"`
	Error              string         `json:"error,omitempty"`
	Payload            map[string]any `json:"payload,omitempty"`
}
