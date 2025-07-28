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

// Package memory provides interfaces and implementations for agent memory systems.
package memory

import (
	"context"
	"time"
)

// Service defines the interface for memory service operations.
type Service interface {
	// AddMemory adds a new memory for a user.
	AddMemory(ctx context.Context, userID string, memory string, input string, topics []string) error

	// UpdateMemory updates an existing memory for a user.
	UpdateMemory(ctx context.Context, userID string, id string, memory string) error

	// DeleteMemory deletes a memory for a user.
	DeleteMemory(ctx context.Context, userID string, id string) error

	// ClearMemories clears all memories for a user.
	ClearMemories(ctx context.Context, userID string) error

	// ReadMemories reads memories for a user.
	ReadMemories(ctx context.Context, userID string, limit int) ([]*MemoryEntry, error)

	// SearchMemories searches memories for a user.
	SearchMemories(ctx context.Context, userID string, query string) ([]*MemoryEntry, error)
}

// Memory represents a memory entry with content and metadata.
type Memory struct {
	Memory      string     `json:"memory"`                 // Memory content.
	MemoryID    string     `json:"memory_id,omitempty"`    // Memory ID.
	Topics      []string   `json:"topics,omitempty"`       // Memory topics (array).
	Input       string     `json:"input,omitempty"`        // Input content.
	LastUpdated *time.Time `json:"last_updated,omitempty"` // Last update time.
}

// MemoryEntry represents a memory entry stored in the system.
type MemoryEntry struct {
	Memory    *Memory   `json:"memory"`     // Direct Memory object reference.
	UserID    string    `json:"user_id"`    // User ID.
	CreatedAt time.Time `json:"created_at"` // Creation time.
	UpdatedAt time.Time `json:"updated_at"` // Update time.
	ID        string    `json:"id"`         // Auto-generated ID.
}
