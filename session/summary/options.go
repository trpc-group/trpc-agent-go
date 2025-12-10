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
)

// Option is a function that configures a SessionSummarizer.
type Option func(*sessionSummarizer)

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

// WithSkipRecentEvents sets the number of recent events to skip during summarization.
// These events will be excluded from the summary input but remain in the session.
// A value <= 0 means no events are skipped.
func WithSkipRecentEvents(count int) Option {
	return func(s *sessionSummarizer) {
		if count > 0 {
			s.skipRecentEvents = count
		}
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
