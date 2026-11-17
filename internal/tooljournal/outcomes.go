//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tooljournal

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Index links recovery evidence to exact source events, not reused tool IDs.
func Index(sess *session.Session) (map[string]Record, error) {
	records, err := Records(sess)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Record, len(records))
	for _, r := range records {
		if r.ID == "" || r.SourceEventID == "" || r.Response == nil {
			return nil, fmt.Errorf("incomplete tool execution record %q", r.ID)
		}
		if _, ok := out[r.SourceEventID]; ok {
			return nil, fmt.Errorf("ambiguous tool execution source %q", r.SourceEventID)
		}
		seen := make(map[string]bool)
		for _, c := range r.Calls {
			if c.Tool.ID == "" || seen[c.Tool.ID] {
				return nil, fmt.Errorf("ambiguous tool execution call %q", c.Tool.ID)
			}
			seen[c.Tool.ID] = true
			switch c.Status {
			case "not_started", "dispatched", "completed", "failed", "unknown":
			default:
				return nil, fmt.Errorf("unknown tool execution status %q", c.Status)
			}
		}
		out[r.SourceEventID] = r
	}
	return out, nil
}

// Outcome projects evidence into a tool response. Framework explanations are
// explicitly distinguished from actual results and never claim rollback.
func Outcome(c Call) ([]model.Choice, error) {
	if c.Status == "completed" && len(c.Choices) > 0 {
		rsp := (&model.Response{Choices: c.Choices}).Clone()
		return rsp.Choices, nil
	}
	var content string
	switch c.Status {
	case "failed":
		content = "[framework] Tool returned an error: " + c.Error + ". Side effects are not assumed rolled back."
	case "not_started":
		content = "[framework] Tool was not dispatched by this run before it stopped."
	case "dispatched":
		content = "[framework] Tool execution was admitted; its outcome is unknown and it may still be running. Side effects may have occurred. Verify before repeating."
	case "unknown":
		content = "[framework] Tool execution exited without a confirmed complete outcome. Side effects may have occurred. Verify before repeating."
	default:
		return nil, fmt.Errorf("tool %q has no valid recorded outcome", c.Tool.ID)
	}
	return []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: c.Tool.ID, ToolName: c.Tool.Function.Name, Content: content}}}, nil
}
