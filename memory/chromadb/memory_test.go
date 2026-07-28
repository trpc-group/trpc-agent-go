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
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// newTestChromaService creates a service backed by the package test server.
func newTestChromaService(
	t *testing.T,
	embedder *testEmbedder,
	options ...ServiceOpt,
) (*Service, *fakeChroma) {
	t.Helper()
	fake := newFakeChroma()
	t.Cleanup(fake.close)
	service := newTestChromaServiceWithFake(t, fake, embedder, options...)
	return service, fake
}

func newTestChromaServiceWithFake(
	t *testing.T,
	fake *fakeChroma,
	embedder *testEmbedder,
	options ...ServiceOpt,
) *Service {
	t.Helper()
	base := []ServiceOpt{
		WithBaseURL(fake.server.URL),
		WithEmbedder(embedder),
	}
	service, err := NewService(append(base, options...)...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	return service
}

func readTestMemories(
	t *testing.T,
	service *Service,
	userKey memory.UserKey,
) []*memory.Entry {
	t.Helper()

	entries, err := service.ReadMemories(context.Background(), userKey, 0)
	require.NoError(t, err)
	return entries
}

func testMemoryEntryByContent(
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

func TestServiceAddMemoryIsIdempotentAndReplacesTopics(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithMemoryLimit(10),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2025, 10, 1, 16, 0, 0, 0, time.UTC)
	metadata := &memory.Metadata{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice", "Bob"},
		Location:     "office",
	}
	require.NoError(t, service.AddMemory(
		ctx, userKey, "Alice met Bob", []string{"old"}, memory.WithMetadata(metadata),
	))
	require.NoError(t, service.AddMemory(
		ctx, userKey, "Alice met Bob", []string{"new"}, memory.WithMetadata(metadata),
	))

	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"new"}, entries[0].Memory.Topics)
	assert.Equal(t, memory.KindEpisode, entries[0].Memory.Kind)
	assert.Equal(t, eventTime, *entries[0].Memory.EventTime)
}

func TestServiceAddMemoryRejectsOutOfRangeEventTime(t *testing.T) {
	tests := []struct {
		name      string
		eventTime time.Time
	}{
		{name: "before minimum", eventTime: time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)},
		{name: "after maximum", eventTime: time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})

			err := service.AddMemory(
				context.Background(),
				memory.UserKey{AppName: "app", UserID: "user"},
				"episode",
				nil,
				memory.WithMetadata(&memory.Metadata{
					Kind:      memory.KindEpisode,
					EventTime: &tt.eventTime,
				}),
			)

			require.Error(t, err)
			fake.mu.Lock()
			defer fake.mu.Unlock()
			assert.Empty(t, fake.records)
		})
	}
}

func TestServiceAddMemoryEnforcesCapacity(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithMemoryLimit(1),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "first", nil))

	err := service.AddMemory(ctx, userKey, "second", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
	entries, readErr := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, readErr)
	assert.Len(t, entries, 1)
}

