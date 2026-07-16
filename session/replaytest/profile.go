// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

// InMemoryProfile returns the capability profile for in-memory backends.
func InMemoryProfile() BackendProfile {
	return BackendProfile{
		Name:                 "inmemory",
		SupportsTrack:        true,
		SupportsAppState:     true,
		SupportsUserState:    true,
		SupportsSessionState: true,
		SupportsAsyncSummary: true,
		SupportsMemory:       true,
	}
}

// SQLiteProfile returns the capability profile for SQLite backends.
func SQLiteProfile() BackendProfile {
	return BackendProfile{
		Name:                 "sqlite",
		SupportsTrack:        true,
		SupportsAppState:     true,
		SupportsUserState:    true,
		SupportsSessionState: true,
		SupportsAsyncSummary: true,
		SupportsMemory:       true,
	}
}

// RedisProfile returns the capability profile for Redis backends.
func RedisProfile() BackendProfile {
	return BackendProfile{
		Name:                 "redis",
		SupportsTrack:        true,
		SupportsAppState:     true,
		SupportsUserState:    true,
		SupportsSessionState: true,
		SupportsAsyncSummary: true,
		SupportsMemory:       true,
	}
}

// MissingCaps returns human-readable missing capabilities for a case.
func MissingCaps(c Caps, p BackendProfile) []string {
	var missing []string
	if c.NeedsTrack && !p.SupportsTrack {
		missing = append(missing, "track")
	}
	if c.NeedsMemory && !p.SupportsMemory {
		missing = append(missing, "memory")
	}
	if c.NeedsAsyncSummary && !p.SupportsAsyncSummary {
		missing = append(missing, "async_summary")
	}
	return missing
}
