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

// Package memory provides memory-related tools for the agent system.
package memory

import "time"

// MemoryResult represents a single memory result.
type MemoryResult struct {
	ID      string    `json:"id"`      // ID is the memory ID.
	Memory  string    `json:"memory"`  // Memory is the memory content.
	Topics  []string  `json:"topics"`  // Topics is the memory topics.
	Created time.Time `json:"created"` // Created is the creation time.
}

// AddMemoryResponse represents the response from memory_add tool.
type AddMemoryResponse struct {
	Success bool     `json:"success"` // Success is whether the operation was successful.
	Message string   `json:"message"` // Message is the success or error message.
	Memory  string   `json:"memory"`  // Memory is the memory content that was added.
	Input   string   `json:"input"`   // Input is the original user input.
	Topics  []string `json:"topics"`  // Topics is the topics associated with the memory.
}

// UpdateMemoryResponse represents the response from memory_update tool.
type UpdateMemoryResponse struct {
	Success  bool     `json:"success"`   // Success is whether the operation was successful.
	Message  string   `json:"message"`   // Message is the success or error message.
	MemoryID string   `json:"memory_id"` // MemoryID is the ID of the updated memory.
	Memory   string   `json:"memory"`    // Memory is the updated memory content.
	Input    string   `json:"input"`     // Input is the original user input.
	Topics   []string `json:"topics"`    // Topics is the topics associated with the memory.
}

// DeleteMemoryResponse represents the response from memory_delete tool.
type DeleteMemoryResponse struct {
	Success  bool   `json:"success"`   // Success is whether the operation was successful.
	Message  string `json:"message"`   // Message is the success or error message.
	MemoryID string `json:"memory_id"` // MemoryID is the ID of the deleted memory.
}

// ClearMemoryResponse represents the response from memory_clear tool.
type ClearMemoryResponse struct {
	Success bool   `json:"success"` // Success is whether the operation was successful.
	Message string `json:"message"` // Message is the success or error message.
}

// SearchMemoryResponse represents the response from memory_search tool.
type SearchMemoryResponse struct {
	Success bool           `json:"success"` // Success is whether the operation was successful.
	Query   string         `json:"query"`   // Query is the search query that was used.
	Results []MemoryResult `json:"results"` // Results is the search results.
	Count   int            `json:"count"`   // Count is the number of results found.
}

// LoadMemoryResponse represents the response from memory_load tool.
type LoadMemoryResponse struct {
	Success bool           `json:"success"` // Success is whether the operation was successful.
	Limit   int            `json:"limit"`   // Limit is the limit that was used.
	Results []MemoryResult `json:"results"` // Results is the loaded memories.
	Count   int            `json:"count"`   // Count is the number of memories loaded.
}