func TestServiceAddMemoryCapacityIsSerialized(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithMemoryLimit(1),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	errorsCh := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, content := range []string{"first", "second"} {
		waitGroup.Add(1)
		go func(value string) {
			defer waitGroup.Done()
			errorsCh <- service.AddMemory(ctx, userKey, value, nil)
		}(content)
	}
	waitGroup.Wait()
	close(errorsCh)

	var success, failed int
	for err := range errorsCh {
		if err == nil {
			success++
		} else {
			failed++
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, failed)
}

func TestServiceAddMemoryRejectsForeignIDCollision(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	now := time.Now().UTC()
	target := newAddRecord(
		recordScope{appName: "app", userID: "user"}, "fact", nil, nil, now,
	)
	foreign := newAddRecord(
		recordScope{appName: "other-app", userID: "other-user"}, "fact", nil, nil, now,
	)
	foreign.entry.ID = target.entry.ID
	putFakeRecord(fake, foreign)

	err := service.AddMemory(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"fact",
		nil,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "different app or user")
}

func TestServiceAddMemoryReturnsEmbeddingFailure(t *testing.T) {
	embeddingErr := errors.New("embedding unavailable")
	service, fake := newTestChromaService(t, &testEmbedder{
		dimension: 3,
		err:       embeddingErr,
	})

	err := service.AddMemory(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"fact",
		nil,
	)

	require.ErrorIs(t, err, embeddingErr)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Empty(t, fake.records)
}

func TestServiceUpdateMemoryRotatesID(t *testing.T) {
	service, _ := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "old content", []string{"old"}))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID
	result := &memory.UpdateResult{}

	err = service.UpdateMemory(
		ctx,
		memory.Key{AppName: "app", UserID: "user", MemoryID: oldID},
		"new content",
		[]string{"new"},
		memory.WithUpdateResult(result),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, result.MemoryID)
	assert.NotEqual(t, oldID, result.MemoryID)

	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, result.MemoryID, entries[0].ID)
	assert.Equal(t, "new content", entries[0].Memory.Memory)
	assert.Equal(t, []string{"new"}, entries[0].Memory.Topics)
}

func TestServiceUpdateMemoryRejectsOutOfRangeEventTime(t *testing.T) {
	service, _ := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", []string{"original"}))
	source := readTestMemories(t, service, userKey)[0]
	eventTime := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := service.UpdateMemory(
		ctx,
		memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: source.ID,
		},
		"target",
		[]string{"updated"},
		memory.WithUpdateMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &eventTime,
		}),
		memory.WithUpdateResult(result),
	)

	require.Error(t, err)
	assert.Equal(t, "unchanged", result.MemoryID)
	entries := readTestMemories(t, service, userKey)
	require.Len(t, entries, 1)
	assert.Equal(t, source.ID, entries[0].ID)
	assert.Equal(t, []string{"original"}, entries[0].Memory.Topics)
}

func TestServiceUpdateMemoryRejectsActiveTargetWithoutMutation(t *testing.T) {
	service, _ := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "active-target"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", []string{"source"}))
	require.NoError(t, service.AddMemory(ctx, userKey, "target", []string{"target"}))
	entries := readTestMemories(t, service, userKey)
	sourceID := testMemoryEntryByContent(t, entries, "source").ID
	targetID := testMemoryEntryByContent(t, entries, "target").ID
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"target",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	)

	require.EqualError(t, err, fmt.Sprintf("memory with id %s already exists", targetID))
	assert.Equal(t, "unchanged", result.MemoryID)
	entries = readTestMemories(t, service, userKey)
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"source"}, testMemoryEntryByContent(t, entries, "source").Memory.Topics)
	assert.Equal(t, []string{"target"}, testMemoryEntryByContent(t, entries, "target").Memory.Topics)
}

func TestServiceUpdateMemoryRevivesSoftDeletedTarget(t *testing.T) {
	service, fake := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "soft-target"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "target", []string{"deleted"}))
	entries := readTestMemories(t, service, userKey)
	sourceID := testMemoryEntryByContent(t, entries, "source").ID
	target := testMemoryEntryByContent(t, entries, "target")
	targetID := target.ID
	targetCreatedAt := target.CreatedAt
	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: targetID,
	}))
	fake.status["delete"] = http.StatusBadRequest
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"target",
		[]string{"revived"},
		memory.WithUpdateResult(result),
	)

	require.NoError(t, err)
	assert.Equal(t, targetID, result.MemoryID)
	entries = readTestMemories(t, service, userKey)
	require.Len(t, entries, 1)
	assert.Equal(t, targetID, entries[0].ID)
	assert.Equal(t, []string{"revived"}, entries[0].Memory.Topics)
	assert.True(t, entries[0].CreatedAt.Equal(targetCreatedAt))
}

