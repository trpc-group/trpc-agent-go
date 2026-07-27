//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestIntegrationChromaDB159(t *testing.T) {
	baseURL := os.Getenv("CHROMADB_INTEGRATION_URL")
	if baseURL == "" {
		t.Skip("CHROMADB_INTEGRATION_URL is not set")
	}
	options := integrationOptions(baseURL)
	service, err := NewService(options...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	runIntegrationCRUD(t, service)
	runIntegrationClear(t, service, false)

	softOptions := append(options, WithSoftDelete(true))
	softService, err := NewService(softOptions...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, softService.Close()) })
	runIntegrationClear(t, softService, true)
	t.Run("active target conflict", func(t *testing.T) {
		runIntegrationActiveTargetConflict(t, service)
	})
	t.Run("soft target revival", func(t *testing.T) {
		runIntegrationSoftTargetRevival(t, softService)
	})
	t.Run("hard target replacement", func(t *testing.T) {
		runIntegrationHardTargetReplacement(t, service, softService)
	})
}

func integrationOptions(baseURL string) []ServiceOpt {
	options := []ServiceOpt{
		WithBaseURL(baseURL),
		WithCollectionName("trpc_agent_go_memory_integration"),
		WithEmbedder(&testEmbedder{
			dimension: 3,
			values: map[string][]float64{
				"integration memory": {1, 0, 0},
				"integration query":  {1, 0, 0},
				"null metadata":      {0, 1, 0},
			},
		}),
		WithMemoryLimit(0),
	}
	if tenant := os.Getenv("CHROMADB_INTEGRATION_TENANT"); tenant != "" {
		options = append(options, WithTenant(tenant))
	}
	if database := os.Getenv("CHROMADB_INTEGRATION_DATABASE"); database != "" {
		options = append(options, WithDatabase(database))
	}
	return options
}

func runIntegrationCRUD(t *testing.T, service *Service) {
	t.Helper()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "integration", UserID: "crud"}
	require.NoError(t, service.ClearMemories(ctx, userKey))
	t.Cleanup(func() {
		_ = service.ClearMemories(context.Background(), userKey)
	})
	eventTime := time.Unix(0, 1730500123456789012).UTC()
	metadata := &memory.Metadata{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice", "Bob"},
		Location:     "office",
	}
	require.NoError(t, service.AddMemory(
		ctx, userKey, "integration memory", []string{"old", "native-array"},
		memory.WithMetadata(metadata),
	))
	require.NoError(t, service.AddMemory(
		ctx, userKey, "integration memory", []string{"replacement"},
		memory.WithMetadata(metadata),
	))
	require.NoError(t, service.AddMemory(ctx, userKey, "null metadata", nil))
	require.NoError(t, service.AddMemory(
		ctx, userKey, "null metadata", []string{"updated-through-null-patch"},
	))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	eventEntry := integrationEntryByContent(t, entries, "integration memory")
	assert.Equal(t, []string{"replacement"}, eventEntry.Memory.Topics)
	assert.Equal(t, []string{"Alice", "Bob"}, eventEntry.Memory.Participants)
	assert.Equal(t, eventTime.UnixNano(), eventEntry.Memory.EventTime.UnixNano())
	nullEntry := integrationEntryByContent(t, entries, "null metadata")
	assert.Equal(t, []string{"updated-through-null-patch"}, nullEntry.Memory.Topics)
	assert.Nil(t, nullEntry.Memory.EventTime)
	assert.Empty(t, nullEntry.Memory.Participants)
	assert.Empty(t, nullEntry.Memory.Location)

	oldID := eventEntry.ID
	result := &memory.UpdateResult{}
	require.NoError(t, service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: oldID},
		"integration memory updated",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	))
	assert.NotEqual(t, oldID, result.MemoryID)
	require.EqualError(t, service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: "missing"},
		"missing",
		nil,
	), "memory with id missing not found")

	results, err := service.SearchMemories(ctx, userKey, "integration query")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "integration memory updated", results[0].Memory.Memory)
}

