//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
)

// TestServiceCollidingIdentities isolates CRUD operations and quotas across
// delimiter collisions, including configured key prefixes.
func TestServiceCollidingIdentities(t *testing.T) {
	for _, limit := range []int{0, 1} {
		for _, prefix := range []string{"", "prod", "prod:"} {
			t.Run(fmt.Sprintf("prefix=%q/limit=%d", prefix, limit), func(t *testing.T) {
				server := miniredis.RunT(t)
				svc, err := memoryredis.NewService(
					memoryredis.WithRedisClientURL("redis://"+server.Addr()),
					memoryredis.WithKeyPrefix(prefix),
					memoryredis.WithMemoryLimit(limit),
				)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, svc.Close()) })
				ctx := context.Background()
				alice := memory.UserKey{AppName: "a:b", UserID: "c"}
				bob := memory.UserKey{AppName: "a", UserID: "b:c"}
				require.NoError(t, svc.AddMemory(ctx, alice, "alice private fact", nil))
				entries, err := svc.ReadMemories(ctx, bob, 10)
				require.NoError(t, err)
				require.Empty(t, entries, "different identities must not share memory")
				require.NoError(t, svc.AddMemory(ctx, bob, "bob private fact", nil),
					"another identity must not consume the user's quota")
				entries, err = svc.ReadMemories(ctx, alice, 10)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.Equal(t, "alice private fact", entries[0].Memory.Memory)
				aliceID := entries[0].ID
				foreignKey := memory.Key{AppName: bob.AppName, UserID: bob.UserID, MemoryID: aliceID}
				require.Error(t, svc.UpdateMemory(ctx, foreignKey, "changed", nil))
				require.NoError(t, svc.DeleteMemory(ctx, foreignKey))
				results, err := svc.SearchMemories(ctx, bob, "private fact")
				require.NoError(t, err)
				require.Len(t, results, 1)
				for _, entry := range results {
					require.Equal(t, bob.AppName, entry.AppName)
					require.Equal(t, bob.UserID, entry.UserID)
				}
				require.NoError(t, svc.ClearMemories(ctx, bob))
				entries, err = svc.ReadMemories(ctx, alice, 10)
				require.NoError(t, err)
				require.Len(t, entries, 1, "clearing another user must preserve this memory")
				require.Equal(t, aliceID, entries[0].ID)
				require.Equal(t, "alice private fact", entries[0].Memory.Memory)
			})
		}
	}
}

// TestServiceSpecialIdentities keeps Unicode, braces, and literal escape
// sequences distinct without changing the stored identity fields.
func TestServiceSpecialIdentities(t *testing.T) {
	server := miniredis.RunT(t)
	svc, err := memoryredis.NewService(
		memoryredis.WithRedisClientURL("redis://"+server.Addr()),
		memoryredis.WithMemoryLimit(1),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	ctx := context.Background()
	identities := []memory.UserKey{
		{AppName: "租户:一", UserID: "用户"},
		{AppName: "租户", UserID: "一:用户"},
		{AppName: "a:{x", UserID: "y}"},
		{AppName: "a", UserID: "{x:y}"},
		{AppName: "a%3Ab", UserID: "c"},
		{AppName: "a:b", UserID: "c"},
		{AppName: "{app}", UserID: "user"},
		{AppName: "%7Bapp%7D", UserID: "user"},
		{AppName: "app", UserID: "%"},
		{AppName: "app", UserID: "%25"},
	}
	for _, identity := range identities {
		require.NoError(t, svc.AddMemory(ctx, identity, "same memory", nil))
	}
	for _, identity := range identities {
		entries, err := svc.ReadMemories(ctx, identity, 10)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, identity.AppName, entries[0].AppName)
		require.Equal(t, identity.UserID, entries[0].UserID)
	}
}

// TestServiceDoesNotReadOrDeleteAmbiguousLegacyHashes avoids treating a shared
// legacy key as belonging to either possible identity.
func TestServiceDoesNotReadOrDeleteAmbiguousLegacyHashes(t *testing.T) {
	server := miniredis.RunT(t)
	const legacyKey = "mem:{a:b:c}"
	const legacyValue = `{"id":"legacy","app_name":"a:b","user_id":"c","memory":{"memory":"legacy private fact"}}`
	server.HSet(legacyKey, "legacy", legacyValue)
	svc, err := memoryredis.NewService(memoryredis.WithRedisClientURL("redis://" + server.Addr()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	ctx := context.Background()
	for _, identity := range []memory.UserKey{
		{AppName: "a:b", UserID: "c"},
		{AppName: "a", UserID: "b:c"},
	} {
		entries, err := svc.ReadMemories(ctx, identity, 10)
		require.NoError(t, err)
		require.Empty(t, entries)
		require.NoError(t, svc.ClearMemories(ctx, identity))
	}
	require.True(t, server.Exists(legacyKey))
	require.Equal(t, legacyValue, server.HGet(legacyKey, "legacy"))
}
