//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package memory provides memory-related tools for the agent system.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryAddTool provides a tool for LLM to add memories.
type MemoryAddTool struct {
	memoryService memory.Service
	userID        string
}

// NewMemoryAddTool creates a new MemoryAddTool.
func NewMemoryAddTool(memoryService memory.Service, userID string) *MemoryAddTool {
	return &MemoryAddTool{
		memoryService: memoryService,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *MemoryAddTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "memory_add",
		Description: "Add a new memory for the user. Use this when you want to remember important information about the user.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory": {
					Type:        "string",
					Description: "The memory content to store. Should be a concise summary of important information about the user.",
				},
				"input": {
					Type:        "string",
					Description: "The original user input that led to this memory.",
				},
				"topics": {
					Type:        "array",
					Items:       &tool.Schema{Type: "string"},
					Description: "Optional topics for categorizing the memory. Can be multiple topics.",
				},
			},
			Required: []string{"memory", "input"},
		},
	}
}

// Call executes the memory add tool.
func (m *MemoryAddTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.memoryService == nil {
		return nil, errors.New("memory service not available")
	}

	var args struct {
		Memory string   `json:"memory"`
		Input  string   `json:"input"`
		Topics []string `json:"topics,omitempty"`
	}

	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %v", err)
	}

	if args.Memory == "" {
		return nil, errors.New("memory content cannot be empty")
	}

	if args.Input == "" {
		return nil, errors.New("input content cannot be empty")
	}

	// Add memory to the service.
	err := m.memoryService.AddMemory(ctx, m.userID, args.Memory, args.Input, args.Topics)
	if err != nil {
		return nil, fmt.Errorf("failed to add memory: %v", err)
	}

	return map[string]any{
		"success": true,
		"message": "Memory added successfully",
		"memory":  args.Memory,
		"input":   args.Input,
		"topics":  args.Topics,
	}, nil
}
