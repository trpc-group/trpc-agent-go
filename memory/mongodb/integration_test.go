//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package mongodb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const mongodbIntegrationURIEnv = "MONGODB_INTEGRATION_URI"

// newIntegrationService builds a Service against a real MongoDB deployment
// using a unique database that is dropped when the test finishes.
// The memory backend does not require multi-document transactions, so a
// standalone deployment is sufficient.
func newIntegrationService(t *testing.T, opts ...ServiceOpt) (context.Context, *Service, *mongo.Database) {
	t.Helper()
	uri := os.Getenv(mongodbIntegrationURIEnv)
	if uri == "" {
		t.Skipf("%s is not set", mongodbIntegrationURIEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbName := fmt.Sprintf("trpc_agent_go_memory_mongodb_it_%d", time.Now().UnixNano())
	cleanupClient, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	database := cleanupClient.Database(dbName)
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = database.Drop(dropCtx)
		_ = cleanupClient.Disconnect(dropCtx)
	})

	allOpts := []ServiceOpt{
		WithMongoClientURI(uri),
		WithDatabase(dbName),
	}
	allOpts = append(allOpts, opts...)
	svc, err := NewService(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	return context.Background(), svc, database
}

func TestIntegrationLifecycle(t *testing.T) {
	ctx, svc, _ := newIntegrationService(t)
	userKey := memory.UserKey{AppName: "it-app", UserID: "it-user"}

	// Add.
	require.NoError(t, svc.AddMemory(ctx, userKey, "User loves golang programming", []string{"golang"}))
	require.NoError(t, svc.AddMemory(ctx, userKey, "User enjoys hiking on weekends", nil))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Search (application-layer BM25).
	found, err := svc.SearchMemories(ctx, userKey, "golang")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "User loves golang programming", found[0].Memory.Memory)

	// Idempotent add.
	require.NoError(t, svc.AddMemory(ctx, userKey, "User loves golang programming", nil))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// In-place update keeps the canonical ID.
	golangID := found[0].ID
	result := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: golangID,
	}, "User loves golang programming", []string{"lang"}, memory.WithUpdateResult(result)))
	assert.Equal(t, golangID, result.MemoryID)

	// Rotation update changes the canonical ID.
	result = &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: golangID,
	}, "User loves rust programming", nil, memory.WithUpdateResult(result)))
	assert.NotEqual(t, golangID, result.MemoryID)

	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Delete (hard delete by default).
	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: result.MemoryID,
	}))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Clear.
	require.NoError(t, svc.ClearMemories(ctx, userKey))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestIntegrationSoftDeleteAndIndexes(t *testing.T) {
	ctx, svc, database := newIntegrationService(t, WithSoftDelete(true))
	userKey := memory.UserKey{AppName: "it-app", UserID: "it-user"}

	// Both declared indexes exist on the collection.
	cursor, err := database.Collection(defaultCollectionName).Indexes().List(ctx)
	require.NoError(t, err)
	defer cursor.Close(ctx)
	names := map[string]bool{}
	for cursor.Next(ctx) {
		var row struct {
			Name string `bson:"name"`
		}
		require.NoError(t, cursor.Decode(&row))
		names[row.Name] = true
	}
	require.NoError(t, cursor.Err())
	assert.True(t, names["idx_memories_unique_active"])
	assert.True(t, names["idx_memories_app_user"])

	// Soft delete leaves a tombstone that re-adding revives.
	require.NoError(t, svc.AddMemory(ctx, userKey, "User likes tea", nil))
	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	id := entries[0].ID

	require.NoError(t, svc.DeleteMemory(ctx, memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: id,
	}))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)

	require.NoError(t, svc.AddMemory(ctx, userKey, "User likes tea", []string{"revived"}))
	entries, err = svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, id, entries[0].ID)
	assert.Equal(t, []string{"revived"}, entries[0].Memory.Topics)
}
