//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var testCtx = context.Background()

func testUserKey() memory.UserKey {
	return memory.UserKey{AppName: "test-app", UserID: "test-user"}
}

func memoryKeyOf(userKey memory.UserKey, id string) memory.Key {
	return memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: id}
}

// entryIDByContent returns the ID of the single entry whose content matches.
func entryIDByContent(t *testing.T, entries []*memory.Entry, content string) string {
	t.Helper()
	for _, e := range entries {
		if e.Memory != nil && e.Memory.Memory == content {
			return e.ID
		}
	}
	t.Fatalf("entry with content %q not found", content)
	return ""
}

func TestKeyValidation(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)

	t.Run("AddMemory", func(t *testing.T) {
		err := svc.AddMemory(testCtx, memory.UserKey{UserID: "u"}, "m", nil)
		assert.ErrorIs(t, err, memory.ErrAppNameRequired)
		err = svc.AddMemory(testCtx, memory.UserKey{AppName: "a"}, "m", nil)
		assert.ErrorIs(t, err, memory.ErrUserIDRequired)
	})
	t.Run("UpdateMemory", func(t *testing.T) {
		err := svc.UpdateMemory(testCtx, memory.Key{UserID: "u", MemoryID: "m"}, "m", nil)
		assert.ErrorIs(t, err, memory.ErrAppNameRequired)
		err = svc.UpdateMemory(testCtx, memory.Key{AppName: "a", MemoryID: "m"}, "m", nil)
		assert.ErrorIs(t, err, memory.ErrUserIDRequired)
		err = svc.UpdateMemory(testCtx, memory.Key{AppName: "a", UserID: "u"}, "m", nil)
		assert.ErrorIs(t, err, memory.ErrMemoryIDRequired)
	})
	t.Run("DeleteMemory", func(t *testing.T) {
		err := svc.DeleteMemory(testCtx, memory.Key{UserID: "u", MemoryID: "m"})
		assert.ErrorIs(t, err, memory.ErrAppNameRequired)
		err = svc.DeleteMemory(testCtx, memory.Key{AppName: "a", MemoryID: "m"})
		assert.ErrorIs(t, err, memory.ErrUserIDRequired)
		err = svc.DeleteMemory(testCtx, memory.Key{AppName: "a", UserID: "u"})
		assert.ErrorIs(t, err, memory.ErrMemoryIDRequired)
	})
	t.Run("ReadMemories", func(t *testing.T) {
		_, err := svc.ReadMemories(testCtx, memory.UserKey{UserID: "u"}, 0)
		assert.ErrorIs(t, err, memory.ErrAppNameRequired)
		_, err = svc.ReadMemories(testCtx, memory.UserKey{AppName: "a"}, 0)
		assert.ErrorIs(t, err, memory.ErrUserIDRequired)
	})
	t.Run("SearchMemories", func(t *testing.T) {
		_, err := svc.SearchMemories(testCtx, memory.UserKey{UserID: "u"}, "q")
		assert.ErrorIs(t, err, memory.ErrAppNameRequired)
		_, err = svc.SearchMemories(testCtx, memory.UserKey{AppName: "a"}, "q")
		assert.ErrorIs(t, err, memory.ErrUserIDRequired)
	})
	t.Run("ClearMemories", func(t *testing.T) {
		err := svc.ClearMemories(testCtx, memory.UserKey{UserID: "u"})
		assert.ErrorIs(t, err, memory.ErrAppNameRequired)
		err = svc.ClearMemories(testCtx, memory.UserKey{AppName: "a"})
		assert.ErrorIs(t, err, memory.ErrUserIDRequired)
	})
}

