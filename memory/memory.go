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

// Service defines the interface for memory service operations.
type Service interface {
	// AddMemory adds or updates a memory for a user (idempotent).
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
