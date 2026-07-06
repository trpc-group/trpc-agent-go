//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package customservice

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/memoryutils"
)

func TestMapService_addIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := NewMapService()
	defer func() { require.NoError(t, svc.Close()) }()

	userKey := memory.UserKey{AppName: "demo", UserID: "alice"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "likes hiking", []string{"outdoors"}))
	require.NoError(t, svc.AddMemory(ctx, userKey, "likes hiking", []string{"outdoors"}))

	got, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestMapService_updateRotatesIDWhenContentChanges(t *testing.T) {
	ctx := context.Background()
	svc := NewMapService()
	defer func() { require.NoError(t, svc.Close()) }()

	userKey := memory.UserKey{AppName: "demo", UserID: "alice"}
	require.NoError(t, svc.AddMemory(ctx, userKey, "likes hiking", []string{"outdoors"}))

	before, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, before, 1)
	oldID := before[0].ID

	var rotated memory.UpdateResult
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: oldID,
	}, "likes trail running", []string{"outdoors"}, memory.WithUpdateResult(&rotated)))
	require.NotEqual(t, oldID, rotated.MemoryID)

	after, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, rotated.MemoryID, after[0].ID)
}

func TestMapService_searchFiltersByKind(t *testing.T) {
	ctx := context.Background()
	svc := NewMapService()
	defer func() { require.NoError(t, svc.Close()) }()

	userKey := memory.UserKey{AppName: "demo", UserID: "alice"}
	eventTime := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	require.NoError(t, svc.AddMemory(
		ctx,
		userKey,
		"team lunch downtown",
		[]string{"social"},
		memory.WithMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &eventTime,
		}),
	))
	require.NoError(t, svc.AddMemory(ctx, userKey, "prefers tea", []string{"pref"}))

	episodes, err := svc.SearchMemories(
		ctx,
		userKey,
		"lunch",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "lunch",
			Kind:  memory.KindEpisode,
		}),
	)
	require.NoError(t, err)
	require.Len(t, episodes, 1)
	require.Equal(t, memory.KindEpisode, memoryutils.EffectiveKind(episodes[0].Memory))
}
