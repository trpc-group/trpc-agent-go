//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
)

// TestAddMemoryUpsertAtCapacity verifies that updating existing memory entries is allowed at capacity.
func TestAddMemoryUpsertAtCapacity(t *testing.T) {
	eventTime := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		metadata *memory.Metadata
	}{
		{name: "fact"},
		{name: "episode", metadata: &memory.Metadata{
			Kind: memory.KindEpisode, EventTime: &eventTime, Location: "office",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			service := inmemory.NewMemoryService(inmemory.WithMemoryLimit(1))
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			key := memory.UserKey{AppName: "app", UserID: "user"}
			opts := []memory.AddOption{memory.WithMetadata(tc.metadata)}
			require.NoError(t, service.AddMemory(ctx, key, "existing memory", []string{"old"}, opts...))
			before, err := service.ReadMemories(ctx, key, 0)
			require.NoError(t, err)
			require.Len(t, before, 1)

			// Retrying the same entry and refreshing its topics need no extra capacity.
			require.NoError(t, service.AddMemory(ctx, key, "existing memory", []string{"old"}, opts...))
			require.NoError(t, service.AddMemory(ctx, key, "existing memory", []string{"new"}, opts...))
			require.ErrorContains(t, service.AddMemory(ctx, key, "different memory", nil, opts...), "memory limit exceeded")
			if tc.metadata != nil {
				other := *tc.metadata
				other.Location = "home"
				require.ErrorContains(t, service.AddMemory(ctx, key, "existing memory", nil,
					memory.WithMetadata(&other)), "memory limit exceeded")
			}

			after, err := service.ReadMemories(ctx, key, 0)
			require.NoError(t, err)
			require.Len(t, after, 1)
			require.Equal(t, before[0].ID, after[0].ID)
			require.Equal(t, "existing memory", after[0].Memory.Memory)
			require.Equal(t, []string{"new"}, after[0].Memory.Topics)
		})
	}
}

// TestAddMemoryConcurrentUpsertsAtCapacity verifies that concurrent updates preserve the size limit.
func TestAddMemoryConcurrentUpsertsAtCapacity(t *testing.T) {
	ctx := context.Background()
	service := inmemory.NewMemoryService(inmemory.WithMemoryLimit(1))
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	key := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, service.AddMemory(ctx, key, "existing memory", nil))

	const callers = 16
	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			content := "existing memory"
			if i%2 != 0 {
				content = fmt.Sprintf("new memory %d", i)
			}
			errs[i] = service.AddMemory(ctx, key, content, nil)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if i%2 == 0 {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, "memory limit exceeded")
		}
	}
	entries, err := service.ReadMemories(ctx, key, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "existing memory", entries[0].Memory.Memory)
}