func TestAddMemoryAndRead(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	err := svc.AddMemory(testCtx, userKey, "User loves golang programming", []string{"golang"})
	require.NoError(t, err)

	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, "test-app", e.AppName)
	assert.Equal(t, "test-user", e.UserID)
	assert.Equal(t, "User loves golang programming", e.Memory.Memory)
	assert.Equal(t, []string{"golang"}, e.Memory.Topics)
	assert.NotEmpty(t, e.ID)
	assert.False(t, e.CreatedAt.IsZero())
	assert.False(t, e.UpdatedAt.IsZero())
	assert.Equal(t, memory.KindFact, e.Memory.Kind)
	require.Len(t, store.docs, 1)
	assert.Nil(t, store.docs[0].deletedAt)

	// Episodic metadata is persisted through the JSON payload.
	eventTime := time.Date(2024, 5, 7, 10, 0, 0, 0, time.UTC)
	err = svc.AddMemory(testCtx, userKey, "User went hiking at Mt. Fuji", nil,
		memory.WithMetadata(&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice"},
			Location:     "Mt. Fuji",
		}))
	require.NoError(t, err)
	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	ep := entries[0]
	if ep.Memory.Kind != memory.KindEpisode {
		ep = entries[1]
	}
	require.Equal(t, memory.KindEpisode, ep.Memory.Kind)
	require.NotNil(t, ep.Memory.EventTime)
	assert.True(t, eventTime.Equal(*ep.Memory.EventTime))
	assert.Equal(t, []string{"Alice"}, ep.Memory.Participants)
	assert.Equal(t, "Mt. Fuji", ep.Memory.Location)
}

func TestAddMemoryIdempotent(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang", nil))
	require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang", []string{"pl"}))

	assert.Len(t, store.docs, 1, "idempotent add must not create duplicates")
	assert.Equal(t, 1, store.activeDocs())
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"pl"}, entries[0].Memory.Topics)
}

func TestAddMemoryRevivesSoftDeleted(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc, func(o *ServiceOpts) { o.softDelete = true })
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang", nil))
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	id := entries[0].ID
	originalCreatedAt := store.docs[0].createdAt

	require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, id)))
	assert.Equal(t, 0, store.activeDocs())

	// Re-adding the same content revives the tombstone instead of creating
	// a new document.
	require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang", []string{"revived"}))
	require.Len(t, store.docs, 1)
	assert.Equal(t, 1, store.activeDocs())
	assert.True(t, originalCreatedAt.Equal(store.docs[0].createdAt),
		"revived document keeps its original created_at")

	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, id, entries[0].ID)
	assert.Equal(t, []string{"revived"}, entries[0].Memory.Topics)
}

func TestAddMemoryLimit(t *testing.T) {
	t.Run("new memories rejected at limit, re-add allowed", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc, func(o *ServiceOpts) { o.memoryLimit = 2 })
		userKey := testUserKey()

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory bravo", nil))
		err := svc.AddMemory(testCtx, userKey, "memory charlie", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memory limit exceeded")

		// Idempotent re-add of an existing memory is not counted.
		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
	})

	t.Run("tombstones are not counted", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc, func(o *ServiceOpts) {
			o.memoryLimit = 1
			o.softDelete = true
		})
		userKey := testUserKey()

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
		err := svc.AddMemory(testCtx, userKey, "memory bravo", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memory limit exceeded")

		entries, err := svc.ReadMemories(testCtx, userKey, 0)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, entries[0].ID)))

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory bravo", nil))
	})
}

func TestUpdateMemoryNotFound(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)

	err := svc.UpdateMemory(testCtx, memoryKeyOf(testUserKey(), "missing"), "new", nil)
	require.Error(t, err)
	assert.Equal(t, "memory with id missing not found", err.Error())
}

func TestUpdateMemoryFindError(t *testing.T) {
	mc, _ := newFakeClient()
	mc.findOneFn = func(any) *mongo.SingleResult {
		return mongo.NewSingleResultFromDocument(bson.D{}, errors.New("connection lost"), nil)
	}
	svc := newServiceForTest(mc)

	err := svc.UpdateMemory(testCtx, memoryKeyOf(testUserKey(), "id"), "new", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get memory entry failed")
}

func TestUpdateMemoryInPlace(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang", nil))
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	id := entries[0].ID

	// Same content keeps the canonical ID; only topics change.
	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(testCtx, memoryKeyOf(userKey, id),
		"User loves golang", []string{"programming"}, memory.WithUpdateResult(result)))
	assert.Equal(t, id, result.MemoryID)

	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, id, entries[0].ID)
	assert.Equal(t, []string{"programming"}, entries[0].Memory.Topics)
}

