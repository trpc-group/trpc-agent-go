//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package assistantmemory defines the private marker shared by assistant
// episode extraction and auto-memory reconciliation.
package assistantmemory

import "strings"

const (
	// Prefix identifies assistant-provided conversation episodes stored as
	// ordinary episodic memories.
	Prefix = "Assistant-provided conversation episode: "
	// MetadataKey identifies assistant episode extraction in extractor metadata.
	MetadataKey = "conversation_extraction"
	// MetadataValue is the metadata value for assistant episode extraction.
	MetadataValue = "assistant-episode"
)

// IsText reports whether text contains the private assistant episode marker.
func IsText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), Prefix)
}
