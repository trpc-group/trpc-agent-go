//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Option is a function that configures a SessionSummarizer.
type Option func(*sessionSummarizer)

// SkipRecentFunc defines a function that determines how many recent events to skip during summarization.
// It receives all events and returns the number of recent events to skip.
// Return 0 to skip no events.
type SkipRecentFunc func(events []event.Event) int

// WithPrompt sets the custom prompt for summarization.
// The prompt must include the placeholder {conversation_text}, which will be
// replaced with the extracted conversation when generating the summary.
func WithPrompt(prompt string) Option {
	return func(s *sessionSummarizer) {
		if prompt != "" {
			s.prompt = prompt
		}
	}
}

// WithMaxSummaryWords sets the maximum word count for summaries.
// A value <= 0 means no word limit. The word limit will be included in the
// prompt to guide the model's generation rather than truncating the output.
func WithMaxSummaryWords(maxWords int) Option {
	return func(s *sessionSummarizer) {
		if maxWords > 0 {
			s.maxSummaryWords = maxWords
		}
	}
}

// WithSkipRecent sets a custom function to determine how many of the most recent
// events (from the tail) should be skipped during summarization. The function
// receives all events and returns the count of tail events to skip. Return 0 to
// skip none.
//
// Example:
//
//	WithSkipRecent(func(events []event.Event) int {
//	    // Skip the last 3 events
//	    return 3
//	})
//
//	WithSkipRecent(func(events []event.Event) int {
//	    // Skip events from the last 5 minutes
//	    cutoff := time.Now().Add(-5 * time.Minute)
//	    skipCount := 0
//	    for i := len(events) - 1; i >= 0; i-- {
//	        if events[i].Timestamp.After(cutoff) {
//	            skipCount++
//	        } else {
//	            break
//	        }
//	    }
//	    return skipCount
//	})
func WithSkipRecent(skipFunc SkipRecentFunc) Option {
	return func(s *sessionSummarizer) {
		s.skipRecentFunc = skipFunc
	}
}

// WithTokenThreshold creates a token-based check function.
func WithTokenThreshold(tokenCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, CheckTokenThreshold(tokenCount))
	}
}

// WithEventThreshold creates an event-count-based check function.
func WithEventThreshold(eventCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, CheckEventThreshold(eventCount))
	}
}

// WithTimeThreshold creates a time-based check function.
func WithTimeThreshold(interval time.Duration) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, CheckTimeThreshold(interval))
	}
}

// WithChecksAll appends a single composite check that requires all provided checks (AND logic).
func WithChecksAll(checks ...Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = append(s.checks, ChecksAll(checks))
		}
	}
}

// WithChecksAny appends a single composite check that passes if any provided check passes (OR logic).
func WithChecksAny(checks ...Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = append(s.checks, ChecksAny(checks))
		}
	}
}

// WithPreSummaryHook sets a pre-summary hook to modify text before the model call.
func WithPreSummaryHook(h PreSummaryHook) Option {
	return func(s *sessionSummarizer) {
		s.preHook = h
	}
}

// WithPostSummaryHook sets a post-summary hook to modify the summary before returning.
func WithPostSummaryHook(h PostSummaryHook) Option {
	return func(s *sessionSummarizer) {
		s.postHook = h
	}
}

// WithSummaryHookAbortOnError decides whether to abort when a hook returns an error.
// Default false: ignore hook errors and use original text/summary; true: return error.
func WithSummaryHookAbortOnError(abort bool) Option {
	return func(s *sessionSummarizer) {
		s.hookAbortOnError = abort
	}
}