func TestUpdateMemoryRotationHardDelete(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes tea", nil))
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID

	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(testCtx, memoryKeyOf(userKey, oldID),
		"User likes coffee", nil, memory.WithUpdateResult(result)))
	require.NotEmpty(t, result.MemoryID)
	assert.NotEqual(t, oldID, result.MemoryID)

	require.Len(t, store.docs, 1, "hard-delete rotation removes the source document")
	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, result.MemoryID, entries[0].ID)
	assert.Equal(t, "User likes coffee", entries[0].Memory.Memory)
}

func TestUpdateMemoryRotationSoftDelete(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc, func(o *ServiceOpts) { o.softDelete = true })
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes tea", nil))
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID

	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(testCtx, memoryKeyOf(userKey, oldID),
		"User likes coffee", nil, memory.WithUpdateResult(result)))

	require.Len(t, store.docs, 2, "soft-delete rotation retains the source tombstone")
	assert.Equal(t, 1, store.activeDocs())

	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, result.MemoryID, entries[0].ID)

	// The soft-deleted source is treated as not found.
	err = svc.UpdateMemory(testCtx, memoryKeyOf(userKey, oldID), "again", nil)
	require.Error(t, err)
	assert.Equal(t, fmt.Sprintf("memory with id %s not found", oldID), err.Error())

	// Re-adding the original content revives the source tombstone.
	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes tea", nil))
	assert.Equal(t, 2, store.activeDocs())
}

func TestUpdateMemoryRotationTargetConflict(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes tea", nil))
	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes coffee", nil))
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	teaID := entryIDByContent(t, entries, "User likes tea")

	err = svc.UpdateMemory(testCtx, memoryKeyOf(userKey, teaID), "User likes coffee", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Neither memory is modified.
	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.NotNil(t, entryIDByContent(t, entries, "User likes tea"))
	assert.NotNil(t, entryIDByContent(t, entries, "User likes coffee"))
	assert.Equal(t, 2, store.activeDocs())
}

func TestUpdateMemoryRotationRevivesTargetTombstone(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc, func(o *ServiceOpts) { o.softDelete = true })
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes tea", nil))
	require.NoError(t, svc.AddMemory(testCtx, userKey, "User likes coffee", nil))
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	coffeeID := entryIDByContent(t, entries, "User likes coffee")
	teaID := entryIDByContent(t, entries, "User likes tea")
	var coffeeCreatedAt time.Time
	for _, e := range entries {
		if e.ID == coffeeID {
			coffeeCreatedAt = e.CreatedAt
		}
	}

	// Tombstone the coffee memory, then rotate tea onto the coffee ID.
	require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, coffeeID)))
	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(testCtx, memoryKeyOf(userKey, teaID),
		"User likes coffee", nil, memory.WithUpdateResult(result)))
	assert.Equal(t, coffeeID, result.MemoryID)

	require.Len(t, store.docs, 2)
	assert.Equal(t, 1, store.activeDocs())
	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, coffeeID, entries[0].ID)
	assert.Equal(t, "User likes coffee", entries[0].Memory.Memory)
	assert.True(t, coffeeCreatedAt.Equal(entries[0].CreatedAt),
		"revived target keeps its original created_at")
}

func TestDeleteMemory(t *testing.T) {
	t.Run("hard delete", func(t *testing.T) {
		mc, store := newFakeClient()
		svc := newServiceForTest(mc)
		userKey := testUserKey()

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
		entries, err := svc.ReadMemories(testCtx, userKey, 0)
		require.NoError(t, err)
		require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, entries[0].ID)))
		assert.Empty(t, store.docs)

		// Deleting a missing memory is tolerated.
		require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, "missing")))
	})

	t.Run("soft delete", func(t *testing.T) {
		mc, store := newFakeClient()
		svc := newServiceForTest(mc, func(o *ServiceOpts) { o.softDelete = true })
		userKey := testUserKey()

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
		entries, err := svc.ReadMemories(testCtx, userKey, 0)
		require.NoError(t, err)
		require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, entries[0].ID)))
		require.Len(t, store.docs, 1)
		assert.Equal(t, 0, store.activeDocs())

		// Tombstones are invisible to reads and searches.
		entries, err = svc.ReadMemories(testCtx, userKey, 0)
		require.NoError(t, err)
		assert.Empty(t, entries)
		found, err := svc.SearchMemories(testCtx, userKey, "alpha")
		require.NoError(t, err)
		assert.Empty(t, found)

		// Deleting an already deleted memory is tolerated.
		require.NoError(t, svc.DeleteMemory(testCtx, memoryKeyOf(userKey, "missing")))
	})
}

