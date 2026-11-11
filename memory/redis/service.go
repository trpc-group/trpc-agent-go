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
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// defaultConnectionTimeout is the default timeout for Redis connection test.
	defaultConnectionTimeout = 5 * time.Second

	addMemoryResultSuccess = 0
	addMemoryResultForeign = -2

	updateMemoryResultNotFound = 0
	updateMemoryResultSuccess  = 1
	updateMemoryResultConflict = 2
)

var _ memory.Service = (*Service)(nil)

// Escape the field delimiter, hash-tag braces, and the escape character itself.
// Other bytes, including UTF-8 names, remain readable without expanding the key.
var userMemKeyEscaper = strings.NewReplacer(
	"%", "%25",
	":", "%3A",
	"{", "%7B",
	"}", "%7D",
)

// luaOwnsEntry rejects malformed or unscoped records without mutating them.
// Ownership must be checked inside the same script as each guarded write.
const luaOwnsEntry = `
local function ownsEntry(value, appName, userID)
    if not value then
        return false
    end
    local ok, entry = pcall(cjson.decode, value)
    return ok and type(entry) == 'table'
        and entry.app_name == appName and entry.user_id == userID
end
`

// luaAddMemory atomically enforces the per-user limit and protects records of
// other identities in overlapping legacy hashes. A positive result is the
// owned memory count when the limit has been reached.
var luaAddMemory = redis.NewScript(luaOwnsEntry + `
local key = KEYS[1]
local memoryID = ARGV[1]
local entryJSON = ARGV[2]
local memoryLimit = tonumber(ARGV[3])
local appName = ARGV[4]
local userID = ARGV[5]
local checkIdentity = ARGV[6] == '1'
local existing = redis.call('HGET', key, memoryID)

if existing and checkIdentity and not ownsEntry(existing, appName, userID) then
    return -2
end
if not existing and memoryLimit > 0 then
    local count = redis.call('HLEN', key)
    if count >= memoryLimit and checkIdentity then
        count = 0
        for _, value in ipairs(redis.call('HVALS', key)) do
            if ownsEntry(value, appName, userID) then
                count = count + 1
            end
        end
    end
    if count >= memoryLimit then
        return count
    end
end

redis.call('HSET', key, memoryID, entryJSON)
return 0
`)

// luaUpdateMemory atomically updates a memory hash field and, when its
// canonical ID changes, rejects an existing target before rotating the field.
var luaUpdateMemory = redis.NewScript(luaOwnsEntry + `
local key = KEYS[1]
local sourceID = ARGV[1]
local targetID = ARGV[2]
local entryJSON = ARGV[3]

if not ownsEntry(redis.call('HGET', key, sourceID), ARGV[4], ARGV[5]) then
    return 0
end
if sourceID ~= targetID and redis.call('HEXISTS', key, targetID) == 1 then
    return 2
end

redis.call('HSET', key, targetID, entryJSON)
if sourceID ~= targetID then
    redis.call('HDEL', key, sourceID)
end
return 1
`)

// luaDeleteMemory preserves a legacy record belonging to another identity,
// even if it replaced the requested memory just before deletion.
var luaDeleteMemory = redis.NewScript(luaOwnsEntry + `
if ownsEntry(redis.call('HGET', KEYS[1], ARGV[1]), ARGV[2], ARGV[3]) then
    return redis.call('HDEL', KEYS[1], ARGV[1])
end
return 0
`)

// luaClearMemories removes only owned fields because an escaped key can also
// be the raw legacy key of a different identity.
var luaClearMemories = redis.NewScript(luaOwnsEntry + `
local entries = redis.call('HGETALL', KEYS[1])
for i = 1, #entries, 2 do
    if ownsEntry(entries[i + 1], ARGV[1], ARGV[2]) then
        redis.call('HDEL', KEYS[1], entries[i])
    end
end
return 0
`)

