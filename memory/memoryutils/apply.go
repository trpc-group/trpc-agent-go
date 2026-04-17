//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryutils

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// ApplyMetadata merges episodic metadata from memory.AddMemory options into mem,
// then normalizes fields. External persistence backends (e.g. custom SQL adapters)
// should use this on add paths so behavior matches built-in memory services.
func ApplyMetadata(mem *memory.Memory, ep *memory.Metadata) {
	imemory.ApplyMetadata(mem, ep)
}

// ApplyMetadataPatch merges update-time metadata into mem. Zero values on ep mean
// "leave existing stored metadata unchanged" for that field.
func ApplyMetadataPatch(mem *memory.Memory, ep *memory.Metadata) {
	imemory.ApplyMetadataPatch(mem, ep)
}

// ApplyMemoryUpdate applies content, topics, and optional metadata in-place on
// entry and returns the canonical memory ID after normalization (same contract as
// the MySQL/Postgres/SQLite memory services).
func ApplyMemoryUpdate(
	entry *memory.Entry,
	appName, userID, memoryStr string,
	topics []string,
	ep *memory.Metadata,
	now time.Time,
) string {
	return imemory.ApplyMemoryUpdate(entry, appName, userID, memoryStr, topics, ep, now)
}