func TestClearMemories(t *testing.T) {
	t.Run("hard delete", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc)
		userKey := testUserKey()

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory bravo", nil))
		require.NoError(t, svc.ClearMemories(testCtx, userKey))
		entries, err := svc.ReadMemories(testCtx, userKey, 0)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("soft delete", func(t *testing.T) {
		mc, store := newFakeClient()
		svc := newServiceForTest(mc, func(o *ServiceOpts) { o.softDelete = true })
		userKey := testUserKey()

		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "memory bravo", nil))
		require.NoError(t, svc.ClearMemories(testCtx, userKey))
		assert.Len(t, store.docs, 2)
		assert.Equal(t, 0, store.activeDocs())
	})
}

func TestScopeIsolation(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)
	alice := memory.UserKey{AppName: "app-one", UserID: "user-one"}
	bob := memory.UserKey{AppName: "app-two", UserID: "user-one"}

	require.NoError(t, svc.AddMemory(testCtx, alice, "shared content alpha", nil))
	require.NoError(t, svc.AddMemory(testCtx, bob, "shared content bravo", nil))

	entries, err := svc.ReadMemories(testCtx, alice, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "shared content alpha", entries[0].Memory.Memory)

	found, err := svc.SearchMemories(testCtx, bob, "shared content")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "shared content bravo", found[0].Memory.Memory)

	require.NoError(t, svc.ClearMemories(testCtx, alice))
	entries, err = svc.ReadMemories(testCtx, bob, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestReadMemoriesOrderAndLimit(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	require.NoError(t, svc.AddMemory(testCtx, userKey, "memory alpha", nil))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, svc.AddMemory(testCtx, userKey, "memory bravo", nil))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, svc.AddMemory(testCtx, userKey, "memory charlie", nil))

	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "memory charlie", entries[0].Memory.Memory)
	assert.Equal(t, "memory bravo", entries[1].Memory.Memory)
	assert.Equal(t, "memory alpha", entries[2].Memory.Memory)

	// An in-place update bumps updated_at and reorders the entry first.
	alphaID := entries[2].ID
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, svc.UpdateMemory(testCtx, memoryKeyOf(userKey, alphaID),
		"memory alpha", []string{"bumped"}))
	entries, err = svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, alphaID, entries[0].ID)

	// Limit truncates; non-positive limits return everything.
	entries, err = svc.ReadMemories(testCtx, userKey, 2)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	entries, err = svc.ReadMemories(testCtx, userKey, -1)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestReadMemoriesEmpty(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)

	entries, err := svc.ReadMemories(testCtx, testUserKey(), 0)
	require.NoError(t, err)
	require.NotNil(t, entries)
	assert.Empty(t, entries)
}

