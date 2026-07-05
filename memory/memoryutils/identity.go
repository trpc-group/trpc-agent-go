//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package memoryutils provides identity and mutation helpers for memory records:
// stable IDs, kind normalization, participant metadata, and ApplyMetadata-style
// entry updates shared with built-in persistence backends.
package memoryutils

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// GenerateMemoryID generates a unique ID for memory based on content,
// user context, and canonical episodic metadata.
// Topics are intentionally excluded so that topic drift does not change
// identity, while event metadata is included so distinct episodes with
// the same text do not collapse into a single upsert key.
//
// External persistence backends should use this function so row keys match
// built-in memory service implementations.
func GenerateMemoryID(mem *memory.Memory, appName, userID string) string {
	return imemory.GenerateMemoryID(mem, appName, userID)
}

// EffectiveKind returns the runtime memory kind. Legacy records that did not
// persist kind explicitly are treated as facts.
func EffectiveKind(mem *memory.Memory) memory.Kind {
	return imemory.EffectiveKind(mem)
}

// NormalizeMemory canonicalizes memory metadata for runtime use and new writes.
func NormalizeMemory(mem *memory.Memory) {
	imemory.NormalizeMemory(mem)
}
