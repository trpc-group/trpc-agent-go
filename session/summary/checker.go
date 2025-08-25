//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Checker defines a function type for checking if summarization is needed.
type Checker func(sess *session.Session) bool

// SetEventThreshold creates a checker that triggers when event count exceeds threshold.
func SetEventThreshold(eventCount int) Checker {
	return func(sess *session.Session) bool {
		return len(sess.Events) >= eventCount
	}
}

// SetTimeThreshold creates a checker that triggers when time since last event exceeds interval.
func SetTimeThreshold(interval time.Duration) Checker {
	return func(sess *session.Session) bool {
		if len(sess.Events) == 0 {
			return false
		}
		lastEvent := sess.Events[len(sess.Events)-1]
		return time.Since(lastEvent.Timestamp) >= interval
	}
}

// SetTokenThreshold creates a checker that triggers when estimated token count exceeds threshold.
func SetTokenThreshold(tokenCount int) Checker {
	return func(sess *session.Session) bool {
		if len(sess.Events) == 0 {
			return false
		}

		totalTokens := 0
		for _, event := range sess.Events {
			if event.Response != nil && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				// Rough estimation: 1 token ≈ 4 characters.
				totalTokens += len(content) / 4
			}
		}

		return totalTokens > tokenCount
	}
}

// SetImportantThreshold creates a checker that triggers when content exceeds character threshold.
func SetImportantThreshold(charCount int) Checker {
	return func(sess *session.Session) bool {
		if len(sess.Events) == 0 {
			return false
		}

		totalChars := 0
		for _, event := range sess.Events {
			if event.Response != nil && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				totalChars += len(strings.TrimSpace(content))
			}
		}

		return totalChars > charCount
	}
}

// SetConversationThreshold creates a checker that triggers when conversation count exceeds threshold.
func SetConversationThreshold(conversationCount int) Checker {
	return func(sess *session.Session) bool {
		return len(sess.Events) >= conversationCount
	}
}

// SetChecksAll creates a checker that requires all checks to pass (AND logic).
func SetChecksAll(checks []Checker) Checker {
	return func(sess *session.Session) bool {
		for _, check := range checks {
			if !check(sess) {
				return false
			}
		}
		return true
	}
}

// SetChecksAny creates a checker that requires any check to pass (OR logic).
func SetChecksAny(checks []Checker) Checker {
	return func(sess *session.Session) bool {
		for _, check := range checks {
			if check(sess) {
				return true
			}
		}
		return false
	}
}
