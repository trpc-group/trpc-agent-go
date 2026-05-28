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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummaryscope "trpc.group/trpc-go/trpc-agent-go/session/internal/summaryscope"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// authorSystem is the system author.
const authorSystem = "system"

// computeDeltaSince returns events that occurred strictly after the given time
// and match the filterKey, along with the latest event timestamp among the
// returned events. When since is zero, all events are considered. When filterKey
// is empty, all events are considered.
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

// computeDeltaAfterBoundary returns events after the structural summary
// boundary. Exact boundaries use event order, while legacy timestamp-only
// boundaries keep same-timestamp events to avoid dropping uncovered history.
func computeDeltaAfterBoundary(
	sess *session.Session,
	boundary *session.SummaryBoundary,
	filterKey string,
) ([]event.Event, *session.SummaryBoundary) {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	startIndex := deltaStartIndex(sess.Events, boundary)
	out := make([]event.Event, 0, len(sess.Events))
	var latest *session.SummaryBoundary
	for i, e := range sess.Events {
		if !eventAfterBoundaryIndex(i, e, startIndex, boundary) {
			continue
		}
		if filterKey != "" && !e.Filter(filterKey) {
			continue
		}
		out = append(out, e)
		latest = laterBoundary(filterKey, latest, e)
	}
	return out, latest
}

func deltaStartIndex(
	events []event.Event,
	boundary *session.SummaryBoundary,
) int {
	if boundary == nil || boundary.LastEventID == "" {
		return -1
	}
	for i, e := range events {
		if e.ID == boundary.LastEventID {
			return i + 1
		}
	}
	return -1
}

func eventAfterBoundaryIndex(
	index int,
	evt event.Event,
	startIndex int,
	boundary *session.SummaryBoundary,
) bool {
	if startIndex >= 0 {
		return index >= startIndex
	}
	if boundary == nil {
		return true
	}
	cutoff := boundary.CutoffTime()
	if cutoff.IsZero() {
		return true
	}
	return !evt.Timestamp.Before(cutoff)
}

func laterBoundary(
	filterKey string,
	current *session.SummaryBoundary,
	evt event.Event,
) *session.SummaryBoundary {
	if current != nil && evt.Timestamp.Before(current.CutoffAt) {
		return current
	}
	return session.NewSummaryBoundaryWithEventID(
		filterKey,
		evt.Timestamp,
		evt.ID,
	)
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
	tmp := &session.Session{
		ID:        base.ID + ":" + filterKey,
		AppName:   base.AppName,
		UserID:    base.UserID,
		State:     nil,
		Events:    evs,
		UpdatedAt: time.Now(),
		CreatedAt: base.CreatedAt,
	}
	isummaryscope.SetScopeFilterKey(tmp, filterKey)
	return tmp
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
	if base == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unlock, err := lockSessionSummary(ctx, base, filterKey)
	if err != nil {
		return false, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}

	prev := readPreviousSummary(base, filterKey)
	if prev.needsPersistOnly {
		persistCopiedSummary(base, filterKey)
		return true, nil
	}
	if m == nil {
		return false, nil
	}

	input, ok := buildSummaryInput(ctx, m, base, filterKey, force, prev)
	if !ok {
		return false, nil
	}
	text, err := m.Summarize(ctx, input.session)
	if err != nil {
		return false, fmt.Errorf("summarize session %s failed: %w", base.ID, err)
	}
	if text == "" {
		return false, nil
	}

	boundary := selectSummaryBoundary(
		input.session,
		filterKey,
		prev.boundary,
		input.latestBoundary,
		input.hasDelta,
	)
	updatedAt := boundary.CutoffTime()
	writeSummary(
		base,
		filterKey,
		text,
		updatedAt,
		boundary,
	)
	return true, nil
}

type previousSummary struct {
	text             string
	boundary         *session.SummaryBoundary
	needsPersistOnly bool
}

type summaryInput struct {
	session        *session.Session
	latestBoundary *session.SummaryBoundary
	hasDelta       bool
}

// readPreviousSummary returns the current summary state for filterKey.
func readPreviousSummary(base *session.Session, filterKey string) previousSummary {
	var prev previousSummary
	base.SummariesMu.RLock()
	defer base.SummariesMu.RUnlock()
	if base.Summaries == nil {
		return prev
	}
	s := base.Summaries[filterKey]
	if s == nil {
		return prev
	}
	prev.text = s.Summary
	prev.boundary = s.CutoffBoundary()
	// Zero UpdatedAt indicates summary was copied and needs persistence.
	prev.needsPersistOnly = prev.text != "" && s.UpdatedAt.IsZero()
	return prev
}

