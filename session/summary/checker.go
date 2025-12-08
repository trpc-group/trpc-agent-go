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

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Checker defines a function type for checking if summarization is needed.
// A Checker inspects the provided session and returns true when a
// summarization should be triggered based on its own criterion.
// Multiple checkers can be composed using SetChecksAll (AND) or SetChecksAny (OR).
// When no custom checkers are supplied, a default set is used.
type Checker func(sess *session.Session) bool

// CheckEventThreshold creates a checker that triggers when the total number of
// events in the session is greater than the specified threshold.
// This is a simple proxy for conversation growth and is inexpensive to compute.
// Example: CheckEventThreshold(30) will trigger once there are at least 30 events.
func CheckEventThreshold(eventCount int) Checker {
	return func(sess *session.Session) bool {
		return len(sess.Events) > eventCount
	}
}

// CheckTimeThreshold creates a checker that triggers when the time elapsed since
// the last event is greater than to the given interval.
// This is useful to ensure periodic summarization in long-running sessions.
// Example: CheckTimeThreshold(5*time.Minute) triggers if no events occurred in five minutes.
func CheckTimeThreshold(interval time.Duration) Checker {
	return func(sess *session.Session) bool {
		if len(sess.Events) == 0 {
			return false
		}
		lastEvent := sess.Events[len(sess.Events)-1]
		return time.Since(lastEvent.Timestamp) > interval
	}
}

// CheckTokenThreshold creates a checker that triggers when the approximate token
// count of the accumulated messages exceeds the given threshold.
// Tokens are estimated naïvely as len(content)/4 for simplicity and speed.
// This estimation is coarse and model-agnostic but good enough for gating.
func CheckTokenThreshold(tokenCount int) Checker {
	return func(sess *session.Session) bool {
		if len(sess.Events) == 0 {
			return false
		}

		invocationTokens := make(map[string]int)
		for _, event := range sess.Events {
			if event.Response == nil || event.Response.Usage == nil {
				continue
			}
			invocationID := event.InvocationID
			if event.Response.Usage.TotalTokens > 0 {
				invocationTokens[invocationID] = event.Response.Usage.TotalTokens
			}
		}

		totalTokens := 0
		for _, tokens := range invocationTokens {
			totalTokens += tokens
		}

		return totalTokens > tokenCount
	}
}

// ChecksAll composes multiple checkers using AND logic.
// It returns true only if all provided checkers return true.
// Use this to enforce stricter summarization gates.
func ChecksAll(checks []Checker) Checker {
	return func(sess *session.Session) bool {
		for _, check := range checks {
			if !check(sess) {
				return false
			}
		}
		return true
	}
}

// ChecksAny composes multiple checkers using OR logic.
// It returns true if any one of the provided checkers returns true.
// Use this to allow flexible, opportunistic summarization triggers.
func ChecksAny(checks []Checker) Checker {
	return func(sess *session.Session) bool {
		for _, check := range checks {
			if check(sess) {
				return true
			}
		}
		return false
	}
}