// Service is the redis memory service.
// Storage structure:
//
//	Memory: independently escaped appName and userID -> hash [memoryID -> Entry(json)].
type Service struct {
	opts        ServiceOpts
	redisClient redis.UniversalClient

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// NewService creates a new redis memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
	}

	// Apply auto mode defaults after all options are applied.
	// User settings via WithToolEnabled take precedence regardless of option order.
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
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

	svc := &Service{
		opts:        opts,
		redisClient: redisClient,
		cachedTools: make(map[string]tool.Tool),
	}

	// Pre-compute tools list to avoid lock contention in Tools() method.
	svc.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		svc.cachedTools,
	)

	// Initialize auto memory worker if extractor is configured.
	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(
			opts.extractor, opts.enabledTools,
		)
		config := imemory.AutoMemoryConfig{
			Extractor:                opts.extractor,
			AsyncMemoryNum:           opts.asyncMemoryNum,
			MemoryQueueSize:          opts.memoryQueueSize,
			MemoryJobTimeout:         opts.memoryJobTimeout,
			DisableOnExternalContext: opts.disableAutoMemoryOnExternalContext,
			EnabledTools:             opts.enabledTools,
		}
		svc.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, svc)
		svc.autoMemoryWorker.Start()
	}

	return svc, nil
}

