//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tool provides memory-related tools for the agent system.
package tool

import (
	"time"
)

// Result represents a single memory result.
type Result struct {
	ID      string    `json:"id"`      // ID is the memory ID.
	Memory  string    `json:"memory"`  // Memory is the memory content.
	Topics  []string  `json:"topics"`  // Topics is the memory topics.
	Created time.Time `json:"created"` // Created is the creation time.
}

// AddMemoryRequest represents the input for the add memory tool.
type AddMemoryRequest struct {
	Memory string   `json:"memory" jsonschema:"description=The memory content to store. Should be a brief. third-person statement that captures key information about the user"`
	Topics []string `json:"topics,omitempty" jsonschema:"description=Optional topics for categorizing the memory"`
}

// AddMemoryResponse represents the response from memory_add tool.
type AddMemoryResponse struct {
	Message string   `json:"message"` // Message is the success message.
	Memory  string   `json:"memory"`  // Memory is the memory content that was added.
	Topics  []string `json:"topics"`  // Topics is the topics associated with the memory.
}

// UpdateMemoryRequest represents the input for the update memory tool.
type UpdateMemoryRequest struct {
	MemoryID string   `json:"memory_id" jsonschema:"description=The ID of the memory to update"`
	Memory   string   `json:"memory" jsonschema:"description=The updated memory content"`
	Topics   []string `json:"topics,omitempty" jsonschema:"description=Optional topics for categorizing the memory"`
}

// UpdateMemoryResponse represents the response from memory_update tool.
type UpdateMemoryResponse struct {
	Message  string   `json:"message"`   // Message is the success message.
	MemoryID string   `json:"memory_id"` // MemoryID is the ID of the updated memory.
	Memory   string   `json:"memory"`    // Memory is the updated memory content.
	Topics   []string `json:"topics"`    // Topics is the topics associated with the memory.
}

// DeleteMemoryRequest represents the input for the delete memory tool.
type DeleteMemoryRequest struct {
	MemoryID string `json:"memory_id" jsonschema:"description=The ID of the memory to delete"`
}

// DeleteMemoryResponse represents the response from memory_delete tool.
type DeleteMemoryResponse struct {
	Message  string `json:"message"`   // Message is the success message.
	MemoryID string `json:"memory_id"` // MemoryID is the ID of the deleted memory.
}

// ClearMemoryRequest represents the input for the clear memory tool.
// Having at least one optional field ensures the generated JSON Schema includes
// a non-empty properties object for compatibility with strict validators.
type ClearMemoryRequest struct {
	Reason string `json:"reason,omitempty" jsonschema:"description=Optional reason for clearing all memories"`
}

// ClearMemoryResponse represents the response from memory_clear tool.
type ClearMemoryResponse struct {
	Message string `json:"message"` // Message is the success message.
}

// SearchMemoryRequest represents the input for the search memory tool.
type SearchMemoryRequest struct {
	Query string `json:"query" jsonschema:"description=The search query to find relevant memories"`
}

// SearchMemoryResponse represents the response from memory_search tool.
type SearchMemoryResponse struct {
	Query   string   `json:"query"`   // Query is the search query that was used.
	Results []Result `json:"results"` // Results is the search results.
	Count   int      `json:"count"`   // Count is the number of results found.
}

// LoadMemoryRequest represents the input for the load memory tool.
type LoadMemoryRequest struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum number of memories to load (default: 10)"`
}

// LoadMemoryResponse represents the response from memory_load tool.
type LoadMemoryResponse struct {
	Limit   int      `json:"limit"`   // Limit is the limit that was used.
	Results []Result `json:"results"` // Results is the loaded memories.
	Count   int      `json:"count"`   // Count is the number of memories loaded.
}