func TestSearchMemories(t *testing.T) {
	userKey := testUserKey()

	newSearchService := func(mods ...func(*ServiceOpts)) *Service {
		mc, _ := newFakeClient()
		return newServiceForTest(mc, mods...)
	}

	t.Run("empty query returns empty", func(t *testing.T) {
		svc := newSearchService()
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang programming", nil))
		entries, err := svc.SearchMemories(testCtx, userKey, "")
		require.NoError(t, err)
		require.NotNil(t, entries)
		assert.Empty(t, entries)
	})

	t.Run("keyword match ranks relevant memory", func(t *testing.T) {
		svc := newSearchService()
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang programming", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User enjoys Italian pasta cooking", nil))

		entries, err := svc.SearchMemories(testCtx, userKey, "golang")
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "User loves golang programming", entries[0].Memory.Memory)
		assert.Greater(t, entries[0].Score, 0.0)
	})

	t.Run("min score threshold filters weak matches", func(t *testing.T) {
		svc := newSearchService(func(o *ServiceOpts) { o.searchMinScore = 0.99 })
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang programming", nil))
		entries, err := svc.SearchMemories(testCtx, userKey, "golang")
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("max results truncates", func(t *testing.T) {
		svc := newSearchService(func(o *ServiceOpts) { o.maxSearchResults = 1 })
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User loves golang programming", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "golang is fun for the user", nil))
		entries, err := svc.SearchMemories(testCtx, userKey, "golang")
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})

	t.Run("kind filter", func(t *testing.T) {
		svc := newSearchService()
		eventTime := time.Date(2024, 5, 7, 10, 0, 0, 0, time.UTC)
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User is a golang engineer", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "golang conference in Tokyo", nil,
			memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &eventTime})))

		entries, err := svc.SearchMemories(testCtx, userKey, "golang",
			memory.WithSearchOptions(memory.SearchOptions{
				Query: "golang",
				Kind:  memory.KindEpisode,
			}))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, memory.KindEpisode, entries[0].Memory.Kind)
	})

	t.Run("kind fallback merges unfiltered results", func(t *testing.T) {
		svc := newSearchService()
		eventTime := time.Date(2024, 5, 7, 10, 0, 0, 0, time.UTC)
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User is a golang engineer", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "golang conference in Tokyo", nil,
			memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &eventTime})))

		entries, err := svc.SearchMemories(testCtx, userKey, "golang",
			memory.WithSearchOptions(memory.SearchOptions{
				Query:        "golang",
				Kind:         memory.KindFact,
				KindFallback: true,
			}))
		require.NoError(t, err)
		assert.Len(t, entries, 2, "fallback should merge the episode back in")
	})

	t.Run("time range filters episodes", func(t *testing.T) {
		svc := newSearchService()
		may := time.Date(2024, 5, 7, 10, 0, 0, 0, time.UTC)
		july := time.Date(2024, 7, 1, 10, 0, 0, 0, time.UTC)
		require.NoError(t, svc.AddMemory(testCtx, userKey, "hiking trip in spring", nil,
			memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &may})))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "hiking trip in summer", nil,
			memory.WithMetadata(&memory.Metadata{Kind: memory.KindEpisode, EventTime: &july})))

		after := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		entries, err := svc.SearchMemories(testCtx, userKey, "hiking",
			memory.WithSearchOptions(memory.SearchOptions{Query: "hiking", TimeAfter: &after}))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "hiking trip in summer", entries[0].Memory.Memory)

		before := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		entries, err = svc.SearchMemories(testCtx, userKey, "hiking",
			memory.WithSearchOptions(memory.SearchOptions{Query: "hiking", TimeBefore: &before}))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "hiking trip in spring", entries[0].Memory.Memory)
	})

	t.Run("deduplicate removes near duplicates", func(t *testing.T) {
		svc := newSearchService()
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User prefers dark roast coffee beans", nil))
		require.NoError(t, svc.AddMemory(testCtx, userKey, "User prefers dark roast coffee beans daily", nil))

		entries, err := svc.SearchMemories(testCtx, userKey, "coffee",
			memory.WithSearchOptions(memory.SearchOptions{Query: "coffee", Deduplicate: false}))
		require.NoError(t, err)
		assert.Len(t, entries, 2)

		entries, err = svc.SearchMemories(testCtx, userKey, "coffee",
			memory.WithSearchOptions(memory.SearchOptions{Query: "coffee", Deduplicate: true}))
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
}

func TestTools(t *testing.T) {
	toolNames := func(tools []tool.Tool) []string {
		names := make([]string, 0, len(tools))
		for _, tl := range tools {
			names = append(names, tl.Declaration().Name)
		}
		return names
	}

	t.Run("default tools", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc)
		assert.Equal(t, []string{
			memory.AddToolName, memory.LoadToolName, memory.SearchToolName, memory.UpdateToolName,
		}, toolNames(svc.Tools()))
	})

	t.Run("tools list is cloned", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc)
		first := svc.Tools()
		require.NotEmpty(t, first)
		first[0] = nil
		second := svc.Tools()
		assert.NotNil(t, second[0])
	})

	t.Run("enable and disable tools", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc, func(o *ServiceOpts) {
			WithToolEnabled(memory.AddToolName, false)(o)
			WithToolEnabled(memory.DeleteToolName, true)(o)
		})
		assert.Equal(t, []string{
			memory.DeleteToolName, memory.LoadToolName, memory.SearchToolName, memory.UpdateToolName,
		}, toolNames(svc.Tools()))
	})

	t.Run("custom tool", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc, func(o *ServiceOpts) {
			WithCustomTool(memory.ClearToolName,
				func() tool.Tool { return memorytool.NewClearTool() })(o)
		})
		assert.Contains(t, toolNames(svc.Tools()), memory.ClearToolName)
	})
}

