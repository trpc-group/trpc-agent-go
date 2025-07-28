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

// Package inmemory provides in-memory memory service implementation.
package inmemory

import (
	"context"
	"crypto/md5"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

var _ memory.Service = (*MemoryService)(nil)

const (
	// appName is the name of the application.
	defaultAppName = "default"
	// defaultMemoryLimit is the default limit of memories per user.
	defaultMemoryLimit = 1000
)

// appMemories stores memories for one app.
type appMemories struct {
	mu       sync.RWMutex
	memories map[string]*memory.MemoryEntry // userID -> memoryID -> MemoryEntry
}

// newAppMemories creates a new app memories instance.
func newAppMemories() *appMemories {
	return &appMemories{
		memories: make(map[string]*memory.MemoryEntry),
	}
}

// serviceOpts contains options for memory service.
type serviceOpts struct {
	// memoryLimit is the limit of memories per user.
	memoryLimit int
}

// MemoryService provides an in-memory implementation of MemoryService.
type MemoryService struct {
	mu   sync.RWMutex
	apps map[string]*appMemories // appName -> appMemories
	opts serviceOpts
}

// ServiceOpt is the option for the in-memory memory service.
type ServiceOpt func(*serviceOpts)

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryLimit = limit
	}
}

// NewMemoryService creates a new in-memory memory service.
func NewMemoryService(options ...ServiceOpt) *MemoryService {
	opts := serviceOpts{
		memoryLimit: defaultMemoryLimit,
	}
	for _, option := range options {
		option(&opts)
	}
	return &MemoryService{
		apps: make(map[string]*appMemories),
		opts: opts,
	}
}

// getAppMemories gets or creates app memories for the given app name.
func (s *MemoryService) getAppMemories(appName string) *appMemories {
	s.mu.RLock()
	app, ok := s.apps[appName]
	if ok {
		s.mu.RUnlock()
		return app
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Double check after acquiring write lock.
	if app, ok = s.apps[appName]; ok {
		return app
	}
	app = newAppMemories()
	s.apps[appName] = app
	return app
}

// generateMemoryID generates a unique ID for memory based on content.
func generateMemoryID(memoryData map[string]any) string {
	// Sort keys to ensure consistent ID generation.
	keys := make([]string, 0, len(memoryData))
	for k := range memoryData {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Create a consistent string representation.
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%v", k, memoryData[k]))
	}
	content := strings.Join(parts, "|")

	// Generate MD5 hash.
	hash := md5.Sum([]byte(content))
	return fmt.Sprintf("%x", hash)
}

// createMemoryEntry creates a new MemoryEntry from memory data.
func createMemoryEntry(userID string, memoryData map[string]any) *memory.MemoryEntry {
	now := time.Now()
	id := generateMemoryID(memoryData)

	return &memory.MemoryEntry{
		Memory:    memoryData,
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
	}
}

// AddMemory adds a new memory for a user.
func (s *MemoryService) AddMemory(ctx context.Context, userID string, memoryStr string) error {
	appName := defaultAppName
	app := s.getAppMemories(appName)

	// Create memory data.
	memoryData := map[string]any{
		"memory": memoryStr,
		"input":  memoryStr,
	}

	memoryEntry := createMemoryEntry(userID, memoryData)

	app.mu.Lock()
	defer app.mu.Unlock()

	// Check memory limit.
	if len(app.memories) >= s.opts.memoryLimit {
		return fmt.Errorf("memory limit exceeded for user %s", userID)
	}

	app.memories[memoryEntry.ID] = memoryEntry
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *MemoryService) UpdateMemory(ctx context.Context, userID string, id string, memoryStr string) error {
	appName := defaultAppName
	app := s.getAppMemories(appName)

	app.mu.Lock()
	defer app.mu.Unlock()

	memoryEntry, exists := app.memories[id]
	if !exists {
		return fmt.Errorf("memory with id %s not found", id)
	}

	// Update memory data.
	memoryEntry.Memory["memory"] = memoryStr
	memoryEntry.Memory["input"] = memoryStr
	memoryEntry.UpdatedAt = time.Now()

	app.memories[id] = memoryEntry
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *MemoryService) DeleteMemory(ctx context.Context, userID string, id string) error {
	appName := defaultAppName
	app := s.getAppMemories(appName)

	app.mu.Lock()
	defer app.mu.Unlock()

	if _, exists := app.memories[id]; !exists {
		return fmt.Errorf("memory with id %s not found", id)
	}

	delete(app.memories, id)
	return nil
}

// ClearMemories clears all memories for a user.
func (s *MemoryService) ClearMemories(ctx context.Context, userID string) error {
	appName := defaultAppName
	app := s.getAppMemories(appName)

	app.mu.Lock()
	defer app.mu.Unlock()

	// Remove memories for the specific user.
	for id, memoryEntry := range app.memories {
		if memoryEntry.UserID == userID {
			delete(app.memories, id)
		}
	}
	return nil
}

// ReadMemories reads memories for a user.
func (s *MemoryService) ReadMemories(ctx context.Context, userID string, limit int) ([]*memory.MemoryEntry, error) {
	appName := defaultAppName
	app := s.getAppMemories(appName)

	app.mu.RLock()
	defer app.mu.RUnlock()

	var memories []*memory.MemoryEntry
	for _, memoryEntry := range app.memories {
		if memoryEntry.UserID == userID {
			memories = append(memories, memoryEntry)
		}
	}

	// Sort by creation time (newest first).
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].CreatedAt.After(memories[j].CreatedAt)
	})

	// Apply limit.
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}

	return memories, nil
}

// SearchMemories searches memories for a user.
func (s *MemoryService) SearchMemories(ctx context.Context, userID string, query string) ([]*memory.MemoryEntry, error) {
	appName := defaultAppName
	app := s.getAppMemories(appName)

	app.mu.RLock()
	defer app.mu.RUnlock()

	var results []*memory.MemoryEntry
	queryLower := strings.ToLower(query)

	for _, memoryEntry := range app.memories {
		if memoryEntry.UserID != userID {
			continue
		}

		// Simple string search in memory content.
		if memory, ok := memoryEntry.Memory["memory"].(string); ok {
			if strings.Contains(strings.ToLower(memory), queryLower) {
				results = append(results, memoryEntry)
			}
		}
	}

	// Sort by creation time (newest first).
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	return results, nil
}
