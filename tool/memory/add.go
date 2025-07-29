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
	appName       string
}

// NewMemoryAddTool creates a new MemoryAddTool.
func NewMemoryAddTool(memoryService memory.Service, appName string, userID string) *MemoryAddTool {
	return &MemoryAddTool{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// Declaration returns the tool declaration.
func (m *MemoryAddTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "memory_add",
		Description: "Add a new memory for the user. Use this when you want to remember important information " +
			"about the user that could personalize future interactions. Memories should include details like: " +
			"personal facts (name, age, occupation, location, interests, preferences), significant life events, " +
			"important context about the user's situation, what the user likes/dislikes, opinions, beliefs, values, " +
			"or any other details that provide valuable insights into the user's personality or needs. Create brief, " +
			"third-person statements that encapsulate the most important aspect of the user's input " +
			"without adding extraneous information.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"memory": {
					Type: "string",
					Description: "The memory content to store. Should be a brief, third-person statement that " +
						"captures key information about the user. Example: 'User's name is John Doe' or " +
						"'User likes coffee and works as a developer'.",
				},
				"input": {
					Type:        "string",
					Description: "The original user input that led to this memory.",
				},
				"topics": {
					Type:  "array",
					Items: &tool.Schema{Type: "string"},
					Description: "Optional topics for categorizing the memory (e.g. ['name', 'hobbies', 'location', " +
						"'work', 'preferences']). Can be multiple topics.",
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

	// Create user key.
	userKey := memory.UserKey{
		AppName: m.appName,
		UserID:  m.userID,
	}

	// Add memory to the service.
	err := m.memoryService.AddMemory(ctx, userKey, args.Memory, args.Input, args.Topics)
	if err != nil {
		return nil, fmt.Errorf("failed to add memory: %v", err)
	}

	return AddMemoryResponse{
		Success: true,
		Message: "Memory added successfully",
		Memory:  args.Memory,
		Input:   args.Input,
		Topics:  args.Topics,
	}, nil
}
