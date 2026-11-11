//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package systemone

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
)

type reviewState struct {
	Action     actionState       `json:"action"`
	Transcript []transcriptState `json:"transcript"`
	Context    json.RawMessage   `json:"context,omitempty"`
}

type actionState struct {
	ToolName        string          `json:"tool_name"`
	ToolDescription string          `json:"tool_description"`
	Arguments       json.RawMessage `json:"arguments"`
}

type transcriptState struct {
	Role    model.Role `json:"role"`
	Content string     `json:"content"`
}

func buildState(req *review.Request) (*reviewState, bool, error) {
	if req == nil {
		return nil, false, fmt.Errorf("request is nil")
	}
	if strings.TrimSpace(req.Action.ToolName) == "" {
		return nil, false, fmt.Errorf("action tool name is empty")
	}
	if !utf8.ValidString(req.Action.ToolName) || !utf8.ValidString(req.Action.ToolDescription) {
		return nil, false, fmt.Errorf("action text is not valid UTF-8")
	}
	args := req.Action.Arguments
	if len(args) > 0 && (!utf8.Valid(args) || !json.Valid(args)) {
		return nil, false, fmt.Errorf("action arguments must be valid UTF-8 JSON")
	}
	if req.Context != nil && len(req.Context.TranscriptInfo) != len(req.Transcript) {
		return nil, false, fmt.Errorf("transcript metadata length mismatch")
	}
	state := &reviewState{
		Action: actionState{ToolName: req.Action.ToolName, ToolDescription: req.Action.ToolDescription,
			Arguments: append(json.RawMessage(nil), args...)},
		Transcript: make([]transcriptState, 0, len(req.Transcript)),
	}
	if req.Context != nil {
		data, err := json.Marshal(req.Context)
		if err != nil {
			return nil, false, fmt.Errorf("marshal review context: %w", err)
		}
		state.Context = data
	}
	var hasUser bool
	for i, entry := range req.Transcript {
		if !utf8.ValidString(string(entry.Role)) || !utf8.ValidString(entry.Content) {
			return nil, false, fmt.Errorf("transcript text is not valid UTF-8")
		}
		state.Transcript = append(state.Transcript, transcriptState{Role: entry.Role, Content: entry.Content})
		if entry.Role == model.RoleUser && strings.TrimSpace(entry.Content) != "" &&
			entry.Content != "[non-text content omitted]" &&
			(req.Context == nil || req.Context.TranscriptInfo[i].Origin == review.OriginUserInput) {
			hasUser = true
		}
	}
	return state, hasUser, nil
}
