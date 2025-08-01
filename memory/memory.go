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

// Package memory provides interfaces and implementations for agent memory systems.
package memory

import (
	"context"
	"errors"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Tool names for memory tools.
const (
	AddToolName    = "memory_add"
	UpdateToolName = "memory_update"
	DeleteToolName = "memory_delete"
	ClearToolName  = "memory_clear"
	SearchToolName = "memory_search"
	LoadToolName   = "memory_load"
)

var (
	// ErrAppNameRequired is the error for app name required.
	ErrAppNameRequired = errors.New("appName is required")
	// ErrUserIDRequired is the error for user id required.
	ErrUserIDRequired = errors.New("userID is required")
	// ErrMemoryIDRequired is the error for memory id required.
	ErrMemoryIDRequired = errors.New("memoryID is required")
)

// Service defines the interface for memory service operations.
type Service interface {
	// AddMemory adds a new memory for a user.
	AddMemory(ctx context.Context, userKey UserKey, memory string, topics []string) error

	// UpdateMemory updates an existing memory for a user.
	UpdateMemory(ctx context.Context, memoryKey Key, memory string, topics []string) error

	// DeleteMemory deletes a memory for a user.
	DeleteMemory(ctx context.Context, memoryKey Key) error

	// ClearMemories clears all memories for a user.
	ClearMemories(ctx context.Context, userKey UserKey) error

	// ReadMemories reads memories for a user.
	ReadMemories(ctx context.Context, userKey UserKey, limit int) ([]*Entry, error)

	// SearchMemories searches memories for a user.
	SearchMemories(ctx context.Context, userKey UserKey, query string) ([]*Entry, error)

	// Tools returns the list of available memory tools.
	Tools() []tool.Tool
}

// Memory represents a memory entry with content and metadata.
type Memory struct {
	Memory      string     `json:"memory"`                 // Memory content.
	Topics      []string   `json:"topics,omitempty"`       // Memory topics (array).
	LastUpdated *time.Time `json:"last_updated,omitempty"` // Last update time.
}

// Entry represents a memory entry stored in the system.
type Entry struct {
	ID        string    `json:"id"`         // ID is the unique identifier of the memory.
	AppName   string    `json:"app_name"`   // App name is the name of the application.
	Memory    *Memory   `json:"memory"`     // Memory is the memory content.
	UserID    string    `json:"user_id"`    // User ID is the unique identifier of the user.
	CreatedAt time.Time `json:"created_at"` // CreatedAt is the creation time.
	UpdatedAt time.Time `json:"updated_at"` // UpdatedAt is the last update time.
}

// Key is the key for a memory.
type Key struct {
	AppName  string // AppName is the name of the application.
	UserID   string // UserID is the unique identifier of the user.
	MemoryID string // MemoryID is the unique identifier of the memory.
}

// CheckMemoryKey checks if a memory key is valid.
func (m *Key) CheckMemoryKey() error {
	return checkMemoryKey(m.AppName, m.UserID, m.MemoryID)
}

// CheckUserKey checks if a user key is valid.
func (m *Key) CheckUserKey() error {
	return checkUserKey(m.AppName, m.UserID)
}

// UserKey is the key for a user.
type UserKey struct {
	AppName string // AppName is the name of the application.
	UserID  string // UserID is the unique identifier of the user.
}

// CheckUserKey checks if a user key is valid.
func (u *UserKey) CheckUserKey() error {
	return checkUserKey(u.AppName, u.UserID)
}

func checkMemoryKey(appName, userID, memoryID string) error {
	if appName == "" {
		return ErrAppNameRequired
	}
	if userID == "" {
		return ErrUserIDRequired
	}
	if memoryID == "" {
		return ErrMemoryIDRequired
	}
	return nil
}

func checkUserKey(appName, userID string) error {
	if appName == "" {
		return ErrAppNameRequired
	}
	if userID == "" {
		return ErrUserIDRequired
	}
	return nil
}

// getEnabledMemoryTools extracts the names of enabled memory tools from the memory service.
func getEnabledMemoryTools(memoryService Service) []string {
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
func GenerateInstruction(memoryService Service) string {
	// Get enabled memory tools from the service.
	enabledTools := getEnabledMemoryTools(memoryService)

	// Generate dynamic instruction based on enabled tools.
	instruction := `You have access to memory tools to provide personalized assistance. 

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
		case AddToolName:
			// Already covered in the main instruction.
		case SearchToolName:
			instruction += `

When users ask about themselves or their preferences, use memory_search to find relevant information.`
		case LoadToolName:
			instruction += `

When users ask 'tell me about myself' or similar, use memory_load to get an overview.`
		case UpdateToolName:
			instruction += `

When users want to update existing information, use memory_update with the memory_id.`
		case DeleteToolName:
			instruction += `

When users want to remove specific memories, use memory_delete with the memory_id.`
		case ClearToolName:
			instruction += `

When users want to clear all their memories, use memory_clear to remove all stored information.`
		}
	}

	instruction += `

Available memory tools: ` + strings.Join(enabledTools, ", ") + `.

Be helpful, conversational, and proactive about remembering user information. 
Always strive to create memories that capture the essence of what the user shares, 
making future interactions more personalized and contextually relevant.`

	return instruction
}

// validToolNames contains all valid memory tool names.
var validToolNames = map[string]struct{}{
	AddToolName:    {},
	UpdateToolName: {},
	DeleteToolName: {},
	ClearToolName:  {},
	SearchToolName: {},
	LoadToolName:   {},
}

// IsValidToolName checks if the given tool name is valid.
func IsValidToolName(toolName string) bool {
	_, ok := validToolNames[toolName]
	return ok
}