// AddMemory adds or updates a memory for a user (idempotent).
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string,
	topics []string, opts ...memory.AddOption) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := s.getUserMemKey(userKey)

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	ep := memory.ResolveAddOptions(opts)
	imemory.ApplyMetadata(mem, ep)
	entry := &memory.Entry{
		ID:        imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID),
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
	checkIdentity := needsHashIdentityCheck(userKey)
	if s.opts.memoryLimit > 0 || checkIdentity {
		scriptResult, err := luaAddMemory.Run(
			ctx,
			s.redisClient,
			[]string{key},
			entry.ID,
			string(bytes),
			s.opts.memoryLimit,
			userKey.AppName,
			userKey.UserID,
			checkIdentity,
		).Int()
		if err != nil {
			return fmt.Errorf("store memory entry failed: %w", err)
		}
		switch {
		case scriptResult == addMemoryResultSuccess:
			return nil
		case scriptResult == addMemoryResultForeign:
			return fmt.Errorf("memory with id %s belongs to another identity", entry.ID)
		case scriptResult > addMemoryResultSuccess:
			return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
				userKey.UserID, s.opts.memoryLimit, scriptResult)
		default:
			return fmt.Errorf("add memory entry returned unexpected result %d", scriptResult)
		}
	}
	if err := s.redisClient.HSet(ctx, key, entry.ID, bytes).Err(); err != nil {
		return fmt.Errorf("store memory entry failed: %w", err)
	}
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string,
	topics []string, opts ...memory.UpdateOption) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	key := s.getUserMemKey(memory.UserKey{
		AppName: memoryKey.AppName,
		UserID:  memoryKey.UserID,
	})

	bytes, err := s.redisClient.HGet(ctx, key, memoryKey.MemoryID).Bytes()
	if err != nil {
		return fmt.Errorf("get memory entry failed: %w", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal(bytes, entry); err != nil {
		return fmt.Errorf("unmarshal memory entry failed: %w", err)
	}
	if entry.AppName != memoryKey.AppName || entry.UserID != memoryKey.UserID {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	imemory.NormalizeEntry(entry)
	now := time.Now()
	ep := memory.ResolveUpdateOptions(opts)
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry failed: %w", err)
	}
	scriptResult, err := luaUpdateMemory.Run(
		ctx,
		s.redisClient,
		[]string{key},
		memoryKey.MemoryID,
		newID,
		string(updated),
		memoryKey.AppName,
		memoryKey.UserID,
	).Int()
	if err != nil {
		return fmt.Errorf("update memory entry failed: %w", err)
	}
	switch scriptResult {
	case updateMemoryResultNotFound:
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	case updateMemoryResultSuccess:
		// Continue and publish the effective ID below.
	case updateMemoryResultConflict:
		return fmt.Errorf("memory with id %s already exists", newID)
	default:
		return fmt.Errorf("update memory entry returned unexpected result %d", scriptResult)
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}
	key := s.getUserMemKey(memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID})
	var err error
	if needsHashIdentityCheck(memory.UserKey{AppName: memoryKey.AppName, UserID: memoryKey.UserID}) {
		err = luaDeleteMemory.Run(ctx, s.redisClient, []string{key},
			memoryKey.MemoryID, memoryKey.AppName, memoryKey.UserID).Err()
	} else {
		err = s.redisClient.HDel(ctx, key, memoryKey.MemoryID).Err()
	}
	if err != nil && err != redis.Nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	key := s.getUserMemKey(userKey)
	var err error
	if needsHashIdentityCheck(userKey) {
		err = luaClearMemories.Run(ctx, s.redisClient, []string{key}, userKey.AppName, userKey.UserID).Err()
	} else {
		err = s.redisClient.Del(ctx, key).Err()
	}
	if err != nil && err != redis.Nil {
		return fmt.Errorf("clear memories failed: %w", err)
	}
	return nil
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	key := s.getUserMemKey(userKey)
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
		if e.AppName != userKey.AppName || e.UserID != userKey.UserID {
			continue
		}
		imemory.NormalizeEntry(e)
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
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey,
	query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	key := s.getUserMemKey(userKey)
	all, err := s.redisClient.HGetAll(ctx, key).Result()
	if err == redis.Nil {
		return []*memory.Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}

	entries := make([]*memory.Entry, 0, len(all))
	for _, v := range all {
		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(v), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		if e.AppName != userKey.AppName || e.UserID != userKey.UserID {
			continue
		}
		imemory.NormalizeEntry(e)
		entries = append(entries, e)
	}
	return imemory.SearchEntries(
		entries,
		memory.ResolveSearchOptions(query, opts),
		s.opts.searchMinScore,
		s.opts.maxSearchResults,
	), nil
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), memory_search is exposed by default,
// memory_load is exposed once enabled, and other enabled tools remain hidden
// unless explicitly exposed.
// Without an extractor, enabled tools are exposed directly.
// The tools list is pre-computed at service creation time.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async
// processing. The session contains the full transcript and state for
// incremental extraction.
func (s *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close closes the redis client connection and stops async workers.
func (s *Service) Close() error {
	if s.autoMemoryWorker != nil {
		s.autoMemoryWorker.Stop()
	}
	if s.redisClient != nil {
		return s.redisClient.Close()
	}
	return nil
}

// prefixedKey adds the configured key prefix to the given base key.
// If no prefix is configured, returns the base key unchanged.
//
// Note: If the prefix already ends with ':', do not add another ':' to avoid
// generating keys like "pfx::mem:{...}".
func (s *Service) prefixedKey(base string) string {
	prefix := s.opts.keyPrefix
	if prefix == "" {
		return base
	}
	if strings.HasSuffix(prefix, ":") {
		return prefix + base
	}
	return prefix + ":" + base
}

// buildUserMemKey builds the Redis base key (without keyPrefix) for a user's
// memories.
//
// Each identity field escapes only %, :, {, and }, preserving readable names
// while keeping field and hash-tag boundaries unambiguous. Escaping % keeps
// literal escape sequences distinct from escaped characters.
func buildUserMemKey(userKey memory.UserKey) string {
	appName := userMemKeyEscaper.Replace(userKey.AppName)
	userID := userMemKeyEscaper.Replace(userKey.UserID)
	return "mem:{" + appName + ":" + userID + "}"
}

// needsHashIdentityCheck identifies keys that could overlap a different
// identity's raw legacy key. Ordinary names retain their direct command paths.
func needsHashIdentityCheck(userKey memory.UserKey) bool {
	return strings.ContainsAny(userKey.AppName, "%:{}") || strings.ContainsAny(userKey.UserID, "%:{}")
}

func (s *Service) getUserMemKey(userKey memory.UserKey) string {
	return s.prefixedKey(buildUserMemKey(userKey))
}