func integrationEntryByContent(
	t *testing.T,
	entries []*memory.Entry,
	content string,
) *memory.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Memory.Memory == content {
			return entry
		}
	}
	t.Fatalf("memory %q was not found", content)
	return nil
}

func runIntegrationClear(t *testing.T, service *Service, soft bool) {
	t.Helper()
	ctx := context.Background()
	mode := "hard"
	if soft {
		mode = "soft"
	}
	userKey := memory.UserKey{AppName: "integration", UserID: mode + "-clear"}
	require.NoError(t, service.ClearMemories(ctx, userKey))
	for i := 0; i < defaultReadPageSize+1; i++ {
		content := fmt.Sprintf("%s-clear-%03d", mode, i)
		require.NoError(t, service.AddMemory(ctx, userKey, content, []string{"batch"}))
	}
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, defaultReadPageSize+1)

	require.NoError(t, service.ClearMemories(ctx, userKey))
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func runIntegrationActiveTargetConflict(t *testing.T, service *Service) {
	t.Helper()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "integration", UserID: "active-target"}
	require.NoError(t, service.ClearMemories(ctx, userKey))
	t.Cleanup(func() { _ = service.ClearMemories(context.Background(), userKey) })
	require.NoError(t, service.AddMemory(ctx, userKey, "contract source", []string{"source"}))
	require.NoError(t, service.AddMemory(ctx, userKey, "contract target", []string{"target"}))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	sourceID := integrationEntryByContent(t, entries, "contract source").ID
	targetID := integrationEntryByContent(t, entries, "contract target").ID
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err = service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"contract target",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	)

	require.EqualError(t, err, fmt.Sprintf("memory with id %s already exists", targetID))
	assert.Equal(t, "unchanged", result.MemoryID)
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"source"}, integrationEntryByContent(t, entries, "contract source").Memory.Topics)
	assert.Equal(t, []string{"target"}, integrationEntryByContent(t, entries, "contract target").Memory.Topics)
}

func runIntegrationSoftTargetRevival(t *testing.T, service *Service) {
	t.Helper()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "integration", UserID: "soft-target"}
	require.NoError(t, service.ClearMemories(ctx, userKey))
	t.Cleanup(func() { _ = service.ClearMemories(context.Background(), userKey) })
	require.NoError(t, service.AddMemory(ctx, userKey, "soft source", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "soft target", []string{"deleted"}))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	sourceID := integrationEntryByContent(t, entries, "soft source").ID
	target := integrationEntryByContent(t, entries, "soft target")
	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: target.ID,
	}))
	result := &memory.UpdateResult{}

	require.NoError(t, service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"soft target",
		[]string{"revived"},
		memory.WithUpdateResult(result),
	))

	assert.Equal(t, target.ID, result.MemoryID)
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"revived"}, entries[0].Memory.Topics)
	assert.True(t, entries[0].CreatedAt.Equal(target.CreatedAt))
}

func runIntegrationHardTargetReplacement(
	t *testing.T,
	hardService *Service,
	softService *Service,
) {
	t.Helper()
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "integration", UserID: "hard-target"}
	require.NoError(t, hardService.ClearMemories(ctx, userKey))
	t.Cleanup(func() { _ = hardService.ClearMemories(context.Background(), userKey) })
	require.NoError(t, softService.AddMemory(ctx, userKey, "hard source", nil))
	require.NoError(t, softService.AddMemory(ctx, userKey, "hard target", []string{"deleted"}))
	entries, err := softService.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	source := integrationEntryByContent(t, entries, "hard source")
	targetID := integrationEntryByContent(t, entries, "hard target").ID
	require.NoError(t, softService.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: targetID,
	}))
	result := &memory.UpdateResult{}

	require.NoError(t, hardService.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: source.ID},
		"hard target",
		[]string{"replacement"},
		memory.WithUpdateResult(result),
	))

	assert.Equal(t, targetID, result.MemoryID)
	entries, err = hardService.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"replacement"}, entries[0].Memory.Topics)
	assert.True(t, entries[0].CreatedAt.Equal(source.CreatedAt))
}