// mockExtractor is a mock implementation of extractor.MemoryExtractor.
type mockExtractor struct {
	extractCalled bool
	enabledTools  map[string]struct{}
}

func (m *mockExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*extractor.Operation, error) {
	m.extractCalled = true
	return nil, nil
}

func (m *mockExtractor) ShouldExtract(_ *extractor.ExtractionContext) bool { return true }
func (m *mockExtractor) SetPrompt(_ string)                                {}
func (m *mockExtractor) SetModel(_ model.Model)                            {}

func (m *mockExtractor) SetEnabledTools(enabled map[string]struct{}) {
	m.enabledTools = enabled
}

func (m *mockExtractor) Metadata() map[string]any { return map[string]any{} }

func TestNewServiceWithExtractor(t *testing.T) {
	mc, _ := newFakeClient()
	installMockBuilder(t, mc, nil)
	ext := &mockExtractor{}

	svc, err := NewService(
		WithMongoClientURI("mongodb://localhost:27017"),
		WithSkipDBInit(true),
		WithExtractor(ext),
	)
	require.NoError(t, err)
	require.NotNil(t, svc.autoMemoryWorker)

	// Auto memory mode exposes only memory_search by default.
	tools := svc.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, memory.SearchToolName, tools[0].Declaration().Name)

	// The extractor received the enabled tool set.
	require.NotNil(t, ext.enabledTools)
	assert.Contains(t, ext.enabledTools, memory.AddToolName)

	// Enqueue with an empty session is a no-op.
	sess := session.NewSession("app", "user", "sid")
	require.NoError(t, svc.EnqueueAutoMemoryJob(testCtx, sess))
	require.NoError(t, svc.Close())
}

func TestNewServiceAutoMemoryExposedTools(t *testing.T) {
	mc, _ := newFakeClient()
	installMockBuilder(t, mc, nil)

	svc, err := NewService(
		WithMongoClientURI("mongodb://localhost:27017"),
		WithSkipDBInit(true),
		WithExtractor(&mockExtractor{}),
		WithAutoMemoryExposedTools(memory.AddToolName),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, svc.Close()) }()

	names := make([]string, 0, 2)
	for _, tl := range svc.Tools() {
		names = append(names, tl.Declaration().Name)
	}
	assert.Equal(t, []string{memory.AddToolName, memory.SearchToolName}, names)
}

func TestEnqueueAutoMemoryJobWithoutExtractor(t *testing.T) {
	mc, _ := newFakeClient()
	svc := newServiceForTest(mc)
	assert.Nil(t, svc.autoMemoryWorker)
	require.NoError(t, svc.EnqueueAutoMemoryJob(testCtx, session.NewSession("app", "user", "sid")))
}

func TestCloseIdempotent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mc, _ := newFakeClient()
		svc := newServiceForTest(mc)
		require.NoError(t, svc.Close())
		require.NoError(t, svc.Close())
		assert.Len(t, mc.opsByName("Disconnect"), 1, "client disconnect must happen exactly once")
	})

	t.Run("disconnect error surfaces on first close only", func(t *testing.T) {
		mc, _ := newFakeClient()
		mc.disconnectFn = func() error { return errors.New("disconnect failed") }
		svc := newServiceForTest(mc)
		err := svc.Close()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disconnect failed")
		assert.NoError(t, svc.Close())
		assert.Len(t, mc.opsByName("Disconnect"), 1)
	})
}

