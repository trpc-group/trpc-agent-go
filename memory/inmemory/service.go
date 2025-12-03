//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides in-memory memory service implementation.
package inmemory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*MemoryService)(nil)

// appMemories represents memories for a specific app.
type appMemories struct {
	mu       sync.RWMutex
	memories map[string]map[string]*memory.Entry // userID -> memoryID -> MemoryEntry
}

// newAppMemories creates a new app memories instance.
func newAppMemories() *appMemories {
	return &appMemories{
		memories: make(map[string]map[string]*memory.Entry),
	}
}

// MemoryService is an in-memory implementation of memory.Service.
type MemoryService struct {
	// mu is the mutex for the service.
	mu sync.RWMutex
	// apps are the app memories.
	apps map[string]*appMemories
	// opts are the service options.
	opts serviceOpts
	// cachedTools caches created tools to avoid recreating them.
	cachedTools map[string]tool.Tool
}

// NewMemoryService creates a new in-memory memory service.
func NewMemoryService(options ...ServiceOpt) *MemoryService {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
	}

	return &MemoryService{
		apps:        make(map[string]*appMemories),
		opts:        opts,
		cachedTools: make(map[string]tool.Tool),
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
func generateMemoryID(memory *memory.Memory) string {
	// Create a consistent string representation for ID generation.
	content := fmt.Sprintf("memory:%s", memory.Memory)
	if len(memory.Topics) > 0 {
		content += fmt.Sprintf("|topics:%s", strings.Join(memory.Topics, ","))
	}

	// Generate SHA256 hash.
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", hash)
}

// createMemoryEntry creates a new MemoryEntry from memory data.
func createMemoryEntry(appName, userID, memoryStr string, topics []string) *memory.Entry {
	now := time.Now()

	// Create Memory object.
	memoryObj := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}

	return &memory.Entry{
		ID:        generateMemoryID(memoryObj), // Generate ID.
		AppName:   appName,
		UserID:    userID,
		Memory:    memoryObj,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// AddMemory adds a new memory for a user.
func (s *MemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	app := s.getAppMemories(userKey.AppName)

	// Create memory entry with provided topics.
	memoryEntry := createMemoryEntry(userKey.AppName, userKey.UserID, memoryStr, topics)

	app.mu.Lock()
	defer app.mu.Unlock()

	// Check memory limit.
	if len(app.memories[userKey.UserID]) >= s.opts.memoryLimit {
		return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
			userKey.UserID, s.opts.memoryLimit, len(app.memories[userKey.UserID]))
	}

	// Initialize user map if not exists.
	if app.memories[userKey.UserID] == nil {
		app.memories[userKey.UserID] = make(map[string]*memory.Entry)
	}

	app.memories[userKey.UserID][memoryEntry.ID] = memoryEntry
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *MemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	app := s.getAppMemories(memoryKey.AppName)

	app.mu.Lock()
	defer app.mu.Unlock()

	// Check if user exists.
	if app.memories[memoryKey.UserID] == nil {
		return fmt.Errorf("user %s not found", memoryKey.UserID)
	}

	memoryEntry, exists := app.memories[memoryKey.UserID][memoryKey.MemoryID]
	if !exists {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	// Update memory data.
	now := time.Now()
	memoryEntry.Memory.Memory = memoryStr
	memoryEntry.Memory.Topics = topics
	memoryEntry.Memory.LastUpdated = &now
	memoryEntry.UpdatedAt = now

	app.memories[memoryKey.UserID][memoryKey.MemoryID] = memoryEntry
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *MemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	app := s.getAppMemories(memoryKey.AppName)

	app.mu.Lock()
	defer app.mu.Unlock()

	// Check if user exists.
	if app.memories[memoryKey.UserID] == nil {
		return fmt.Errorf("user %s not found", memoryKey.UserID)
	}

	if _, exists := app.memories[memoryKey.UserID][memoryKey.MemoryID]; !exists {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}

	delete(app.memories[memoryKey.UserID], memoryKey.MemoryID)
	return nil
}

// ClearMemories clears all memories for a user.
func (s *MemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	app := s.getAppMemories(userKey.AppName)

	app.mu.Lock()
	defer app.mu.Unlock()

	// Remove all memories for the specific user.
	delete(app.memories, userKey.UserID)
	return nil
}

// ReadMemories reads memories for a user.
func (s *MemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	app := s.getAppMemories(userKey.AppName)

	app.mu.RLock()
	defer app.mu.RUnlock()

	var memories []*memory.Entry
	userMemories := app.memories[userKey.UserID]
	if userMemories == nil {
		return memories, nil
	}

	for _, memoryEntry := range userMemories {
		memories = append(memories, memoryEntry)
	}

	// Sort by updated time (newest first), tie-breaker by created time.
	sort.Slice(memories, func(i, j int) bool {
		if memories[i].UpdatedAt.Equal(memories[j].UpdatedAt) {
			return memories[i].CreatedAt.After(memories[j].CreatedAt)
		}
		return memories[i].UpdatedAt.After(memories[j].UpdatedAt)
	})

	// Apply limit if specified.
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}

	return memories, nil
}

// SearchMemories searches memories for a user.
func (s *MemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	app := s.getAppMemories(userKey.AppName)

	app.mu.RLock()
	defer app.mu.RUnlock()

	var results []*memory.Entry

	userMemories := app.memories[userKey.UserID]
	if userMemories == nil {
		return results, nil
	}

	for _, memoryEntry := range userMemories {
		if imemory.MatchMemoryEntry(memoryEntry, query) {
			results = append(results, memoryEntry)
		}
	}

	// Sort results by updated time (newest first), tie-breaker by created time.
	sort.Slice(results, func(i, j int) bool {
		if results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})
	return results, nil
}

// Tools returns the list of available memory tools.
func (s *MemoryService) Tools() []tool.Tool {
	// Ensure concurrency-safety and stable ordering.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Collect enabled tool names and sort for stable order.
	var names []string
	for toolName := range s.opts.toolCreators {
		if s.opts.enabledTools[toolName] {
			names = append(names, toolName)
		}
	}
	sort.Strings(names)

	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		if _, exists := s.cachedTools[name]; !exists {
			s.cachedTools[name] = s.opts.toolCreators[name]()
		}
		tools = append(tools, s.cachedTools[name])
	}
	return tools
}
