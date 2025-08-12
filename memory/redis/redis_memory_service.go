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

// Package redis provides the redis memory service.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	imemory "trpc.group/trpc-go/trpc-agent-go/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// defaultEnabledTools are the creators of default memory tools to enable.
var defaultEnabledTools = imemory.DefaultEnabledTools

const (
	// defaultMemoryLimit is the default limit of memories per user.
	defaultMemoryLimit = imemory.DefaultMemoryLimit
)

// Service is the redis memory service.
// Storage structure:
//
//	Memory: appName + userID -> hash [memoryID -> Entry(json)].
type Service struct {
	opts        ServiceOpts
	redisClient redis.UniversalClient

	cachedTools map[string]tool.Tool
}

// NewService creates a new redis memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := ServiceOpts{
		memoryLimit:  defaultMemoryLimit,
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]bool),
	}
	// Enable default tools.
	for name, creator := range defaultEnabledTools {
		opts.toolCreators[name] = creator
		opts.enabledTools[name] = true
	}
	for _, option := range options {
		option(&opts)
	}

	if opts.url == "" && opts.instanceName == "" {
		return nil, errors.New("redis url or instance name is required")
	}

	builder := storage.GetClientBuilder()
	var (
		redisClient redis.UniversalClient
		err         error
	)
	if opts.url != "" {
		redisClient, err = builder(
			storage.WithClientBuilderURL(opts.url),
			storage.WithExtraOptions(opts.extraOptions...),
		)
		if err != nil {
			return nil, fmt.Errorf("create redis client from url failed: %w", err)
		}
	} else {
		builderOpts, ok := storage.GetRedisInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("redis instance %s not found", opts.instanceName)
		}
		redisClient, err = builder(builderOpts...)
		if err != nil {
			return nil, fmt.Errorf("create redis client from instance name failed: %w", err)
		}
	}

	return &Service{
		opts:        opts,
		redisClient: redisClient,
		cachedTools: make(map[string]tool.Tool),
	}, nil
}

// AddMemory adds a new memory for a user.
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := getUserMemKey(userKey)

	// Enforce memory limit by HLen.
	if s.opts.memoryLimit > 0 {
		count, err := s.redisClient.HLen(ctx, key).Result()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("redis memory service check memory count failed: %w", err)
		}
		if int(count) >= s.opts.memoryLimit {
			return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
				userKey.UserID, s.opts.memoryLimit, count)
		}
	}

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	entry := &memory.Entry{
		ID:        generateMemoryID(mem),
		AppName:   userKey.AppName,
		Memory:    mem,
		UserID:    userKey.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	bytes, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal memory entry failed: %w", err)
	}
	if err := s.redisClient.HSet(ctx, key, entry.ID, bytes).Err(); err != nil {
		return fmt.Errorf("store memory entry failed: %w", err)
	}
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	key := getUserMemKey(memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID})

	bytes, err := s.redisClient.HGet(ctx, key, memoryKey.MemoryID).Bytes()
	if err != nil {
		return fmt.Errorf("get memory entry failed: %w", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal(bytes, entry); err != nil {
		return fmt.Errorf("unmarshal memory entry failed: %w", err)
	}
	now := time.Now()
	entry.Memory.Memory = memoryStr
	entry.Memory.Topics = topics
	entry.Memory.LastUpdated = &now
	entry.UpdatedAt = now

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry failed: %w", err)
	}
	if err := s.redisClient.HSet(ctx, key, entry.ID, updated).Err(); err != nil {
		return fmt.Errorf("update memory entry failed: %w", err)
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	key := getUserMemKey(memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID})
	if err := s.redisClient.HDel(ctx, key, memoryKey.MemoryID).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := getUserMemKey(userKey)
	if err := s.redisClient.Del(ctx, key).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("clear memories failed: %w", err)
	}
	return nil
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	key := getUserMemKey(userKey)
	all, err := s.redisClient.HGetAll(ctx, key).Result()
	if err == redis.Nil {
		return []*memory.Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}

	entries := make([]*memory.Entry, 0, len(all))
	for _, v := range all {
		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(v), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	key := getUserMemKey(userKey)
	all, err := s.redisClient.HGetAll(ctx, key).Result()
	if err == redis.Nil {
		return []*memory.Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}

	q := strings.ToLower(query)
	results := make([]*memory.Entry, 0)
	for _, v := range all {
		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(v), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		if strings.Contains(strings.ToLower(e.Memory.Memory), q) {
			results = append(results, e)
			continue
		}
		for _, t := range e.Memory.Topics {
			if strings.Contains(strings.ToLower(t), q) {
				results = append(results, e)
				break
			}
		}
	}
	return results, nil
}

// Tools returns the list of available memory tools.
func (s *Service) Tools() []tool.Tool {
	var toolsList []tool.Tool
	for toolName, creator := range s.opts.toolCreators {
		if s.opts.enabledTools[toolName] {
			if _, ok := s.cachedTools[toolName]; !ok {
				s.cachedTools[toolName] = creator(s)
			}
			toolsList = append(toolsList, s.cachedTools[toolName])
		}
	}
	return toolsList
}

// generateMemoryID generates a memory ID from memory content.
// Follow the same scheme as the in-memory implementation for consistency.
func generateMemoryID(mem *memory.Memory) string {
	// Follow the same scheme as the in-memory implementation for consistency.
	content := fmt.Sprintf("memory:%s", mem.Memory)
	if len(mem.Topics) > 0 {
		content += fmt.Sprintf("|topics:%s", strings.Join(mem.Topics, ","))
	}
	// Lightweight hex encoding of the content bytes to produce a deterministic ID.
	return fmt.Sprintf("%x", []byte(content))
}

// getUserMemKey builds the Redis key for a user's memories.
func getUserMemKey(userKey memory.UserKey) string {
	return fmt.Sprintf("mem:{%s}:%s", userKey.AppName, userKey.UserID)
}
