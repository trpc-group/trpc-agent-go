//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ExtractionContext contains all context information for extraction decision.
// This struct provides rich context for Checker functions to make decisions.
// Fields may be extended in future versions without breaking existing Checkers.
type ExtractionContext struct {
	// UserKey identifies the user for memory extraction.
	UserKey memory.UserKey

	// Messages contains the actual messages extracted from session events.
	// Only includes user/assistant messages with content, excluding tool calls.
	Messages []model.Message

	// LastExtractAt is the last extraction timestamp, nil if never extracted.
	LastExtractAt *time.Time
}

// Checker defines a function type for checking if extraction is needed.
// A Checker inspects the provided context and returns true when extraction
// should be triggered based on its own criterion.
// Multiple checkers can be composed using ChecksAll (AND) or ChecksAny (OR).
type Checker func(ctx *ExtractionContext) bool

// CheckMessageThreshold creates a checker that triggers when the number of
// extracted messages exceeds the specified threshold.
// This checks the actual message count, not event count.
func CheckMessageThreshold(n int) Checker {
	return func(ctx *ExtractionContext) bool {
		return len(ctx.Messages) > n
	}
}

// CheckTimeInterval creates a checker that triggers if last extraction
// was more than the given duration ago.
func CheckTimeInterval(interval time.Duration) Checker {
	return func(ctx *ExtractionContext) bool {
		if ctx.LastExtractAt == nil {
			return true // First extraction.
		}
		return time.Since(*ctx.LastExtractAt) > interval
	}
}

// ChecksAll composes multiple checkers using AND logic.
// It returns true only if all provided checkers return true.
// Returns true if no checkers are provided (empty AND).
func ChecksAll(checks ...Checker) Checker {
	return func(ctx *ExtractionContext) bool {
		for _, check := range checks {
			if !check(ctx) {
				return false
			}
		}
		return true
	}
}

// ChecksAny composes multiple checkers using OR logic.
// It returns true if any one of the provided checkers returns true.
// Returns false if no checkers are provided (empty OR).
func ChecksAny(checks ...Checker) Checker {
	return func(ctx *ExtractionContext) bool {
		for _, check := range checks {
			if check(ctx) {
				return true
			}
		}
		return false
	}
}
