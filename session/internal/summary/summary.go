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
//   - When filterKey is non-empty, summarizes only that filter's events.
//   - When filterKey is empty, summarizes all events as a single full-session summary.
//   - When summary exists with zero UpdatedAt (copied via copySummaryToKey), returns
//     updated=true to trigger persistence without LLM call, and sets proper UpdatedAt.
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
	var needsPersistOnly bool
	base.SummariesMu.RLock()
	if base.Summaries != nil {
		if s := base.Summaries[filterKey]; s != nil {
			prevText = s.Summary
			prevAt = s.UpdatedAt
			// Zero UpdatedAt indicates summary was copied and needs persistence.
			needsPersistOnly = prevText != "" && prevAt.IsZero()
		}
	}
	base.SummariesMu.RUnlock()

	// Handle copied summary that needs persistence only (no LLM call).
	if needsPersistOnly {
		// Compute the latest event timestamp for proper UpdatedAt.
		_, latestTs := computeDeltaSince(base, time.Time{}, filterKey)
		if latestTs.IsZero() {
			latestTs = time.Now()
		}
		base.SummariesMu.Lock()
		if base.Summaries != nil && base.Summaries[filterKey] != nil {
			base.Summaries[filterKey].UpdatedAt = latestTs.UTC()
		}
		base.SummariesMu.Unlock()
		return true, nil
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
	if err != nil {
		return false, fmt.Errorf("summarize session %s failed: %w", base.ID, err)
	}
	if text == "" {
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

// lastIncludedTsKey is the key for the last included timestamp.
// This key is used to store the last included timestamp in the session state.
const lastIncludedTsKey = "summary:last_included_ts"

func readLastIncludedTimestamp(tmp *session.Session) time.Time {
	if tmp == nil {
		return time.Time{}
	}
	raw, ok := tmp.GetState(lastIncludedTsKey)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// meetsTimeCriteria checks if a summary meets the minimum time requirement.
// Returns true if sum is non-nil and either minTime is zero or sum.UpdatedAt >= minTime.
func meetsTimeCriteria(sum *session.Summary, minTime time.Time) bool {
	if sum == nil {
		return false
	}
	if minTime.IsZero() {
		return true
	}
	return !sum.UpdatedAt.Before(minTime)
}

// PickSummaryText picks a non-empty summary string with preference for the
// specified filterKey. Falls back to all-contents key and then any available summary.
// When filterKey is empty (SummaryFilterKeyAllContents), prefers the full-session summary.
// When minTime is non-zero, only returns summaries with UpdatedAt >= minTime.
func PickSummaryText(
	summaries map[string]*session.Summary,
	filterKey string,
	minTime time.Time,
) (string, bool) {
	if summaries == nil {
		return "", false
	}
	// First, try to get the requested filter key summary.
	if sum, ok := summaries[filterKey]; ok && meetsTimeCriteria(sum, minTime) && sum.Summary != "" {
		return sum.Summary, true
	}
	// Fallback: if requesting a specific filter key but not found, try full-session summary.
	if filterKey != session.SummaryFilterKeyAllContents {
		if sum, ok := summaries[session.SummaryFilterKeyAllContents]; ok && meetsTimeCriteria(sum, minTime) && sum.Summary != "" {
			return sum.Summary, true
		}
	}
	return "", false
}

// GetSummaryTextFromSession attempts to retrieve summary text from the session's
// in-memory summaries using the specified filter key. It parses the provided options
// and applies the summary selection logic. Filters out summaries with UpdatedAt before sess.CreatedAt.
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
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	if len(sess.Summaries) > 0 {
		return PickSummaryText(sess.Summaries, options.FilterKey, sess.CreatedAt)
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

// isSingleFilterKey checks if all events in the session match the target filterKey.
// Returns true if all events match, meaning the filterKey summary would be identical
// to the full-session summary, allowing us to skip duplicate LLM calls.
func isSingleFilterKey(sess *session.Session, targetKey string) bool {
	if sess == nil || targetKey == "" {
		return false
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	for _, e := range sess.Events {
		if !e.Filter(targetKey) {
			return false
		}
	}
	return true
}

// copySummaryToKey copies a summary from srcKey to dstKey within the session.
// This avoids duplicate LLM calls when the summaries would be identical.
// Sets UpdatedAt to zero to mark the summary as needing persistence.
func copySummaryToKey(sess *session.Session, srcKey, dstKey string) {
	if sess == nil {
		return
	}
	sess.SummariesMu.Lock()
	defer sess.SummariesMu.Unlock()
	if sess.Summaries == nil {
		return
	}
	src, ok := sess.Summaries[srcKey]
	if !ok || src == nil {
		return
	}
	// Copy Topics slice to avoid sharing underlying array.
	var topics []string
	if len(src.Topics) > 0 {
		topics = make([]string, len(src.Topics))
		copy(topics, src.Topics)
	}
	// Use zero UpdatedAt to mark as needing persistence.
	// SummarizeSession will detect this and return updated=true.
	sess.Summaries[dstKey] = &session.Summary{
		Summary:   src.Summary,
		Topics:    topics,
		UpdatedAt: time.Time{},
	}
}

// CreateSessionSummaryWithCascade creates a session summary for the specified filterKey
// and cascades to create a full-session summary if the filterKey is not already the full session.
// The createSummaryFunc should create a summary for the given filterKey and return an error if failed.
// When all events match the filterKey (single filterKey scenario), it generates only one summary
// and copies it to both keys to avoid duplicate LLM calls. The copied summary is then persisted
// via createSummaryFunc which detects existing in-memory summary and triggers persistence.
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

	// Optimization: when all events match the filterKey, the filterKey summary
	// would be identical to the full-session summary. Generate only once via LLM,
	// then copy to memory and persist both keys.
	if isSingleFilterKey(sess, filterKey) {
		if err := createSummaryFunc(ctx, sess, filterKey, force); err != nil {
			return fmt.Errorf("create session summary for filterKey %q failed: %w",
				filterKey, err)
		}
		// Copy to in-memory session for immediate access.
		copySummaryToKey(sess, filterKey, session.SummaryFilterKeyAllContents)
		// Persist the full-session key to storage. SummarizeSession detects
		// existing in-memory summary with empty delta and returns updated=true.
		if err := createSummaryFunc(ctx, sess, session.SummaryFilterKeyAllContents, false); err != nil {
			return fmt.Errorf("persist full-session summary failed: %w", err)
		}
		return nil
	}

	// Multiple filterKeys detected: generate both summaries in parallel.
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
