//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

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

	imemory "trpc.group/trpc-go/trpc-agent-go/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*MemoryService)(nil)

// defaultEnabledTools are the creators of default memory tools to enable.
var defaultEnabledTools = imemory.DefaultEnabledTools

const (
	// defaultMemoryLimit is the default limit of memories per user.
	defaultMemoryLimit = imemory.DefaultMemoryLimit
)

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

// serviceOpts contains options for memory service.
type serviceOpts struct {
	// memoryLimit is the limit of memories per user.
	memoryLimit int
	// toolCreators are functions to build tools after service creation.
	toolCreators map[string]memory.ToolCreator
	// enabledTools are the names of tools to enable.
	enabledTools map[string]bool
	// instructionBuilder builds the memory instruction string from enabled tools and default prompt.
	instructionBuilder func(enabledTools []string, defaultPrompt string) string
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
	opts := serviceOpts{
		memoryLimit:  defaultMemoryLimit,
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}

	// Enable default tools first.
	for toolName, creator := range defaultEnabledTools {
		opts.enabledTools[toolName] = true
		opts.toolCreators[toolName] = creator
	}

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

// ServiceOpt is the option for the in-memory memory service.
type ServiceOpt func(*serviceOpts)

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryLimit = limit
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
// If the tool name is invalid, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *serviceOpts) {
		// If the tool name is invalid, do nothing.
		if !imemory.IsValidToolName(toolName) {
			return
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = true
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		// If the tool name is invalid, do nothing.
		if !imemory.IsValidToolName(toolName) {
			return
		}
		opts.enabledTools[toolName] = enabled
	}
}

// WithInstructionBuilder sets a custom instruction builder used by internal GenerateInstruction.
// The builder receives enabled tool names and the framework's default prompt, and should return the final prompt.
func WithInstructionBuilder(builder func(enabledTools []string, defaultPrompt string) string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.instructionBuilder = builder
	}
}

// BuildInstruction allows the internal memory package to obtain a customized instruction if provided.
// Returns (prompt, true) when custom builder is configured; otherwise ("", false).
func (s *MemoryService) BuildInstruction(enabledTools []string, defaultPrompt string) (string, bool) {
	s.mu.RLock()
	builder := s.opts.instructionBuilder
	s.mu.RUnlock()
	if builder == nil {
		return "", false
	}
	return builder(enabledTools, defaultPrompt), true
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
func createMemoryEntry(userID, memoryStr string, topics []string) *memory.Entry {
	now := time.Now()

	// Create Memory object.
	memoryObj := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}

	return &memory.Entry{
		Memory:    memoryObj,
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
		ID:        generateMemoryID(memoryObj), // Generate ID.
	}
}

// AddMemory adds a new memory for a user.
func (s *MemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	app := s.getAppMemories(userKey.AppName)

	// Create memory entry with provided topics.
	memoryEntry := createMemoryEntry(userKey.UserID, memoryStr, topics)

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

	// Sort by creation time (newest first).
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].CreatedAt.After(memories[j].CreatedAt)
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
	queryLower := strings.ToLower(query)

	userMemories := app.memories[userKey.UserID]
	if userMemories == nil {
		return results, nil
	}

	for _, memoryEntry := range userMemories {
		// Simple string search in memory content.
		if strings.Contains(strings.ToLower(memoryEntry.Memory.Memory), queryLower) {
			results = append(results, memoryEntry)
			continue
		}

		// Search in topics.
		for _, topic := range memoryEntry.Memory.Topics {
			if strings.Contains(strings.ToLower(topic), queryLower) {
				results = append(results, memoryEntry)
				break
			}
		}
	}
	return results, nil
}

// Tools returns the list of available memory tools.
func (s *MemoryService) Tools() []tool.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tools []tool.Tool
	for toolName, creator := range s.opts.toolCreators {
		if s.opts.enabledTools[toolName] {
			// Create the tool if not cached.
			if _, exists := s.cachedTools[toolName]; !exists {
				s.cachedTools[toolName] = creator(s)
			}
			tools = append(tools, s.cachedTools[toolName])
		}
	}

	return tools
}
