//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Option is a function that configures a SessionSummarizer.
type Option func(*sessionSummarizer)

// WithModel sets the model for summarization.
func WithModel(model model.Model) Option {
	return func(s *sessionSummarizer) {
		s.model = model
	}
}

// WithPrompt sets the custom prompt for summarization.
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

// WithMaxLength sets the maximum length for generated summaries.
func WithMaxLength(maxLength int) Option {
	return func(s *sessionSummarizer) {
		if maxLength > 0 {
			s.maxLength = maxLength
		}
	}
}

// WithKeepRecent sets the number of recent events to keep after summarization.
func WithKeepRecent(keepRecent int) Option {
	return func(s *sessionSummarizer) {
		if keepRecent > 0 {
			s.keepRecent = keepRecent
		}
	}
}

// WithTokenThreshold creates a token-based check function.
func WithTokenThreshold(tokenCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, SetTokenThreshold(tokenCount))
	}
}

// WithEventThreshold creates an event-count-based check function.
func WithEventThreshold(eventCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, SetEventThreshold(eventCount))
	}
}

// WithTimeThreshold creates a time-based check function.
func WithTimeThreshold(interval time.Duration) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, SetTimeThreshold(interval))
	}
}

// WithImportantThreshold creates an important-content-based check function.
func WithImportantThreshold(importantCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, SetImportantThreshold(importantCount))
	}
}

// WithConversationThreshold creates a conversation-count-based check function.
func WithConversationThreshold(conversationCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, SetConversationThreshold(conversationCount))
	}
}

// WithChecksAll sets all provided checks to be required (AND logic).
func WithChecksAll(checks []Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = []Checker{SetChecksAll(checks)}
		}
	}
}

// WithChecksAny sets any of the provided checks to be sufficient (OR logic).
func WithChecksAny(checks []Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = []Checker{SetChecksAny(checks)}
		}
	}
}

// ManagerOption is a function that configures a SummarizerManager.
type ManagerOption func(*summarizerManager)

// WithAutoSummarize enables or disables automatic summarization.
func WithAutoSummarize(enabled bool) ManagerOption {
	return func(m *summarizerManager) {
		m.autoSummarize = enabled
	}
}

// WithBaseService sets the base session service.
func WithBaseService(service session.Service) ManagerOption {
	return func(m *summarizerManager) {
		m.baseService = service
	}
}
