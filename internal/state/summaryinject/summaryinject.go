//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summaryinject carries the session summary selected while building
// one model request so the final request boundary can report whether the
// recorded summary block text still appears in any message Content after
// GenerateContent returns.
package summaryinject

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const stateKey = "trpc_agent.summary.injection"

// Lookup strategies describe the scope a request was configured to search.
// They are stable diagnostic values and never contain a filter key.
const (
	// LookupStrategyExact searches only the request's own filter key.
	LookupStrategyExact = "exact"
	// LookupStrategyPrefix searches the request's key and its hierarchical
	// descendants.
	LookupStrategyPrefix = "prefix"
	// LookupStrategyAll searches the full-session summary regardless of the
	// request's own filter key.
	LookupStrategyAll = "all"
	// LookupStrategyNone marks a request that does not use session summaries.
	LookupStrategyNone = "none"
)

// Lookup results describe what the configured strategy actually found. They
// are reported separately from the strategy so a miss keeps the scope that was
// searched.
const (
	// LookupResultExact marks a summary found under the searched key.
	LookupResultExact = "exact"
	// LookupResultPrefix marks summaries aggregated from a key prefix.
	LookupResultPrefix = "prefix"
	// LookupResultNone marks a lookup that found nothing in scope.
	LookupResultNone = "none"
)

// Selection records the session summary lookup performed for one model
// request. It is request-scoped state, not a persisted or serialized contract.
type Selection struct {
	// LookupStrategy reports the scope the request was configured to search.
	LookupStrategy string
	// LookupResult reports what that scope found.
	LookupResult string
	// Selected reports whether a non-empty summary was found.
	Selected bool
	// BoundaryPresent reports whether the selected summary carries a usable
	// history cutoff.
	BoundaryPresent bool
	// StoredSummaries is the number of non-empty stored summaries the session
	// held at selection time, across all filter keys.
	StoredSummaries int
	// MatchingCandidates is the number of stored summaries inside the
	// configured lookup scope.
	MatchingCandidates int
	// FullSessionPresent reports whether a non-empty full-session summary was
	// stored, whether or not it was inside the configured scope.
	FullSessionPresent bool
	// ScopedRequest reports whether the request searched a non-empty branch
	// key, so a miss can be separated from a full-session lookup.
	ScopedRequest bool
	// SessionEvents is the number of stored session events at selection time.
	SessionEvents int
	// HistoryMessages is the number of history messages that survived the
	// summary cutoff, counted before any synthetic user-context message is
	// prepended.
	HistoryMessages int
	// Block is the formatted summary block written into the request. It is
	// retained only to detect the same text in any message Content and is
	// never logged.
	Block string
}

// ScopeMismatch reports a lookup miss that is still worth diagnosing: a
// request scoped to a branch key found no summary in its configured scope
// while the session holds a full-session summary outside that scope. This
// does not mean history was dropped. Because no in-scope summary was
// selected, this request's summary cutoff stays zero, so the raw scoped
// history is kept at this stage. That does not depend on whether the
// out-of-scope full-session summary has a boundary. Later token tailoring
// may still trim the request.
func (s Selection) ScopeMismatch() bool {
	return !s.Selected && s.ScopedRequest && s.FullSessionPresent
}

// Record stores the selection made for the current model request, replacing
// any previous record. A nil invocation is ignored.
func Record(inv *agent.Invocation, selection Selection) {
	if inv == nil {
		return
	}
	inv.SetState(stateKey, &selection)
}

// Clear removes the selection recorded for the current model request.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(stateKey)
}

// FromInvocation returns the selection recorded for the current model request.
func FromInvocation(inv *agent.Invocation) (Selection, bool) {
	stored, ok := agent.GetStateValue[*Selection](inv, stateKey)
	if !ok || stored == nil {
		return Selection{}, false
	}
	return *stored, true
}

// BlockPresent reports whether the recorded summary block text still appears
// as a substring of any message Content in the assembled request. A match
// does not prove the original injection slot is intact and does not describe
// a provider payload. The scan is O(total message bytes) and does not copy
// the request.
func (s Selection) BlockPresent(messages []model.Message) bool {
	if !s.Selected || s.Block == "" {
		return false
	}
	block := s.Block
	for i := range messages {
		content := messages[i].Content
		if len(content) < len(block) {
			continue
		}
		if strings.Contains(content, block) {
			return true
		}
	}
	return false
}
