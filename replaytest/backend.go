//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SessionFactory creates a session.Service given a backend name and optional database URL.
// The factory is responsible for all backend-specific setup.
type SessionFactory func(ctx context.Context, dbURL string) (session.Service, error)

// MemoryFactory creates a memory.Service given a backend name and optional database URL.
// The factory is responsible for all backend-specific setup.
type MemoryFactory func(ctx context.Context, dbURL string) (memory.Service, error)

var (
	sessionFactoriesMu sync.RWMutex
	sessionFactories   = map[string]SessionFactory{}

	memoryFactoriesMu sync.RWMutex
	memoryFactories   = map[string]MemoryFactory{}
)

// RegisterSessionFactory registers a named session backend factory.
// Backend names are case-sensitive. Common values: "inmemory", "sqlite".
func RegisterSessionFactory(name string, f SessionFactory) {
	sessionFactoriesMu.Lock()
	defer sessionFactoriesMu.Unlock()
	sessionFactories[name] = f
}

// RegisterMemoryFactory registers a named memory backend factory.
// Backend names are case-sensitive. Common values: "inmemory", "sqlite".
func RegisterMemoryFactory(name string, f MemoryFactory) {
	memoryFactoriesMu.Lock()
	defer memoryFactoriesMu.Unlock()
	memoryFactories[name] = f
}

// RegisteredSessionBackends returns the sorted list of registered session backend names.
func RegisteredSessionBackends() []string {
	sessionFactoriesMu.RLock()
	defer sessionFactoriesMu.RUnlock()
	names := make([]string, 0, len(sessionFactories))
	for name := range sessionFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RegisteredMemoryBackends returns the sorted list of registered memory backend names.
func RegisteredMemoryBackends() []string {
	memoryFactoriesMu.RLock()
	defer memoryFactoriesMu.RUnlock()
	names := make([]string, 0, len(memoryFactories))
	for name := range memoryFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NewSessionService creates a session.Service for the named backend.
func NewSessionService(ctx context.Context, name string, dbURL string) (session.Service, error) {
	sessionFactoriesMu.RLock()
	f, ok := sessionFactories[name]
	sessionFactoriesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session backend %q not registered, available: %v",
			name, RegisteredSessionBackends())
	}
	return f(ctx, dbURL)
}

// NewMemoryService creates a memory.Service for the named backend.
func NewMemoryService(ctx context.Context, name string, dbURL string) (memory.Service, error) {
	memoryFactoriesMu.RLock()
	f, ok := memoryFactories[name]
	memoryFactoriesMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("memory backend %q not registered, available: %v",
			name, RegisteredMemoryBackends())
	}
	return f(ctx, dbURL)
}
