//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Checker defines a function type for checking if summarization is needed.
// A Checker inspects the provided session and returns true when a
// summarization should be triggered based on its own criterion.
// Multiple checkers can be composed using SetChecksAll (AND) or SetChecksAny (OR).
// When no custom checkers are supplied, a default set is used.
type Checker func(sess *session.Session) bool

var (
	defaultTokenCounterMu sync.RWMutex
	defaultTokenCounter   model.TokenCounter = model.NewSimpleTokenCounter()
)

func getTokenCounter() model.TokenCounter {
	defaultTokenCounterMu.RLock()
	counter := defaultTokenCounter
	defaultTokenCounterMu.RUnlock()

	if counter == nil {
		return model.NewSimpleTokenCounter()
	}
	return counter
}

// SetTokenCounter sets the default TokenCounter used by summary checkers.
// This affects all future CheckTokenThreshold evaluations in this process.
func SetTokenCounter(counter model.TokenCounter) {
	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}

	defaultTokenCounterMu.Lock()
	defaultTokenCounter = counter
	defaultTokenCounterMu.Unlock()
}

// filterDeltaEvents returns events that occurred strictly after the last
// summarized timestamp stored in session state. If the timestamp is not set
// or invalid, it returns all events (first summarization scenario).
func filterDeltaEvents(sess *session.Session) []event.Event {
	if sess == nil || len(sess.Events) == 0 {
		return nil
	}

	raw, ok := sess.GetState(lastIncludedTsKey)
	if !ok || len(raw) == 0 {
		return sess.Events
	}

	lastTs, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		log.Warnf(
			"invalid %s in session state (session_id=%s): %v",
			lastIncludedTsKey,
			sess.ID,
			err,
		)
		return sess.Events
	}

	out := make([]event.Event, 0, len(sess.Events))
	for _, e := range sess.Events {
		if e.Timestamp.After(lastTs) {
			out = append(out, e)
		}
	}
	return out
}

// CheckEventThreshold creates a checker that triggers when the number of events
// since the last summary exceeds the given threshold.
func CheckEventThreshold(eventCount int) Checker {
	return func(sess *session.Session) bool {
		delta := filterDeltaEvents(sess)
		return len(delta) > eventCount
	}
}

// CheckTimeThreshold creates a checker that triggers when the time elapsed
// since the last event is greater than the given interval.
func CheckTimeThreshold(interval time.Duration) Checker {
	return func(sess *session.Session) bool {
		if sess == nil || len(sess.Events) == 0 {
			return false
		}
		lastEvent := sess.Events[len(sess.Events)-1]
		return time.Since(lastEvent.Timestamp) > interval
	}
}

// checkTokenThresholdFromText checks if the token count of the given text exceeds the threshold.
func checkTokenThresholdFromText(tokenCount int, conversationText string) bool {
	if conversationText == "" {
		return false
	}

	// SimpleTokenCounter.CountTokens currently never returns an error.
	tokens, _ := getTokenCounter().CountTokens(
		context.Background(),
		model.Message{Content: conversationText},
	)
	return tokens > tokenCount
}

// CheckTokenThreshold creates a checker that triggers when the estimated token
// count of the events since the last summary exceeds the given threshold.
//
// Note:
// Token accounting via model usage is not stable once session summary injection
// is enabled. For consistent gating, we estimate tokens from the delta events.
func CheckTokenThreshold(tokenCount int) Checker {
	return func(sess *session.Session) bool {
		delta := filterDeltaEvents(sess)
		if len(delta) == 0 {
			return false
		}

		conversationText := extractConversationText(delta, nil, nil)
		return checkTokenThresholdFromText(tokenCount, conversationText)
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