func TestServiceUpdateMemoryHardDeleteReplacesSoftDeletedTarget(t *testing.T) {
	fake := newFakeChroma()
	t.Cleanup(fake.close)
	embedder := &testEmbedder{dimension: 3}
	softService := newTestChromaServiceWithFake(
		t,
		fake,
		embedder,
		WithSoftDelete(true),
	)
	hardService := newTestChromaServiceWithFake(t, fake, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "hard-target"}
	require.NoError(t, softService.AddMemory(ctx, userKey, "source", nil))
	require.NoError(t, softService.AddMemory(ctx, userKey, "target", []string{"deleted"}))
	entries := readTestMemories(t, softService, userKey)
	source := testMemoryEntryByContent(t, entries, "source")
	sourceID := source.ID
	sourceCreatedAt := source.CreatedAt
	targetID := testMemoryEntryByContent(t, entries, "target").ID
	require.NoError(t, softService.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: targetID,
	}))
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := hardService.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"target",
		[]string{"replacement"},
		memory.WithUpdateResult(result),
	)

	require.NoError(t, err)
	assert.Equal(t, targetID, result.MemoryID)
	entries = readTestMemories(t, hardService, userKey)
	require.Len(t, entries, 1)
	assert.Equal(t, targetID, entries[0].ID)
	assert.Equal(t, []string{"replacement"}, entries[0].Memory.Topics)
	assert.True(t, entries[0].CreatedAt.Equal(sourceCreatedAt))
}

func TestServiceUpdateMemoryHardDeleteRecoversLostTombstoneDeleteResponse(t *testing.T) {
	fake := newFakeChroma()
	t.Cleanup(fake.close)
	embedder := &testEmbedder{dimension: 3}
	softService := newTestChromaServiceWithFake(
		t,
		fake,
		embedder,
		WithSoftDelete(true),
	)
	hardService := newTestChromaServiceWithFake(t, fake, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "lost-tombstone-delete"}
	require.NoError(t, softService.AddMemory(ctx, userKey, "source", nil))
	require.NoError(t, softService.AddMemory(ctx, userKey, "target", nil))
	entries := readTestMemories(t, softService, userKey)
	sourceID := testMemoryEntryByContent(t, entries, "source").ID
	targetID := testMemoryEntryByContent(t, entries, "target").ID
	require.NoError(t, softService.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: targetID,
	}))
	fake.deleteAfterWrite = http.StatusServiceUnavailable
	fake.deleteFailuresLeft = 1
	result := &memory.UpdateResult{}

	err := hardService.UpdateMemory(
		ctx,
		memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID,
		},
		"target",
		[]string{"replacement"},
		memory.WithUpdateResult(result),
	)

	require.NoError(t, err)
	assert.Equal(t, targetID, result.MemoryID)
	entries = readTestMemories(t, hardService, userKey)
	require.Len(t, entries, 1)
	assert.Equal(t, targetID, entries[0].ID)
	assert.Equal(t, []string{"replacement"}, entries[0].Memory.Topics)
}

func TestServiceUpdateMemoryHardDeleteRejectsTombstoneSource(t *testing.T) {
	fake := newFakeChroma()
	t.Cleanup(fake.close)
	embedder := &testEmbedder{dimension: 3}
	softService := newTestChromaServiceWithFake(
		t,
		fake,
		embedder,
		WithSoftDelete(true),
	)
	hardService := newTestChromaServiceWithFake(t, fake, embedder)
	tests := []struct {
		name    string
		content string
	}{
		{name: "same identity", content: "source"},
		{name: "rotated identity", content: "target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			userKey := memory.UserKey{AppName: "app", UserID: tt.name}
			require.NoError(t, softService.AddMemory(ctx, userKey, "source", nil))
			entries := readTestMemories(t, softService, userKey)
			sourceID := entries[0].ID
			key := memory.Key{
				AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID,
			}
			require.NoError(t, softService.DeleteMemory(ctx, key))
			result := &memory.UpdateResult{MemoryID: "unchanged"}

			err := hardService.UpdateMemory(
				ctx,
				key,
				tt.content,
				nil,
				memory.WithUpdateResult(result),
			)

			require.ErrorContains(t, err, "not found")
			assert.Equal(t, "unchanged", result.MemoryID)
			assert.Empty(t, readTestMemories(t, hardService, userKey))
		})
	}
}

