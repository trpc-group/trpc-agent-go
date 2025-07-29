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
	"time"
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
	AddMemory(ctx context.Context, userKey UserKey, memory string, input string, topics []string) error

	// UpdateMemory updates an existing memory for a user.
	UpdateMemory(ctx context.Context, memoryKey MemoryKey, memory string) error

	// DeleteMemory deletes a memory for a user.
	DeleteMemory(ctx context.Context, memoryKey MemoryKey) error

	// ClearMemories clears all memories for a user.
	ClearMemories(ctx context.Context, userKey UserKey) error

	// ReadMemories reads memories for a user.
	ReadMemories(ctx context.Context, userKey UserKey, limit int) ([]*MemoryEntry, error)

	// SearchMemories searches memories for a user.
	SearchMemories(ctx context.Context, userKey UserKey, query string) ([]*MemoryEntry, error)
}

// Memory represents a memory entry with content and metadata.
type Memory struct {
	Memory      string     `json:"memory"`                 // Memory content.
	Topics      []string   `json:"topics,omitempty"`       // Memory topics (array).
	Input       string     `json:"input,omitempty"`        // Input content.
	LastUpdated *time.Time `json:"last_updated,omitempty"` // Last update time.
}

// MemoryEntry represents a memory entry stored in the system.
type MemoryEntry struct {
	ID        string    `json:"id"`         // ID of the memory.
	AppName   string    `json:"app_name"`   // App name.
	Memory    *Memory   `json:"memory"`     // Direct Memory object reference.
	UserID    string    `json:"user_id"`    // User ID.
	CreatedAt time.Time `json:"created_at"` // Creation time.
	UpdatedAt time.Time `json:"updated_at"` // Last update time.
}

// MemoryKey is the key for a memory.
type MemoryKey struct {
	AppName  string // app name
	UserID   string // user id
	MemoryID string // memory id
}

// CheckMemoryKey checks if a memory key is valid.
func (m *MemoryKey) CheckMemoryKey() error {
	return checkMemoryKey(m.AppName, m.UserID, m.MemoryID)
}

// CheckUserKey checks if a user key is valid.
func (m *MemoryKey) CheckUserKey() error {
	return checkUserKey(m.AppName, m.UserID)
}

// UserKey is the key for a user.
type UserKey struct {
	AppName string // app name
	UserID  string // user id
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
