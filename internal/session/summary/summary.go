//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package summary provides internal session summary functionality.
package summary

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// authorSystem is the system author.
const authorSystem = "system"

// computeDeltaSince returns events that occurred strictly after the given
// time and match the filterKey, along with the latest event timestamp among
// the returned events. When since is zero, all events are considered. When
// filterKey is empty, all events are considered (no filtering).
func computeDeltaSince(sess *session.Session, since time.Time, filterKey string) ([]event.Event, time.Time) {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	out := make([]event.Event, 0, len(sess.Events))
	var latest time.Time
	for _, e := range sess.Events {
		// Apply time filter
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		// Apply filterKey filter
		if filterKey != "" && !e.Filter(filterKey) {
			continue
		}
		out = append(out, e)
		if e.Timestamp.After(latest) {
			latest = e.Timestamp
		}
	}
	return out, latest
}

// prependPrevSummary returns a new slice that prepends the previous summary as
// a synthetic system event when prevSummary is non-empty, followed by delta.
func prependPrevSummary(prevSummary string, delta []event.Event, now time.Time) []event.Event {
	if prevSummary == "" {
		return delta
	}
	out := make([]event.Event, 0, len(delta)+1)
	out = append(out, event.Event{
		Author:    authorSystem,
		Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Content: prevSummary}}}},
		Timestamp: now,
	})
	out = append(out, delta...)
	return out
}

// buildFilterSession builds a temporary session containing filterKey events.
// When filterKey=="", it represents the full-session input.
func buildFilterSession(base *session.Session, filterKey string, evs []event.Event) *session.Session {
	return &session.Session{
		ID:        base.ID + ":" + filterKey,
		AppName:   base.AppName,
		UserID:    base.UserID,
		State:     nil,
		Events:    evs,
		UpdatedAt: time.Now(),
		CreatedAt: base.CreatedAt,
	}
}

// SummarizeSession performs per-filterKey delta summarization using the given
// summarizer and writes results to base.Summaries.
// - When filterKey is non-empty, summarizes only that filter's events.
// - When filterKey is empty, summarizes all events as a single full-session summary.
func SummarizeSession(
	ctx context.Context,
	m summary.SessionSummarizer,
	base *session.Session,
	filterKey string,
	force bool,
) (updated bool, err error) {
	if m == nil || base == nil {
		return false, nil
	}

	// Get previous summary info.
	var prevText string
	var prevAt time.Time
	if base.Summaries != nil {
		if s := base.Summaries[filterKey]; s != nil {
			prevText = s.Summary
			prevAt = s.UpdatedAt
		}
	}

	// Compute delta events with both time and filterKey filtering in one pass.
	delta, latestTs := computeDeltaSince(base, prevAt, filterKey)
	if !force && len(delta) == 0 {
		return false, nil
	}

	// Build input with previous summary prepended.
	input := prependPrevSummary(prevText, delta, time.Now())
	tmp := buildFilterSession(base, filterKey, input)
	if !force && !m.ShouldSummarize(tmp) {
		return false, nil
	}

	// Generate summary.
	text, err := m.Summarize(ctx, tmp)
	if err != nil || text == "" {
		return false, nil
	}

	// Update summaries. UpdatedAt reflects the latest event included in this
	// summarization to avoid skipping events during future delta computations.
	// When no new events were summarized (e.g., force==true and delta empty),
	// keep the previous timestamp.
	updatedAt := prevAt.UTC()
	if len(delta) > 0 && !latestTs.IsZero() {
		updatedAt = latestTs.UTC()
	}

	// Acquire write lock to protect Summaries access.
	base.SummariesMu.Lock()
	defer base.SummariesMu.Unlock()

	if base.Summaries == nil {
		base.Summaries = make(map[string]*session.Summary)
	}
	base.Summaries[filterKey] = &session.Summary{Summary: text, UpdatedAt: updatedAt}
	return true, nil
}