// persistCopiedSummary marks a copied in-memory summary as persisted-ready.
func persistCopiedSummary(base *session.Session, filterKey string) {
	summary := readSummaryClone(base, filterKey)
	if summary == nil {
		return
	}
	boundary := summary.CutoffBoundary()
	updatedAt := time.Time{}
	if boundary != nil {
		updatedAt = boundary.CutoffTime()
	}
	if updatedAt.IsZero() {
		_, latestTs := computeDeltaSince(base, time.Time{}, filterKey)
		updatedAt = latestTs
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	if boundary == nil {
		boundary = session.NewSummaryBoundary(filterKey, updatedAt)
	} else {
		boundary.FilterKey = filterKey
		if boundary.CutoffAt.IsZero() {
			boundary.CutoffAt = updatedAt.UTC()
		}
	}
	base.SummariesMu.Lock()
	defer base.SummariesMu.Unlock()
	if base.Summaries != nil && base.Summaries[filterKey] != nil {
		base.Summaries[filterKey].UpdatedAt = updatedAt.UTC()
		base.Summaries[filterKey].Boundary = boundary
	}
}

func readSummaryClone(base *session.Session, filterKey string) *session.Summary {
	if base == nil {
		return nil
	}
	base.SummariesMu.RLock()
	defer base.SummariesMu.RUnlock()
	if base.Summaries == nil {
		return nil
	}
	return base.Summaries[filterKey].Clone()
}

// buildSummaryInput prepares the temporary session used for summary generation.
func buildSummaryInput(
	ctx context.Context,
	m summary.SessionSummarizer,
	base *session.Session,
	filterKey string,
	force bool,
	prev previousSummary,
) (summaryInput, bool) {
	delta, latestBoundary := computeDeltaAfterBoundary(base, prev.boundary, filterKey)
	if !force && len(delta) == 0 {
		return summaryInput{}, false
	}
	input := prependPrevSummary(prev.text, delta, time.Now())
	tmp := buildFilterSession(base, filterKey, input)
	if !shouldGenerateSummary(ctx, m, base, tmp, input, filterKey, force) {
		return summaryInput{}, false
	}
	return summaryInput{
		session:        tmp,
		latestBoundary: latestBoundary,
		hasDelta:       len(delta) > 0,
	}, true
}

// shouldGenerateSummary runs configured summary checks for the prepared input.
func shouldGenerateSummary(
	ctx context.Context,
	m summary.SessionSummarizer,
	base *session.Session,
	tmp *session.Session,
	input []event.Event,
	filterKey string,
	force bool,
) bool {
	if force {
		return true
	}
	checkTmp := tmp
	if filterKey == session.SummaryFilterKeyAllContents {
		if triggerFilterKey := summaryTriggerFilterKeyFromContext(ctx); triggerFilterKey != "" {
			checkTmp = buildFilterSession(base, triggerFilterKey, input)
		}
	}
	return ShouldSummarize(ctx, m, checkTmp)
}

// writeSummary stores the generated summary under filterKey.
func writeSummary(
	base *session.Session,
	filterKey string,
	text string,
	updatedAt time.Time,
	boundary *session.SummaryBoundary,
) {
	base.SummariesMu.Lock()
	defer base.SummariesMu.Unlock()
	if base.Summaries == nil {
		base.Summaries = make(map[string]*session.Summary)
	}
	base.Summaries[filterKey] = &session.Summary{
		Summary:   text,
		UpdatedAt: updatedAt,
		Boundary:  boundary,
	}
}

func selectUpdatedAt(tmp *session.Session, prevAt, latestTs time.Time, hasDelta bool) time.Time {
	prev := session.NewSummaryBoundary("", prevAt)
	latest := session.NewSummaryBoundary("", latestTs)
	return selectSummaryBoundary(tmp, "", prev, latest, hasDelta).CutoffTime()
}

func selectSummaryBoundary(
	tmp *session.Session,
	filterKey string,
	prevBoundary *session.SummaryBoundary,
	latestBoundary *session.SummaryBoundary,
	hasDelta bool,
) *session.SummaryBoundary {
	boundary := prevBoundary.Clone()
	if boundary == nil {
		boundary = session.NewSummaryBoundary(filterKey, time.Time{})
	}
	boundary.FilterKey = filterKey
	if !hasDelta || latestBoundary == nil || latestBoundary.CutoffTime().IsZero() {
		return boundary
	}

	if recorded := readLastIncludedBoundary(tmp, filterKey); recorded != nil {
		return recorded
	}
	latest := latestBoundary.Clone()
	latest.FilterKey = filterKey
	return latest
}

type summaryTriggerFilterKeyContextKey struct{}

// summaryLockKey identifies the summary scope that must be serialized.
type summaryLockKey struct {
	appName   string
	userID    string
	sessionID string
	filterKey string
}

// summaryLock is a cancelable binary semaphore with reference counting.
type summaryLock struct {
	ch   chan struct{}
	refs int
}

// summaryLockGroup stores in-flight summary locks keyed by session scope.
type summaryLockGroup struct {
	mu    sync.Mutex
	locks map[summaryLockKey]*summaryLock
}

// sessionSummaryLocks prevents duplicate concurrent summaries in this process.
var sessionSummaryLocks summaryLockGroup

// lock acquires the keyed summary semaphore or returns when ctx is canceled.
func (g *summaryLockGroup) lock(ctx context.Context, key summaryLockKey) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.locks == nil {
		g.locks = make(map[summaryLockKey]*summaryLock)
	}
	l := g.locks[key]
	if l == nil {
		l = &summaryLock{ch: make(chan struct{}, 1)}
		l.ch <- struct{}{}
		g.locks[key] = l
	}
	l.refs++
	g.mu.Unlock()

	select {
	case <-l.ch:
	case <-ctx.Done():
		g.release(key, l)
		return nil, ctx.Err()
	}
	return func() {
		select {
		case l.ch <- struct{}{}:
		default:
		}
		g.release(key, l)
	}, nil
}

