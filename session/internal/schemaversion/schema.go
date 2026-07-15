//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package schemaversion tracks the canonical schema versions of created session backends.
package schemaversion

import "sync"

var registry = struct {
	sync.Mutex
	versions  map[string]string
	reporters []func(modulePath, version string)
}{
	versions: make(map[string]string),
}

// Register records the canonical schema version of a successfully created session backend.
func Register(modulePath, version string) {
	if modulePath == "" || version == "" {
		return
	}

	registry.Lock()
	if _, ok := registry.versions[modulePath]; ok {
		registry.Unlock()
		return
	}
	registry.versions[modulePath] = version
	reporters := append([]func(string, string){}, registry.reporters...)
	registry.Unlock()

	for _, reporter := range reporters {
		reporter(modulePath, version)
	}
}

// Observe adds a reporter and replays all versions registered before it.
func Observe(reporter func(modulePath, version string)) {
	if reporter == nil {
		return
	}

	registry.Lock()
	registry.reporters = append(registry.reporters, reporter)
	versions := make(map[string]string, len(registry.versions))
	for modulePath, version := range registry.versions {
		versions[modulePath] = version
	}
	registry.Unlock()

	for modulePath, version := range versions {
		reporter(modulePath, version)
	}
}
