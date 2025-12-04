//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides the redis memory service.
package redis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// defaultConnectionTimeout is the default timeout for Redis connection test.
	defaultConnectionTimeout = 5 * time.Second
)

var _ memory.Service = (*Service)(nil)

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
	opts := defaultOptions.clone()
	for _, option := range options {
		option(&opts)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderURL(opts.url),
		storage.WithExtraOptions(opts.extraOptions...),
	}

	// if instance name set, and url not set, use instance name to create redis client
	if opts.url == "" && opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetRedisInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("redis instance %s not found", opts.instanceName)
		}
	}

	redisClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create redis client failed: %w", err)
	}

	// Test connection with Ping to ensure Redis is accessible.
	ctx, cancel := context.WithTimeout(context.Background(), defaultConnectionTimeout)
	defer cancel()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection test failed: %w", err)
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
	// Sort by updated time (newest first), tie-breaker by created time.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
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

	results := make([]*memory.Entry, 0)
	for _, v := range all {
		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(v), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		if imemory.MatchMemoryEntry(e, query) {
			results = append(results, e)
		}
	}
	// Stable sort by updated time desc.
	sort.Slice(results, func(i, j int) bool {
		if results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})
	return results, nil
}

// Tools returns the list of available memory tools.
func (s *Service) Tools() []tool.Tool {
	// Concurrency-safe and stable order by name.
	// Protect tool creators/enabled flags and cache with a single lock at
	// call-site by converting to a local snapshot first (no struct-level
	// mutex exists). We assume opts are immutable after construction.
	names := make([]string, 0, len(s.opts.toolCreators))
	for name := range s.opts.toolCreators {
		if s.opts.enabledTools[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		if _, ok := s.cachedTools[name]; !ok {
			s.cachedTools[name] = s.opts.toolCreators[name]()
		}
		tools = append(tools, s.cachedTools[name])
	}
	return tools
}

// generateMemoryID generates a memory ID from memory content.
// Uses SHA256 to match the in-memory implementation for consistency.
func generateMemoryID(mem *memory.Memory) string {
	content := fmt.Sprintf("memory:%s", mem.Memory)
	if len(mem.Topics) > 0 {
		content += fmt.Sprintf("|topics:%s", strings.Join(mem.Topics, ","))
	}
	// Use SHA256 to match in-memory implementation for consistency.
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", hash)
}

// getUserMemKey builds the Redis key for a user's memories.
func getUserMemKey(userKey memory.UserKey) string {
	return fmt.Sprintf("mem:{%s}:%s", userKey.AppName, userKey.UserID)
}
