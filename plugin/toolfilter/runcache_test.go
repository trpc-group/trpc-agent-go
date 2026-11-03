//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunCache_EvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	cache := newRunCache[int](2)
	cache.set("a", 1)
	cache.set("b", 2)

	v, ok := cache.get("a")
	require.True(t, ok)
	require.Equal(t, 1, v) // "a" is now most recent

	cache.set("c", 3) // evicts "b"
	_, ok = cache.get("b")
	require.False(t, ok)
	v, ok = cache.get("a")
	require.True(t, ok)
	require.Equal(t, 1, v)

	cache.set("a", 9) // update in place
	v, ok = cache.get("a")
	require.True(t, ok)
	require.Equal(t, 9, v)
	require.Equal(t, 2, cache.len())
}

func TestRunCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	cache := newRunCache[int](16)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + i%26))
			cache.set(key, i)
			cache.get(key)
			cache.len()
		}(i)
	}
	wg.Wait()
	require.LessOrEqual(t, cache.len(), 16)
}

func TestRunCache_NilSafety(t *testing.T) {
	t.Parallel()

	var cache *runCache[int]
	_, ok := cache.get("a")
	require.False(t, ok)
	cache.set("a", 1)
	require.Equal(t, 0, cache.len())
}