func TestServiceUpdateMemorySameIDDoesNotReviveConcurrentlyDeletedSource(t *testing.T) {
	service, fake := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "same-id-source-delete"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", []string{"old"}))
	sourceID := readTestMemories(t, service, userKey)[0].ID
	var once sync.Once
	fake.requestHook = func(operation string) {
		if operation != "update" {
			return
		}
		once.Do(func() {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.records[sourceID].metadata[metadataDeletedAtKey] =
				time.Now().UTC().UnixNano()
		})
	}
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"source",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	)

	require.ErrorContains(t, err, "not found")
	assert.Equal(t, "unchanged", result.MemoryID)
	assert.Empty(t, readTestMemories(t, service, userKey))
}

func TestServiceUpdateMemoryConcurrentTargetCreationDoesNotOverwriteTarget(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "concurrent-target"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", nil))
	sourceID := readTestMemories(t, service, userKey)[0].ID
	scope := recordScope{appName: userKey.AppName, userID: userKey.UserID}
	competingTarget := newAddRecord(
		scope,
		"target",
		[]string{"competing"},
		nil,
		time.Now().UTC(),
	)
	var once sync.Once
	fake.requestHook = func(operation string) {
		if operation == "add" {
			once.Do(func() { putFakeRecord(fake, competingTarget) })
		}
	}
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"target",
		[]string{"updated"},
		memory.WithUpdateResult(result),
	)

	require.Error(t, err)
	assert.Equal(t, "unchanged", result.MemoryID)
	entries := readTestMemories(t, service, userKey)
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"competing"}, testMemoryEntryByContent(t, entries, "target").Memory.Topics)
	assert.Equal(t, sourceID, testMemoryEntryByContent(t, entries, "source").ID)
}

func TestServiceAddMemoryAfterSoftDeleteRotationRevivesSource(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "re-add-source"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", nil))
	sourceID := readTestMemories(t, service, userKey)[0].ID
	result := &memory.UpdateResult{}
	require.NoError(t, service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID},
		"target",
		nil,
		memory.WithUpdateResult(result),
	))
	require.NoError(t, service.AddMemory(ctx, userKey, "source", nil))

	entries := readTestMemories(t, service, userKey)
	require.Len(t, entries, 2)
	assert.Equal(t, sourceID, testMemoryEntryByContent(t, entries, "source").ID)
	assert.Equal(t, result.MemoryID, testMemoryEntryByContent(t, entries, "target").ID)
}

func TestServiceUpdateMemoryReturnsEmbeddingFailureBeforeWrite(t *testing.T) {
	embedder := &testEmbedder{dimension: 3}
	service, fake := newTestChromaService(t, embedder)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "old content", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID
	embeddingErr := errors.New("embedding unavailable")
	embedder.err = embeddingErr

	err = service.UpdateMemory(
		ctx,
		memory.Key{AppName: "app", UserID: "user", MemoryID: oldID},
		"new content",
		nil,
	)

	require.ErrorIs(t, err, embeddingErr)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.records, 1)
	assert.Contains(t, fake.records, oldID)
}

func TestServiceUpdateMemoryMissingPreservesErrorContract(t *testing.T) {
	service, _ := newTestChromaService(t, &testEmbedder{dimension: 3})

	err := service.UpdateMemory(
		context.Background(),
		memory.Key{AppName: "app", UserID: "user", MemoryID: "missing"},
		"new content",
		nil,
	)

	require.EqualError(t, err, "memory with id missing not found")
}

