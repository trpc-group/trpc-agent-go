//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// newLegacyOverlapService seeds a legacy literal-percent identity at the key
// now selected by a different, escaped identity.
func newLegacyOverlapService(t *testing.T, limit int) (*Service, *miniredis.Miniredis, memory.UserKey, string) {
	t.Helper()
	server := miniredis.RunT(t)
	svc, err := NewService(WithRedisClientURL("redis://"+server.Addr()), WithMemoryLimit(limit))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	const value = `{"id":"legacy","app_name":"a%3Ab","user_id":"c","memory":{"memory":"legacy private fact"}}`
	server.HSet("mem:{a%3Ab:c}", "legacy", value)
	return svc, server, memory.UserKey{AppName: "a:b", UserID: "c"}, value
}

// TestServiceLegacyOverlapReads keeps records belonging to a legacy identity
// out of both loading and relevance-ranked search results.
func TestServiceLegacyOverlapReads(t *testing.T) {
	svc, _, identity, _ := newLegacyOverlapService(t, 1)
	ctx := context.Background()
	entries, err := svc.ReadMemories(ctx, identity, 10)
	require.NoError(t, err)
	require.Empty(t, entries)
	entries, err = svc.SearchMemories(ctx, identity, "legacy private fact")
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestServiceLegacyOverlapMutations preserves foreign records when callers
// address them through an overlapping key or clear their own memories.
func TestServiceLegacyOverlapMutations(t *testing.T) {
	for _, operation := range []string{"update", "delete", "clear"} {
		t.Run(operation, func(t *testing.T) {
			svc, server, identity, original := newLegacyOverlapService(t, 1)
			ctx := context.Background()
			key := memory.Key{AppName: identity.AppName, UserID: identity.UserID, MemoryID: "legacy"}
			switch operation {
			case "update":
				result := &memory.UpdateResult{MemoryID: "unchanged"}
				err := svc.UpdateMemory(ctx, key, "changed", nil, memory.WithUpdateResult(result))
				require.ErrorContains(t, err, "not found")
				require.Equal(t, "unchanged", result.MemoryID)
			case "delete":
				require.NoError(t, svc.DeleteMemory(ctx, key))
			case "clear":
				require.NoError(t, svc.ClearMemories(ctx, identity))
			}
			require.Equal(t, original, server.HGet("mem:{a%3Ab:c}", "legacy"))
		})
	}
}

// TestServiceLegacyOverlapQuotaAndRotation checks that foreign records neither
// consume quota nor disappear when owned entries are updated or cleared.
func TestServiceLegacyOverlapQuotaAndRotation(t *testing.T) {
	for _, limit := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			svc, server, identity, original := newLegacyOverlapService(t, limit)
			ctx := context.Background()
			require.NoError(t, svc.AddMemory(ctx, identity, "owned fact", nil))
			require.NoError(t, svc.AddMemory(ctx, identity, "owned fact", []string{"refreshed"}))
			if limit > 0 {
				require.ErrorContains(t, svc.AddMemory(ctx, identity, "another fact", nil), "limit exceeded")
			}
			entries, err := svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			result := &memory.UpdateResult{}
			require.NoError(t, svc.UpdateMemory(ctx,
				memory.Key{AppName: identity.AppName, UserID: identity.UserID, MemoryID: entries[0].ID},
				"updated fact", nil, memory.WithUpdateResult(result)))
			entries, err = svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, result.MemoryID, entries[0].ID)
			require.Equal(t, "updated fact", entries[0].Memory.Memory)
			require.NoError(t, svc.ClearMemories(ctx, identity))
			entries, err = svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Equal(t, original, server.HGet("mem:{a%3Ab:c}", "legacy"))
		})
	}
}

// ownershipRaceHook replaces a record immediately before a mutating command,
// exposing non-atomic read-check-then-write implementations.
type ownershipRaceHook struct {
	once   sync.Once
	inject func()
}

func (h *ownershipRaceHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return next
}

func (h *ownershipRaceHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		h.before(cmd.Name())
		return next(ctx, cmd)
	}
}

func (h *ownershipRaceHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []goredis.Cmder) error {
		for _, cmd := range cmds {
			h.before(cmd.Name())
		}
		return next(ctx, cmds)
	}
}

func (h *ownershipRaceHook) before(command string) {
	switch command {
	case "hset", "hdel", "del", "eval", "evalsha":
		h.once.Do(h.inject)
	}
}

// TestServiceOwnershipCheckedAtomically requires mutators to recheck ownership
// atomically when the record changes after an initial application-side read.
func TestServiceOwnershipCheckedAtomically(t *testing.T) {
	for _, operation := range []string{"add", "update", "delete", "clear"} {
		t.Run(operation, func(t *testing.T) {
			svc, server, identity, _ := newLegacyOverlapService(t, 0)
			ctx := context.Background()
			server.HDel("mem:{a%3Ab:c}", "legacy")
			require.NoError(t, svc.AddMemory(ctx, identity, "owned fact", nil))
			entries, err := svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			entry := entries[0]
			entry.AppName = "a%3Ab"
			foreign, err := json.Marshal(entry)
			require.NoError(t, err)
			svc.redisClient.AddHook(&ownershipRaceHook{inject: func() {
				server.HSet("mem:{a%3Ab:c}", entry.ID, string(foreign))
			}})
			key := memory.Key{AppName: identity.AppName, UserID: identity.UserID, MemoryID: entry.ID}
			switch operation {
			case "add":
				require.Error(t, svc.AddMemory(ctx, identity, "owned fact", nil))
			case "update":
				result := &memory.UpdateResult{MemoryID: "unchanged"}
				require.ErrorContains(t, svc.UpdateMemory(ctx, key, "updated", nil, memory.WithUpdateResult(result)), "not found")
				require.Equal(t, "unchanged", result.MemoryID)
			case "delete":
				require.NoError(t, svc.DeleteMemory(ctx, key))
			case "clear":
				require.NoError(t, svc.ClearMemories(ctx, identity))
			}
			require.Equal(t, string(foreign), server.HGet("mem:{a%3Ab:c}", entry.ID))
		})
	}
}

