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
	ID           string    `json:"id"`                     // ID is the memory ID.
	Memory       string    `json:"memory"`                 // Memory is the memory content.
	Topics       []string  `json:"topics"`                 // Topics is the memory topics.
	Created      time.Time `json:"created"`                // Created is the creation time.
	Kind         string    `json:"kind,omitempty"`         // Kind is the memory kind (fact/episode).
	EventTime    string    `json:"event_time,omitempty"`   // EventTime is when the event occurred.
	Participants []string  `json:"participants,omitempty"` // Participants involved in the event.
	Location     string    `json:"location,omitempty"`     // Location where the event took place.
	Score        float64   `json:"score,omitempty"`        // Score is the similarity score from vector search (0-1).
}

// AddMemoryRequest represents the input for the add memory tool.
type AddMemoryRequest struct {
	Memory       string   `json:"memory" description:"The memory content to store. Should be a brief third-person statement that captures key information about the user"`
	Topics       []string `json:"topics,omitempty" description:"Optional topics for categorizing the memory"`
	MemoryKind   string   `json:"memory_kind,omitempty" jsonschema:"enum=fact,enum=episode" description:"Memory type: 'fact' for stable personal attributes or 'episode' for specific events. Defaults to 'fact'"`
	EventTime    string   `json:"event_time,omitempty" description:"When the event occurred (ISO 8601: YYYY-MM-DD or YYYY-MM-DDTHH:MM:SS). Required for episodes. Must be an absolute date - never use relative time words."`
	Participants []string `json:"participants,omitempty" description:"People involved in the event. Used for episodes."`
	Location     string   `json:"location,omitempty" description:"Where the event took place. Used for episodes."`
}

// AddMemoryResponse represents the response from memory_add tool.
type AddMemoryResponse struct {
	Message string   `json:"message"` // Message is the success message.
	Memory  string   `json:"memory"`  // Memory is the memory content that was added.
	Topics  []string `json:"topics"`  // Topics is the topics associated with the memory.
}

// UpdateMemoryRequest represents the input for the update memory tool.
type UpdateMemoryRequest struct {
	MemoryID     string   `json:"memory_id" description:"The ID of the memory to update"`
	Memory       string   `json:"memory" description:"The updated memory content"`
	Topics       []string `json:"topics,omitempty" description:"Optional topics for categorizing the memory"`
	MemoryKind   string   `json:"memory_kind,omitempty" jsonschema:"enum=fact,enum=episode" description:"Memory type: 'fact' or 'episode'"`
	EventTime    string   `json:"event_time,omitempty" description:"When the event occurred (ISO 8601). Required for episodes."`
	Participants []string `json:"participants,omitempty" description:"People involved in the event."`
	Location     string   `json:"location,omitempty" description:"Where the event took place."`
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
	MemoryID string `json:"memory_id" description:"The ID of the memory to delete"`
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
	Reason string `json:"reason,omitempty" description:"Optional reason for clearing all memories"`
}

// ClearMemoryResponse represents the response from memory_clear tool.
type ClearMemoryResponse struct {
	Message string `json:"message"` // Message is the success message.
}

// SearchMemoryRequest represents the input for the search memory tool.
type SearchMemoryRequest struct {
	Query            string `json:"query" description:"The search query to find relevant memories"`
	Kind             string `json:"kind,omitempty" jsonschema:"enum=fact,enum=episode" description:"Filter by memory kind: 'fact' or 'episode'. Empty means all."`
	TimeAfter        string `json:"time_after,omitempty" description:"Filter episodes with event_time on or after this date (ISO 8601: YYYY-MM-DD)"`
	TimeBefore       string `json:"time_before,omitempty" description:"Filter episodes with event_time on or before this date (ISO 8601: YYYY-MM-DD)"`
	OrderByEventTime bool   `json:"order_by_event_time,omitempty" description:"When true order results by event_time ascending instead of relevance. Useful for temporal sequence questions (what happened first/next/after)."`
}

// SearchMemoryResponse represents the response from memory_search tool.
type SearchMemoryResponse struct {
	Query   string   `json:"query"`   // Query is the search query that was used.
	Results []Result `json:"results"` // Results is the search results.
	Count   int      `json:"count"`   // Count is the number of results found.
}

// LoadMemoryRequest represents the input for the load memory tool.
type LoadMemoryRequest struct {
	Limit int `json:"limit,omitempty" description:"Maximum number of memories to load (default: 10)"`
}

// LoadMemoryResponse represents the response from memory_load tool.
type LoadMemoryResponse struct {
	Limit   int      `json:"limit"`   // Limit is the limit that was used.
	Results []Result `json:"results"` // Results is the loaded memories.
	Count   int      `json:"count"`   // Count is the number of memories loaded.
}
