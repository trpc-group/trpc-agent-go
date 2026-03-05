//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package memory provides interfaces and implementations for agent memory systems.
package memory

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
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

// Session state keys for memory features.
const (
	// SessionStateKeyAutoMemoryLastExtractAt stores the last included event
	// timestamp for auto memory extraction.
	SessionStateKeyAutoMemoryLastExtractAt = "memory:last_extract_at"
)

var (
	// ErrAppNameRequired is the error for app name required.
	ErrAppNameRequired = errors.New("appName is required")
	// ErrUserIDRequired is the error for user id required.
	ErrUserIDRequired = errors.New("userID is required")
	// ErrMemoryIDRequired is the error for memory id required.
	ErrMemoryIDRequired = errors.New("memoryID is required")
)

// EpisodicFields holds optional episodic memory metadata.
// When Kind is empty or MemoryKindFact, episodic fields are ignored.
type EpisodicFields struct {
	Kind         MemoryKind // Memory kind: "fact" or "episode".
	EventTime    *time.Time // When the event occurred (required for episodes).
	Participants []string   // People involved in the event.
	Location     string     // Where the event took place.
}

// Service defines the interface for memory service operations.
type Service interface {
	// AddMemory adds or updates a memory for a user (idempotent).
	AddMemory(ctx context.Context, userKey UserKey, memory string, topics []string) error

	// AddMemoryWithEpisodic adds a memory with optional episodic fields.
	AddMemoryWithEpisodic(ctx context.Context, userKey UserKey, memory string,
		topics []string, ep *EpisodicFields) error

	// UpdateMemory updates an existing memory for a user.
	UpdateMemory(ctx context.Context, memoryKey Key, memory string, topics []string) error

	// UpdateMemoryWithEpisodic updates an existing memory with optional episodic fields.
	UpdateMemoryWithEpisodic(ctx context.Context, memoryKey Key, memory string,
		topics []string, ep *EpisodicFields) error

	// DeleteMemory deletes a memory for a user.
	DeleteMemory(ctx context.Context, memoryKey Key) error

	// ClearMemories clears all memories for a user.
	ClearMemories(ctx context.Context, userKey UserKey) error

	// ReadMemories reads memories for a user.
	ReadMemories(ctx context.Context, userKey UserKey, limit int) ([]*Entry, error)

	// SearchMemories searches memories for a user.
	SearchMemories(ctx context.Context, userKey UserKey, query string) ([]*Entry, error)

	// SearchMemoriesWithOptions searches memories with advanced filtering.
	SearchMemoriesWithOptions(ctx context.Context, userKey UserKey,
		opts SearchOptions) ([]*Entry, error)

	// Tools returns the list of available memory tools.
	Tools() []tool.Tool

	// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async
	// processing. The session contains the full transcript and state for
	// incremental extraction.
	EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error

	// Close closes the service and releases resources.
	// This includes stopping async memory workers if configured.
	Close() error
}

// ToolCreator creates a tool.
// This type can be shared by different implementations.
type ToolCreator func() tool.Tool

// MemoryKind distinguishes between semantic facts and episodic memories.
type MemoryKind string

const (
	// MemoryKindFact represents stable personal attributes, preferences, or background.
	// Example: "User is a software engineer."
	MemoryKindFact MemoryKind = "fact"
	// MemoryKindEpisode represents a specific event that happened at a particular time.
	// Example: "On 2024-05-07, User went hiking at Mt. Fuji with Alice."
	MemoryKindEpisode MemoryKind = "episode"
)

// Memory represents a memory entry with content and metadata.
type Memory struct {
	Memory      string     `json:"memory"`                 // Memory content.
	Topics      []string   `json:"topics,omitempty"`       // Memory topics (array).
	LastUpdated *time.Time `json:"last_updated,omitempty"` // Last update time.

	// Episodic memory fields.
	Kind         MemoryKind `json:"kind,omitempty"`         // Memory kind: "fact" or "episode".
	EventTime    *time.Time `json:"event_time,omitempty"`   // When the event occurred.
	Participants []string   `json:"participants,omitempty"` // People involved in the event.
	Location     string     `json:"location,omitempty"`     // Where the event took place.
}

// Entry represents a memory entry stored in the system.
type Entry struct {
	ID        string    `json:"id"`              // ID is the unique identifier of the memory.
	AppName   string    `json:"app_name"`        // App name is the name of the application.
	Memory    *Memory   `json:"memory"`          // Memory is the memory content.
	UserID    string    `json:"user_id"`         // User ID is the unique identifier of the user.
	CreatedAt time.Time `json:"created_at"`      // CreatedAt is the creation time.
	UpdatedAt time.Time `json:"updated_at"`      // UpdatedAt is the last update time.
	Score     float64   `json:"score,omitempty"` // Score is the similarity score from vector search (0-1).
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

// SearchOptions provides advanced filtering for memory search.
type SearchOptions struct {
	Query      string     // Semantic search query (required).
	Kind       MemoryKind // Filter by memory kind ("fact" or "episode"). Empty means all.
	TimeAfter  *time.Time // Filter episodes with event_time >= TimeAfter.
	TimeBefore *time.Time // Filter episodes with event_time <= TimeBefore.
	MaxResults int        // Override default max results. 0 means use default.

	// SimilarityThreshold sets the minimum similarity score for results.
	// Results below this threshold are filtered out. 0 means use service default.
	SimilarityThreshold float64

	// OrderByEventTime orders results by event_time (ascending) instead
	// of the default embedding similarity order. Only affects episodes
	// that have event_time set; entries without event_time are appended
	// after time-ordered entries. This is useful for temporal sequence
	// questions ("what happened first/next/after X?").
	OrderByEventTime bool

	// KindFallback enables automatic fallback when Kind is set but
	// returns too few results. When true, the service performs a second
	// search without the kind filter and merges both result sets,
	// prioritizing results that match the requested kind. This prevents
	// missed results when the kind classification is uncertain.
	KindFallback bool

	// Deduplicate enables content-based deduplication of search results.
	// When true, near-duplicate memories (high word overlap) are removed,
	// keeping only the highest-scored version. This reduces redundant
	// context in retrieval-augmented generation scenarios.
	Deduplicate bool

	// HybridSearch enables hybrid search mode that combines vector similarity
	// with keyword-based full-text search. When true, both search methods are
	// executed and results are merged using Reciprocal Rank Fusion (RRF).
	// This improves recall for queries containing specific entity names,
	// book titles, or other exact-match terms that vector embeddings may
	// not rank highly.
	HybridSearch bool

	// HybridRRFK is the constant k used in the RRF formula: 1/(k+rank).
	// Higher values give more weight to lower-ranked results.
	// Default is 60 (standard RRF value). Only used when HybridSearch is true.
	HybridRRFK int
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
