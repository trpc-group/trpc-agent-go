//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type handoffTool struct {
}

func newHandoffTool() tool.Tool {
	return &handoffTool{}
}

func (h *handoffTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        handoffToolName,
		Description: "Hand off a task to a selected AgentTool-wrapped GraphAgent.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"agent_id", "task"},
			Properties: map[string]*tool.Schema{
				"agent_id": {Type: "string", Description: "The target agent id."},
				"task":     {Type: "string", Description: "The task to hand off."},
			},
		},
	}
}

func decodeValue[T any](raw any) (T, error) {
	if typed, ok := raw.(T); ok {
		return typed, nil
	}
	var value T
	b, err := json.Marshal(raw)
	if err != nil {
		return value, err
	}
	return value, json.Unmarshal(b, &value)
}