func TestServiceUpdateMemoryPreservesOldRecordWhenTargetAddFails(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "old content", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID
	fake.status["add"] = http.StatusBadRequest

	err = service.UpdateMemory(
		ctx,
		memory.Key{AppName: "app", UserID: "user", MemoryID: oldID},
		"new content",
		nil,
	)

	require.Error(t, err)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Len(t, fake.records, 1)
	assert.Contains(t, fake.records, oldID)
}

func TestServiceUpdateMemoryRecoversLostTargetAddResponse(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "old content", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID
	fake.addAfterWrite = http.StatusServiceUnavailable
	fake.addFailuresLeft = 1
	result := &memory.UpdateResult{}

	err = service.UpdateMemory(
		ctx,
		memory.Key{AppName: "app", UserID: "user", MemoryID: oldID},
		"new content",
		nil,
		memory.WithUpdateResult(result),
	)

	require.NoError(t, err)
	assert.NotEqual(t, oldID, result.MemoryID)
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, result.MemoryID, entries[0].ID)
}

func TestServiceUpdateMemoryRollsForwardAfterPartialFailure(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "old content", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID
	key := memory.Key{AppName: "app", UserID: "user", MemoryID: oldID}
	fake.status["delete"] = 500

	err = service.UpdateMemory(ctx, key, "new content", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "partially completed")
	fake.mu.Lock()
	assert.Len(t, fake.records, 2)
	fake.mu.Unlock()
	delete(fake.status, "delete")
	result := &memory.UpdateResult{}

	err = service.UpdateMemory(
		ctx, key, "new content", nil, memory.WithUpdateResult(result),
	)
	require.NoError(t, err)
	assert.NotEqual(t, oldID, result.MemoryID)
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, result.MemoryID, entries[0].ID)
}

func TestServiceUpdateMemoryRetriesAfterTombstoneRevivalFailure(t *testing.T) {
	service, fake := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "tombstone-roll-forward"}
	require.NoError(t, service.AddMemory(ctx, userKey, "source", nil))
	require.NoError(t, service.AddMemory(ctx, userKey, "target", []string{"deleted"}))
	entries := readTestMemories(t, service, userKey)
	sourceID := testMemoryEntryByContent(t, entries, "source").ID
	targetID := testMemoryEntryByContent(t, entries, "target").ID
	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: targetID,
	}))
	key := memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: sourceID,
	}
	fake.status["update"] = http.StatusBadRequest
	result := &memory.UpdateResult{MemoryID: "unchanged"}

	err := service.UpdateMemory(
		ctx,
		key,
		"target",
		[]string{"replacement"},
		memory.WithUpdateResult(result),
	)

	require.Error(t, err)
	assert.Equal(t, "unchanged", result.MemoryID)
	entries = readTestMemories(t, service, userKey)
	require.Len(t, entries, 1)
	assert.Equal(t, sourceID, entries[0].ID)

	delete(fake.status, "update")
	require.NoError(t, service.UpdateMemory(
		ctx,
		key,
		"target",
		[]string{"replacement"},
		memory.WithUpdateResult(result),
	))
	assert.Equal(t, targetID, result.MemoryID)
	entries = readTestMemories(t, service, userKey)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"replacement"}, entries[0].Memory.Topics)
}

func TestServiceDeleteMemoryRejectsInvalidDeleteCount(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "fact", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	invalidCount := 2
	fake.deleteCount = &invalidCount

	err = service.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: entries[0].ID,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 deletions")
}

func TestServiceSoftDeleteCanReviveRecord(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
		WithMemoryLimit(1),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "fact", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	key := memory.Key{AppName: "app", UserID: "user", MemoryID: entries[0].ID}
	require.NoError(t, service.DeleteMemory(ctx, key))
	require.NoError(t, service.DeleteMemory(ctx, key))

	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
	require.NoError(t, service.AddMemory(ctx, userKey, "fact", []string{"revived"}))
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, []string{"revived"}, entries[0].Memory.Topics)
}

