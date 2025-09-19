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

// WithChecks sets the check functions for determining when to summarize.
func WithChecks(checks []Checker) Option {
	return func(s *sessionSummarizer) {
		if checks != nil {
			s.checks = checks
		}
	}
}

// WithMaxSummaryLength sets the maximum length for generated summaries.
func WithMaxSummaryLength(maxSummaryLength int) Option {
	return func(s *sessionSummarizer) {
		if maxSummaryLength > 0 {
			s.maxSummaryLength = maxSummaryLength
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
		if len(checks) == 0 {
			return
		}
		s.checks = append(s.checks, ChecksAll(checks))
	}
}

// WithChecksAny appends a single composite check that passes if any provided check passes (OR logic).
func WithChecksAny(checks ...Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) == 0 {
			return
		}
		s.checks = append(s.checks, ChecksAny(checks))
	}
}