func TestNewService(t *testing.T) {
	t.Run("creates indexes and precomputes tools", func(t *testing.T) {
		mc, _ := newFakeClient()
		installMockBuilder(t, mc, nil)

		svc, err := NewService(WithMongoClientURI("mongodb://localhost:27017"))
		require.NoError(t, err)
		defer func() { require.NoError(t, svc.Close()) }()

		createMany := mc.opsByName("CreateMany")
		require.Len(t, createMany, 1)
		assert.Equal(t, defaultDatabase, createMany[0].database)
		assert.Equal(t, defaultCollectionName, createMany[0].coll)
		assert.NotEmpty(t, svc.Tools())
	})

	t.Run("custom database and collection", func(t *testing.T) {
		mc, _ := newFakeClient()
		installMockBuilder(t, mc, nil)

		svc, err := NewService(
			WithMongoClientURI("mongodb://localhost:27017"),
			WithDatabase("custom-db"),
			WithCollectionName("custom_memories"),
		)
		require.NoError(t, err)
		defer func() { require.NoError(t, svc.Close()) }()

		createMany := mc.opsByName("CreateMany")
		require.Len(t, createMany, 1)
		assert.Equal(t, "custom-db", createMany[0].database)
		assert.Equal(t, "custom_memories", createMany[0].coll)
	})

	t.Run("skip db init", func(t *testing.T) {
		mc, _ := newFakeClient()
		installMockBuilder(t, mc, nil)

		svc, err := NewService(
			WithMongoClientURI("mongodb://localhost:27017"),
			WithSkipDBInit(true),
		)
		require.NoError(t, err)
		defer func() { require.NoError(t, svc.Close()) }()
		assert.Empty(t, mc.opsByName("CreateMany"))
	})

	t.Run("requires uri or instance", func(t *testing.T) {
		_, err := NewService(WithSkipDBInit(true))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WithMongoClientURI or WithMongoInstance")
	})

	t.Run("instance not found", func(t *testing.T) {
		_, err := NewService(
			WithMongoInstance("memory-mongodb-test-missing-instance"),
			WithSkipDBInit(true),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("registered instance is used", func(t *testing.T) {
		instanceName := "memory-mongodb-test-instance"
		storage.RegisterMongoDBInstance(instanceName,
			storage.WithClientBuilderDSN("mongodb://instance-host:27017"))

		mc, _ := newFakeClient()
		var captured []storage.ClientBuilderOpt
		installMockBuilder(t, mc, func(opts []storage.ClientBuilderOpt) { captured = opts })

		svc, err := NewService(
			WithMongoInstance(instanceName),
			WithSkipDBInit(true),
		)
		require.NoError(t, err)
		defer func() { require.NoError(t, svc.Close()) }()

		builderOpts := &storage.ClientBuilderOpts{}
		for _, opt := range captured {
			opt(builderOpts)
		}
		assert.Equal(t, "mongodb://instance-host:27017", builderOpts.URI)
	})

	t.Run("uri takes precedence over instance", func(t *testing.T) {
		instanceName := "memory-mongodb-test-instance-priority"
		storage.RegisterMongoDBInstance(instanceName,
			storage.WithClientBuilderDSN("mongodb://instance-host:27017"))

		mc, _ := newFakeClient()
		var captured []storage.ClientBuilderOpt
		installMockBuilder(t, mc, func(opts []storage.ClientBuilderOpt) { captured = opts })

		svc, err := NewService(
			WithMongoInstance(instanceName),
			WithMongoClientURI("mongodb://uri-host:27017"),
			WithExtraOptions("extra-opt"),
			WithSkipDBInit(true),
		)
		require.NoError(t, err)
		defer func() { require.NoError(t, svc.Close()) }()

		builderOpts := &storage.ClientBuilderOpts{}
		for _, opt := range captured {
			opt(builderOpts)
		}
		assert.Equal(t, "mongodb://uri-host:27017", builderOpts.URI)
		assert.Contains(t, builderOpts.ExtraOptions, "extra-opt")
	})

	t.Run("init failure closes client", func(t *testing.T) {
		mc, _ := newFakeClient()
		mc.ensureIndexesFn = func([]mongo.IndexModel) ([]string, error) {
			return nil, errors.New("createIndexes not allowed")
		}
		installMockBuilder(t, mc, nil)

		_, err := NewService(WithMongoClientURI("mongodb://localhost:27017"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ensure mongodb indexes failed")
		assert.Len(t, mc.opsByName("Disconnect"), 1,
			"client must be disconnected when initialization fails")
	})

	t.Run("client builder error", func(t *testing.T) {
		original := storage.GetClientBuilder()
		storage.SetClientBuilder(func(_ context.Context,
			_ ...storage.ClientBuilderOpt) (storage.Client, error) {
			return nil, errors.New("connect failed")
		})
		t.Cleanup(func() { storage.SetClientBuilder(original) })

		_, err := NewService(WithMongoClientURI("mongodb://localhost:27017"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create mongodb client failed")
	})

	t.Run("client missing index viewer is rejected", func(t *testing.T) {
		inner := &mockClient{}
		original := storage.GetClientBuilder()
		storage.SetClientBuilder(func(_ context.Context,
			_ ...storage.ClientBuilderOpt) (storage.Client, error) {
			return &storageOnlyClient{inner: inner}, nil
		})
		t.Cleanup(func() { storage.SetClientBuilder(original) })

		_, err := NewService(WithMongoClientURI("mongodb://localhost:27017"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "storage.Client and storage.IndexViewer")
		assert.Len(t, inner.opsByName("Disconnect"), 1)
	})
}

// storageOnlyClient implements storage.Client but deliberately not
// storage.IndexViewer, to exercise the NewService type-assertion path.
// Calls are delegated to an inner mockClient so Disconnect is recorded.
type storageOnlyClient struct {
	inner *mockClient
}

func (c *storageOnlyClient) InsertOne(ctx context.Context, db, coll string, doc any,
	opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	return c.inner.InsertOne(ctx, db, coll, doc, opts...)
}

func (c *storageOnlyClient) UpdateOne(ctx context.Context, db, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return c.inner.UpdateOne(ctx, db, coll, filter, update, opts...)
}

func (c *storageOnlyClient) UpdateMany(ctx context.Context, db, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return c.inner.UpdateMany(ctx, db, coll, filter, update, opts...)
}

func (c *storageOnlyClient) DeleteOne(ctx context.Context, db, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.inner.DeleteOne(ctx, db, coll, filter, opts...)
}

func (c *storageOnlyClient) DeleteMany(ctx context.Context, db, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.inner.DeleteMany(ctx, db, coll, filter, opts...)
}

func (c *storageOnlyClient) FindOne(ctx context.Context, db, coll string, filter any,
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	return c.inner.FindOne(ctx, db, coll, filter, opts...)
}

func (c *storageOnlyClient) Find(ctx context.Context, db, coll string, filter any,
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	return c.inner.Find(ctx, db, coll, filter, opts...)
}

func (c *storageOnlyClient) Aggregate(ctx context.Context, db, coll string, pipeline any,
	opts ...*options.AggregateOptions) (*mongo.Cursor, error) {
	return c.inner.Aggregate(ctx, db, coll, pipeline, opts...)
}

func (c *storageOnlyClient) CountDocuments(ctx context.Context, db, coll string, filter any,
	opts ...*options.CountOptions) (int64, error) {
	return c.inner.CountDocuments(ctx, db, coll, filter, opts...)
}

func (c *storageOnlyClient) Transaction(ctx context.Context, sf storage.TxFunc,
	tOpts []*options.TransactionOptions, opts ...*options.SessionOptions) error {
	return c.inner.Transaction(ctx, sf, tOpts, opts...)
}

func (c *storageOnlyClient) Disconnect(ctx context.Context) error {
	return c.inner.Disconnect(ctx)
}

func (c *storageOnlyClient) Indexes(ctx context.Context, db, coll string) (mongo.IndexView, error) {
	return c.inner.Indexes(ctx, db, coll)
}

func TestConcurrentAdd(t *testing.T) {
	mc, store := newFakeClient()
	svc := newServiceForTest(mc)
	userKey := testUserKey()

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			assert.NoError(t, svc.AddMemory(testCtx, userKey,
				fmt.Sprintf("concurrent memory %d", i), nil))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, n, store.activeDocs())
	entries, err := svc.ReadMemories(testCtx, userKey, 0)
	require.NoError(t, err)
	assert.Len(t, entries, n)
}
