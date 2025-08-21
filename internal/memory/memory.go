//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package memory provides internal usage for memory service.
package memory

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// DefaultMemoryLimit is the default limit of memories per user.
	DefaultMemoryLimit = 1000
)

// DefaultEnabledTools are the creators of default memory tools to enable.
// This is shared between different memory service implementations.
var DefaultEnabledTools = map[string]memory.ToolCreator{
	memory.AddToolName:    func(service memory.Service) tool.Tool { return memorytool.NewAddTool(service) },
	memory.UpdateToolName: func(service memory.Service) tool.Tool { return memorytool.NewUpdateTool(service) },
	memory.SearchToolName: func(service memory.Service) tool.Tool { return memorytool.NewSearchTool(service) },
	memory.LoadToolName:   func(service memory.Service) tool.Tool { return memorytool.NewLoadTool(service) },
}

// validToolNames contains all valid memory tool names.
var validToolNames = map[string]struct{}{
	memory.AddToolName:    {},
	memory.UpdateToolName: {},
	memory.DeleteToolName: {},
	memory.ClearToolName:  {},
	memory.SearchToolName: {},
	memory.LoadToolName:   {},
}

// IsValidToolName checks if the given tool name is valid.
func IsValidToolName(toolName string) bool {
	_, ok := validToolNames[toolName]
	return ok
}

// getEnabledMemoryTools extracts the names of enabled memory tools from the memory service.
func getEnabledMemoryTools(memoryService memory.Service) []string {
	if memoryService == nil {
		return []string{}
	}

	tools := memoryService.Tools()
	enabledTools := make([]string, 0, len(tools))

	for _, tool := range tools {
		decl := tool.Declaration()
		if decl != nil {
			enabledTools = append(enabledTools, decl.Name)
		}
	}

	return enabledTools
}

// GenerateInstruction generates a memory-specific instruction based on the memory service.
// This function creates an instruction that guides the LLM on how to use memory tools effectively.
func GenerateInstruction(memoryService memory.Service) string {
	// Get enabled memory tools from the service.
	enabledTools := getEnabledMemoryTools(memoryService)

	// Generate default dynamic instruction based on enabled tools.
	defaultInstruction := `You have access to memory tools to provide personalized assistance. 

IMPORTANT: When users share personal information about themselves (name, preferences, experiences, etc.), 
ALWAYS use memory_add to remember this information. 

Examples of when to use memory_add:
- User tells you their name: 'I am Jack' → use memory_add to remember 'User is named Jack'
- User shares preferences: 'I like coffee' → use memory_add to remember 'User likes coffee'
- User shares experiences: 'I had beef tonight' → use memory_add to remember 'User had beef for dinner and enjoyed it'
- User mentions their job: 'I work as a developer' → use memory_add to remember 'User works as a developer'
- User shares their location: 'I live in Beijing' → use memory_add to remember 'User lives in Beijing'`

	// Add tool-specific instructions based on enabled tools.
	for _, toolName := range enabledTools {
		switch toolName {
		case memory.AddToolName:
			// Already covered in the main instruction.
		case memory.SearchToolName:
			defaultInstruction += `

When users ask about themselves or their preferences, use memory_search to find relevant information.`
		case memory.LoadToolName:
			defaultInstruction += `

When users ask 'tell me about myself' or similar, use memory_load to get an overview.`
		case memory.UpdateToolName:
			defaultInstruction += `

When users want to update existing information, use memory_update with the memory_id.`
		case memory.DeleteToolName:
			defaultInstruction += `

When users want to remove specific memories, use memory_delete with the memory_id.`
		case memory.ClearToolName:
			defaultInstruction += `

When users want to clear all their memories, use memory_clear to remove all stored information.`
		}
	}

	defaultInstruction += `

Available memory tools: ` + strings.Join(enabledTools, ", ") + `.

Be helpful, conversational, and proactive about remembering user information. 
Always strive to create memories that capture the essence of what the user shares, 
making future interactions more personalized and contextually relevant.`

	// If the service provides a custom instruction builder, use it.
	if memoryService != nil {
		if builtInstruction, ok := memoryService.BuildInstruction(
			enabledTools, defaultInstruction,
		); ok && builtInstruction != "" {
			return builtInstruction
		}
	}

	return defaultInstruction
}