// TestServiceUnownedEntriesPreserved prevents unsafe writes when an overlapping
// hash contains invalid JSON, unscoped data, or a different user's entry.
func TestServiceUnownedEntriesPreserved(t *testing.T) {
	for _, value := range []string{
		`invalid json`,
		`null`,
		`[]`,
		`42`,
		`{"memory":{"memory":"unscoped"}}`,
		`{"app_name":"a:b","user_id":"someone-else","memory":{"memory":"foreign"}}`,
	} {
		t.Run(value, func(t *testing.T) {
			svc, server, identity, _ := newLegacyOverlapService(t, 1)
			ctx := context.Background()
			server.HDel("mem:{a%3Ab:c}", "legacy")
			require.NoError(t, svc.AddMemory(ctx, identity, "owned fact", nil))
			entries, err := svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			id := entries[0].ID
			server.HSet("mem:{a%3Ab:c}", id, value)
			require.Error(t, svc.AddMemory(ctx, identity, "owned fact", nil))
			key := memory.Key{AppName: identity.AppName, UserID: identity.UserID, MemoryID: id}
			require.Error(t, svc.UpdateMemory(ctx, key, "updated fact", nil))
			require.NoError(t, svc.DeleteMemory(ctx, key))
			require.NoError(t, svc.ClearMemories(ctx, identity))
			require.Equal(t, value, server.HGet("mem:{a%3Ab:c}", id))
			require.NoError(t, svc.AddMemory(ctx, identity, "another fact", nil),
				"unowned entries must not consume the user's quota")
		})
	}
}

// TestServiceLegacyOverlapResultLimits filters foreign entries before applying
// read limits and relevance-ranked search limits.
func TestServiceLegacyOverlapResultLimits(t *testing.T) {
	svc, server, identity, _ := newLegacyOverlapService(t, 1)
	server.HSet("mem:{a%3Ab:c}", "legacy",
		`{"id":"legacy","app_name":"a%3Ab","user_id":"c","memory":{"memory":"shared fact"},`+
			`"updated_at":"2099-01-01T00:00:00Z"}`)
	ctx := context.Background()
	svc.opts.maxSearchResults = 1
	require.NoError(t, svc.AddMemory(ctx, identity, "shared fact", nil))
	entries, err := svc.ReadMemories(ctx, identity, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, identity.AppName, entries[0].AppName)
	entries, err = svc.SearchMemories(ctx, identity, "shared fact")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, identity.AppName, entries[0].AppName)
}

// TestServiceLegacyOverlapConcurrentQuota enforces the requesting user's limit
// atomically while excluding the foreign entry from capacity calculations.
func TestServiceLegacyOverlapConcurrentQuota(t *testing.T) {
	svc, server, identity, original := newLegacyOverlapService(t, 1)
	const writers = 12
	start := make(chan struct{})
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			<-start
			results <- svc.AddMemory(context.Background(), identity, fmt.Sprintf("owned fact %d", i), nil)
		}(i)
	}
	close(start)
	var successes int
	for i := 0; i < writers; i++ {
		if err := <-results; err != nil {
			require.ErrorContains(t, err, "limit exceeded")
		} else {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	entries, err := svc.ReadMemories(context.Background(), identity, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, original, server.HGet("mem:{a%3Ab:c}", "legacy"))
}

// TestServiceGuardedMutationFailures propagates Redis script failures without
// falling back to an unguarded mutation that could destroy foreign entries.
func TestServiceGuardedMutationFailures(t *testing.T) {
	for _, operation := range []string{"add", "delete", "clear"} {
		t.Run(operation, func(t *testing.T) {
			svc, server, identity, original := newLegacyOverlapService(t, 0)
			svc.redisClient.AddHook(&memoryScriptHook{err: fmt.Errorf("script denied")})
			var err error
			switch operation {
			case "add":
				err = svc.AddMemory(context.Background(), identity, "owned fact", nil)
			case "delete":
				err = svc.DeleteMemory(context.Background(), memory.Key{
					AppName: identity.AppName, UserID: identity.UserID, MemoryID: "legacy",
				})
			case "clear":
				err = svc.ClearMemories(context.Background(), identity)
			}
			require.ErrorContains(t, err, "script denied")
			require.Equal(t, original, server.HGet("mem:{a%3Ab:c}", "legacy"))
		})
	}
}

// TestServiceOrdinaryIdentitiesWithoutLua preserves the direct add, delete,
// and clear paths for ordinary identities when the quota is disabled.
func TestServiceOrdinaryIdentitiesWithoutLua(t *testing.T) {
	for _, limit := range []int{-1, 0} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			svc, _, _, _ := newLegacyOverlapService(t, limit)
			svc.redisClient.AddHook(&memoryScriptHook{err: fmt.Errorf("script denied")})
			identity := memory.UserKey{AppName: "ordinary", UserID: "user"}
			ctx := context.Background()
			require.NoError(t, svc.AddMemory(ctx, identity, "owned fact", nil))
			entries, err := svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
				AppName: identity.AppName, UserID: identity.UserID, MemoryID: entries[0].ID,
			}))
			require.NoError(t, svc.AddMemory(ctx, identity, "another fact", nil))
			require.NoError(t, svc.ClearMemories(ctx, identity))
			entries, err = svc.ReadMemories(ctx, identity, 0)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}
