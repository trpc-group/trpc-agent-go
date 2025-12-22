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
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/util"
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
	updatedAt := selectUpdatedAt(tmp, prevAt, latestTs, len(delta) > 0)

	// Acquire write lock to protect Summaries access.
	base.SummariesMu.Lock()
	defer base.SummariesMu.Unlock()

	if base.Summaries == nil {
		base.Summaries = make(map[string]*session.Summary)
	}
	base.Summaries[filterKey] = &session.Summary{Summary: text, UpdatedAt: updatedAt}
	return true, nil
}

func selectUpdatedAt(tmp *session.Session, prevAt, latestTs time.Time, hasDelta bool) time.Time {
	updatedAt := prevAt.UTC()
	if !hasDelta || latestTs.IsZero() {
		return updatedAt
	}

	if ts := readLastIncludedTimestamp(tmp); !ts.IsZero() {
		return ts.UTC()
	}
	return latestTs.UTC()
}

const lastIncludedTsKey = "summary:last_included_ts"

func readLastIncludedTimestamp(tmp *session.Session) time.Time {
	if tmp == nil || tmp.State == nil {
		return time.Time{}
	}
	raw, ok := tmp.State[lastIncludedTsKey]
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// PickSummaryText picks a non-empty summary string with preference for the
// specified filterKey. Falls back to all-contents key and then any available summary.
// When filterKey is empty (SummaryFilterKeyAllContents), prefers the full-session summary.
func PickSummaryText(summaries map[string]*session.Summary, filterKey string) (string, bool) {
	if summaries == nil {
		return "", false
	}
	// First, try to get the requested filter key summary.
	if sum, ok := summaries[filterKey]; ok && sum != nil && sum.Summary != "" {
		return sum.Summary, true
	}
	// Fallback: if requesting a specific filter key but not found, try full-session summary.
	if filterKey != session.SummaryFilterKeyAllContents {
		if sum, ok := summaries[session.SummaryFilterKeyAllContents]; ok && sum != nil && sum.Summary != "" {
			return sum.Summary, true
		}
	}
	// Last resort: return any available summary.
	for _, s := range summaries {
		if s != nil && s.Summary != "" {
			return s.Summary, true
		}
	}
	return "", false
}

// GetSummaryTextFromSession attempts to retrieve summary text from the session's
// in-memory summaries using the specified filter key. It parses the provided options
// and applies the summary selection logic.
func GetSummaryTextFromSession(sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	if sess == nil {
		return "", false
	}

	// Parse options.
	options := &session.SummaryOptions{
		FilterKey: session.SummaryFilterKeyAllContents, // Default to full session.
	}
	for _, opt := range opts {
		opt(options)
	}

	// Prefer local in-memory session summaries when available.
	if len(sess.Summaries) > 0 {
		return PickSummaryText(sess.Summaries, options.FilterKey)
	}

	return "", false
}

// GetFilterKeyFromOptions extracts the filter key from the provided summary options.
// Returns SummaryFilterKeyAllContents if no options are provided.
func GetFilterKeyFromOptions(opts ...session.SummaryOption) string {
	options := &session.SummaryOptions{
		FilterKey: session.SummaryFilterKeyAllContents, // Default to full session.
	}
	for _, opt := range opts {
		opt(options)
	}
	return options.FilterKey
}

// CreateSessionSummaryWithCascade creates a session summary for the specified filterKey
// and cascades to create a full-session summary if the filterKey is not already the full session.
// The createSummaryFunc should create a summary for the given filterKey and return an error if failed.
func CreateSessionSummaryWithCascade(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
	createSummaryFunc func(context.Context, *session.Session, string, bool) error,
) error {
	if filterKey == session.SummaryFilterKeyAllContents {
		return createSummaryFunc(ctx, sess, filterKey, force)
	}

	var summaryWg sync.WaitGroup
	result := make([]error, 2)
	summaryWg.Add(2)
	for i, fk := range []string{filterKey, session.SummaryFilterKeyAllContents} {
		go func(i int, fk string) {
			defer summaryWg.Done()
			err := createSummaryFunc(ctx, sess, fk, force)
			if err != nil {
				result[i] = fmt.Errorf("create session summary for filterKey %q failed: %w", fk, err)
			}
		}(i, fk)
	}
	summaryWg.Wait()

	return util.If(result[0] != nil, result[0], result[1])
}
