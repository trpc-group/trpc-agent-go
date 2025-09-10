//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Checker defines a function type for checking if summarization is needed.
// A Checker inspects the provided session and returns true when a
// summarization should be triggered based on its own criterion.
// Multiple checkers can be composed using SetChecksAll (AND) or SetChecksAny (OR).
// When no custom checkers are supplied, a default set is used.
type Checker func(sess *session.Session) bool

// SetEventThreshold creates a checker that triggers when the total number of
// events in the session is greater than or equal to the specified threshold.
// This is a simple proxy for conversation growth and is inexpensive to compute.
// Example: SetEventThreshold(30) will trigger once there are at least 30 events.
func SetEventThreshold(eventCount int) Checker {
	return func(sess *session.Session) bool {
		return len(sess.Events) >= eventCount
	}
}

// SetTimeThreshold creates a checker that triggers when the time elapsed since
// the last event is greater than or equal to the given interval.
// This is useful to ensure periodic summarization in long-running sessions.
// Example: SetTimeThreshold(5*time.Minute) triggers if no events occurred in five minutes.
func SetTimeThreshold(interval time.Duration) Checker {
	return func(sess *session.Session) bool {
		if len(sess.Events) == 0 {
			return false
		}
		lastEvent := sess.Events[len(sess.Events)-1]
		return time.Since(lastEvent.Timestamp) >= interval
	}
}

// SetTokenThreshold creates a checker that triggers when the approximate token
// count of the accumulated messages exceeds the given threshold.
// Tokens are estimated naïvely as len(content)/4 for simplicity and speed.
// This estimation is coarse and model-agnostic but good enough for gating.
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

// SetImportantThreshold creates a checker that triggers when the combined
// character count of messages (trimmed) exceeds the given threshold.
// This provides a simple importance heuristic aligned with content density.
// For more advanced signals, integrate a separate importance detector upstream.
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

// SetChecksAll composes multiple checkers using AND logic.
// It returns true only if all provided checkers return true.
// Use this to enforce stricter summarization gates.
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

// SetChecksAny composes multiple checkers using OR logic.
// It returns true if any one of the provided checkers returns true.
// Use this to allow flexible, opportunistic summarization triggers.
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