// release drops one reference and removes the lock after the last waiter exits.
func (g *summaryLockGroup) release(key summaryLockKey, l *summaryLock) {
	g.mu.Lock()
	defer g.mu.Unlock()
	l.refs--
	if l.refs == 0 && g.locks[key] == l {
		delete(g.locks, key)
	}
}

// lockSessionSummary serializes summary generation for a session/filter pair.
func lockSessionSummary(ctx context.Context, sess *session.Session, filterKey string) (func(), error) {
	if sess == nil {
		return func() {}, nil
	}
	return sessionSummaryLocks.lock(ctx, summaryLockKey{
		appName:   sess.AppName,
		userID:    sess.UserID,
		sessionID: sess.ID,
		filterKey: filterKey,
	})
}

func contextWithSummaryTriggerFilterKey(ctx context.Context, filterKey string) context.Context {
	if filterKey == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, summaryTriggerFilterKeyContextKey{}, filterKey)
}

func summaryTriggerFilterKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	filterKey, _ := ctx.Value(summaryTriggerFilterKeyContextKey{}).(string)
	return filterKey
}

func readLastIncludedTimestamp(tmp *session.Session) time.Time {
	boundary := readLastIncludedBoundary(tmp, "")
	if boundary == nil {
		return time.Time{}
	}
	return boundary.CutoffTime()
}

func readLastIncludedBoundary(
	tmp *session.Session,
	filterKey string,
) *session.SummaryBoundary {
	if tmp == nil {
		return nil
	}
	raw, ok := tmp.GetState(session.SummaryLastIncludedTimestampStateKey)
	if !ok || len(raw) == 0 {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return nil
	}
	eventID := ""
	if rawID, ok := tmp.GetState(session.SummaryLastIncludedEventIDStateKey); ok {
		eventID = string(rawID)
	}
	return session.NewSummaryBoundaryWithEventID(filterKey, parsed, eventID)
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
	copied := src.Clone()
	if boundary := copied.CutoffBoundary(); boundary != nil {
		boundary.FilterKey = dstKey
		copied.Boundary = boundary
	}
	// Use zero UpdatedAt to mark as needing persistence.
	// SummarizeSession will detect this and return updated=true.
	copied.UpdatedAt = time.Time{}
	if len(copied.Topics) == 0 {
		copied.Topics = nil
	}
	sess.Summaries[dstKey] = copied
}

// CreateSessionSummaryWithCascade creates one or more session summaries for the
// specified filterKey according to the dispatch policy.
//
// The createSummaryFunc should create a summary for the given filterKey and
// return an error if failed. When the policy selects both the branch key and
// the full-session key and all events match the branch, the helper generates
// only one summary and copies it to both keys to avoid duplicate LLM calls.
// The copied summary is then persisted via createSummaryFunc which detects the
// existing in-memory summary and triggers persistence.
func CreateSessionSummaryWithCascade(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
	policy SummaryDispatchPolicy,
	createSummaryFunc func(context.Context, *session.Session, string, bool) error,
) error {
	targets := policy.SummaryTargets(filterKey)
	if len(targets) == 0 {
		return nil
	}
	if len(targets) == 1 {
		target := targets[0]
		if target == session.SummaryFilterKeyAllContents &&
			filterKey != session.SummaryFilterKeyAllContents {
			ctx = contextWithSummaryTriggerFilterKey(ctx, filterKey)
		}
		return createSummaryFunc(ctx, sess, target, force)
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
	result := make([]error, len(targets))
	summaryWg.Add(len(targets))
	for i, fk := range targets {
		go func(i int, fk string) {
			defer summaryWg.Done()
			callCtx := ctx
			if fk == session.SummaryFilterKeyAllContents &&
				filterKey != session.SummaryFilterKeyAllContents {
				callCtx = contextWithSummaryTriggerFilterKey(callCtx, filterKey)
			}
			err := createSummaryFunc(callCtx, sess, fk, force)
			if err != nil {
				result[i] = fmt.Errorf("create session summary for filterKey %q failed: %w", fk, err)
			}
		}(i, fk)
	}
	summaryWg.Wait()

	for _, err := range result {
		if err != nil {
			return err
		}
	}
	return nil
}