func TestServiceSoftDeleteReviveRespectsCapacity(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
		WithMemoryLimit(1),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "first", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName: "app", UserID: "user", MemoryID: entries[0].ID,
	}))
	require.NoError(t, service.AddMemory(ctx, userKey, "second", nil))

	err = service.AddMemory(ctx, userKey, "first", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestServiceUpdateMemoryDoesNotReviveSoftDeletedRecord(t *testing.T) {
	service, _ := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
	)
	ctx := context.Background()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, userKey, "old content", nil))
	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	oldID := entries[0].ID
	require.NoError(t, service.DeleteMemory(ctx, memory.Key{
		AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: oldID,
	}))

	err = service.UpdateMemory(
		ctx,
		memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: oldID},
		"new content",
		nil,
	)

	require.EqualError(t, err, "memory with id "+oldID+" not found")
	entries, err = service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestServiceClearMemoriesProcessesAllBatches(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	now := time.Now().UTC()
	for i := 0; i < defaultReadPageSize+5; i++ {
		record := newAddRecord(scope, fmt.Sprintf("memory-%03d", i), nil, nil, now)
		putFakeRecord(fake, record)
	}

	err := service.ClearMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
	)
	require.NoError(t, err)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Empty(t, fake.records)
}

func TestServiceClearMemoriesSoftDeletesAllBatches(t *testing.T) {
	service, fake := newTestChromaService(
		t,
		&testEmbedder{dimension: 3},
		WithSoftDelete(true),
	)
	scope := recordScope{appName: "app", userID: "user"}
	now := time.Now().UTC()
	for i := 0; i < defaultReadPageSize+5; i++ {
		record := newAddRecord(scope, fmt.Sprintf("memory-%03d", i), nil, nil, now)
		putFakeRecord(fake, record)
	}

	err := service.ClearMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
	)
	require.NoError(t, err)
	entries, err := service.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
		0,
	)
	require.NoError(t, err)
	assert.Empty(t, entries)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Len(t, fake.records, defaultReadPageSize+5)
}

func TestServiceClearMemoriesUsesFixedCutoff(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	old := newAddRecord(scope, "old memory", nil, nil, time.Now().UTC().Add(-time.Minute))
	putFakeRecord(fake, old)
	var once sync.Once
	fake.requestHook = func(operation string) {
		if operation != "get" {
			return
		}
		once.Do(func() {
			newRecord := newAddRecord(
				scope, "new memory", nil, nil, time.Now().UTC().Add(time.Minute),
			)
			putFakeRecord(fake, newRecord)
		})
	}

	err := service.ClearMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
	)

	require.NoError(t, err)
	entries, err := service.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
		0,
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "new memory", entries[0].Memory.Memory)
}

func TestServiceClearMemoriesRejectsDeleteWithoutProgress(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	putFakeRecord(fake, newAddRecord(scope, "memory", nil, nil, time.Now().UTC()))
	zero := 0
	fake.deleteCount = &zero
	fake.deleteNoop = true

	err := service.ClearMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remained active")
}

func TestServiceClearMemoriesRecoversLostDeleteResponse(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	putFakeRecord(fake, newAddRecord(scope, "memory", nil, nil, time.Now().UTC()))
	fake.deleteAfterWrite = http.StatusServiceUnavailable
	fake.deleteFailuresLeft = 1

	err := service.ClearMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
	)

	require.NoError(t, err)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Empty(t, fake.records)
}

func TestServiceReadMemoriesSortsAcrossPages(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	start := time.Now().UTC().Add(-time.Hour)
	want := make([]string, 0, 2)
	for i := 0; i < defaultReadPageSize+5; i++ {
		record := newAddRecord(
			scope,
			fmt.Sprintf("memory-%03d", i),
			nil,
			nil,
			start.Add(time.Duration(i)*time.Second),
		)
		putFakeRecord(fake, record)
		if i >= defaultReadPageSize+3 {
			want = append([]string{record.entry.ID}, want...)
		}
	}

	entries, err := service.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
		2,
	)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, want[0], entries[0].ID)
	assert.Equal(t, want[1], entries[1].ID)
}

