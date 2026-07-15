//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package schemaversion

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReporterReceivesRegisteredVersions(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	Register("example/session/mysql", "v1")
	Register("", "v1")
	Register("example/session/redis", "")

	got := make(map[string]string)
	Observe(func(modulePath, version string) {
		got[modulePath] = version
	})
	require.Equal(t, map[string]string{
		"example/session/mysql": "v1",
	}, got)

	Register("example/session/redis", "v1")
	require.Equal(t, "v1", got["example/session/redis"])

	replayed := make(map[string]string)
	Observe(func(modulePath, version string) {
		replayed[modulePath] = version
	})
	require.Equal(t, got, replayed)
}

func TestRegisterKeepsFirstVersion(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	var calls int
	Observe(func(_, _ string) {
		calls++
	})
	Register("example/session/mysql", "v1")
	Register("example/session/mysql", "v1")
	Register("example/session/mysql", "v2")
	require.Equal(t, 1, calls)
}

func TestRegisterConcurrent(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	var mu sync.Mutex
	got := make(map[string]string)
	Observe(func(modulePath, version string) {
		mu.Lock()
		got[modulePath] = version
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for _, modulePath := range []string{
		"example/session/mysql",
		"example/session/redis",
		"example/session/postgres",
	} {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			Register(path, "v1")
		}(modulePath)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 3)
}

func resetRegistry() {
	registry.Lock()
	defer registry.Unlock()
	registry.versions = make(map[string]string)
	registry.reporters = nil
}
