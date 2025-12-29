//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import "sync"

var (
	registryMu     sync.RWMutex
	qdrantRegistry = make(map[string][]ClientBuilderOpt)
)

// RegisterQdrantInstance registers a named Qdrant instance with its configuration options.
// If an instance with the same name already exists, it will be overwritten.
func RegisterQdrantInstance(name string, opts ...ClientBuilderOpt) {
	registryMu.Lock()
	defer registryMu.Unlock()
	qdrantRegistry[name] = opts
}

// GetQdrantInstance retrieves the configuration options for a named Qdrant instance.
// Returns a copy of the options and true if found, or nil and false if not found.
func GetQdrantInstance(name string) ([]ClientBuilderOpt, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	opts, ok := qdrantRegistry[name]
	if !ok {
		return nil, false
	}
	// copy to prevent external modifications
	copyOpts := make([]ClientBuilderOpt, len(opts))
	copy(copyOpts, opts)
	return copyOpts, true
}

// UnregisterQdrantInstance removes a named Qdrant instance from the registry.
func UnregisterQdrantInstance(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(qdrantRegistry, name)
}

// ListQdrantInstances returns a list of all registered instance names.
func ListQdrantInstances() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(qdrantRegistry))
	for name := range qdrantRegistry {
		names = append(names, name)
	}
	return names
}