func TestServiceReadMemoriesSerializesPaginationWithLocalDelete(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	const recordCount = defaultReadPageSize + 1
	lastID := fmt.Sprintf("id-%03d", recordCount-1)
	for i := 0; i < recordCount; i++ {
		record := newAddRecord(
			scope,
			fmt.Sprintf("memory-%03d", i),
			nil,
			nil,
			time.Unix(int64(i), 0).UTC(),
		)
		record.entry.ID = fmt.Sprintf("id-%03d", i)
		putFakeRecord(fake, record)
	}
	secondPageStarted := make(chan struct{})
	releaseSecondPage := make(chan struct{})
	var blockOnce sync.Once
	fake.getHook = func(request getRecordsRequest) {
		if len(request.IDs) > 0 || request.Offset == nil ||
			*request.Offset != defaultReadPageSize {
			return
		}
		blockOnce.Do(func() {
			close(secondPageStarted)
			<-releaseSecondPage
		})
	}
	type readResult struct {
		entries []*memory.Entry
		err     error
	}
	readDone := make(chan readResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		entries, err := service.ReadMemories(ctx, memory.UserKey{
			AppName: scope.appName, UserID: scope.userID,
		}, 0)
		readDone <- readResult{entries: entries, err: err}
	}()
	select {
	case <-secondPageStarted:
	case <-ctx.Done():
		t.Fatal("second read page did not start")
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseSecondPage)
		})
	}
	defer release()
	lock := service.writeLock(scope)
	readHoldsLock := !tryAcquireScopeGate(lock)
	if !readHoldsLock {
		lock.release()
	}
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- service.DeleteMemory(ctx, memory.Key{
			AppName: scope.appName, UserID: scope.userID, MemoryID: "id-000",
		})
	}()
	var result readResult
	var deleteErr error
	if readHoldsLock {
		release()
		result = <-readDone
		deleteErr = <-deleteDone
	} else {
		deleteErr = <-deleteDone
		release()
		result = <-readDone
	}

	require.NoError(t, deleteErr)
	require.NoError(t, result.err)
	assert.Equal(t, recordCount, len(result.entries))
	foundLastRecord := false
	for _, entry := range result.entries {
		if entry.ID == lastID {
			foundLastRecord = true
			break
		}
	}
	assert.True(t, foundLastRecord, "last paginated record was skipped")
}

func TestServiceReadMemoriesRejectsPaginationWithoutProgress(t *testing.T) {
	service, fake := newTestChromaService(t, &testEmbedder{dimension: 3})
	scope := recordScope{appName: "app", userID: "user"}
	putFakeRecord(fake, newAddRecord(scope, "memory", nil, nil, time.Now().UTC()))
	fake.mu.Lock()
	fake.ignoreGetOffset = true
	fake.mu.Unlock()

	_, err := service.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: scope.appName, UserID: scope.userID},
		0,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pagination made no progress")
}

func TestDecodeGetResponseRejectsMalformedColumns(t *testing.T) {
	document := "memory"
	documents := []*string{&document}
	tests := []struct {
		name     string
		response *getRecordsResponse
		match    string
	}{
		{name: "nil", response: nil, match: "nil response"},
		{
			name: "missing columns",
			response: &getRecordsResponse{
				IDs: responseField[[]string]{
					value: []string{"id"}, present: true,
				},
			},
			match: "did not include",
		},
		{
			name: "column mismatch",
			response: &getRecordsResponse{
				IDs: responseField[[]string]{
					value: []string{"id"}, present: true,
				},
				Documents: &documents,
				Metadatas: &[]map[string]any{},
			},
			match: "column length mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeGetResponse(tt.response)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func putFakeRecord(fake *fakeChroma, record *storedRecord) {
	document := record.entry.Memory.Memory
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.records[record.entry.ID] = &fakeRecord{
		document:  &document,
		embedding: []float32{1, 0, 0},
		metadata:  addMetadata(record),
	}
}
