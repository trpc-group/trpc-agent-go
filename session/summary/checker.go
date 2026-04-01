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

	"trpc.group/trpc-go/trpc-agent-go/agent"
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

// ContextChecker evaluates whether a summary should be triggered using the
// current request context.
type ContextChecker func(context.Context, *session.Session) bool

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

// filterPrimaryEvents prevents sub-agent events from inflating
// parent-level threshold checks in the full-session summary scenario.
//
// The function distinguishes two cases by inspecting whether the events
// contain multiple distinct non-empty FilterKey values:
//
//  1. Single non-empty FilterKey (branch summary) — all events with a
//     non-empty FilterKey share the same value because computeDeltaSince
//     already filtered by that branch. No further filtering is needed;
//     return the events as-is.
//
//  2. Mixed non-empty FilterKeys (full-session summary) — events come
//     from both the primary agent and one or more sub-agents. Only
//     events whose FilterKey matches the session's AppName (the primary
//     agent's key) are retained so that sub-agent tokens/counts do not
//     inflate the parent threshold.
//
// Events with an empty FilterKey (e.g. synthetic summary events created
// by prependPrevSummary) are ignored when determining whether the set is
// mixed, and are always kept in the output. This prevents a single
// prepended summary event from incorrectly triggering the mixed-key
// filtering path for what is actually a single-branch summary.
//
// When AppName is empty, no filtering is applied for backward
// compatibility with sessions that do not set an AppName.
func filterPrimaryEvents(
	events []event.Event, appName string,
) []event.Event {
	if appName == "" || len(events) == 0 {
		return events
	}
	// Detect whether the events contain multiple distinct non-empty
	// FilterKeys. Empty FilterKeys are ignored because they come
	// from synthetic events (e.g. prepended previous summary).
	var firstNonEmpty string
	mixed := false
	for i := range events {
		fk := events[i].FilterKey
		if fk == "" {
			continue
		}
		if firstNonEmpty == "" {
			firstNonEmpty = fk
			continue
		}
		if fk != firstNonEmpty {
			mixed = true
			break
		}
	}
	if !mixed {
		// All non-empty FilterKeys are identical (branch summary)
		// or there are no non-empty keys at all — no additional
		// filtering required.
		return events
	}
	// Mixed non-empty FilterKeys (full-session summary) — keep
	// events that belong to the primary agent plus any events with
	// an empty FilterKey (synthetic summary events).
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		if e.FilterKey == appName || e.FilterKey == "" {
			out = append(out, e)
		}
	}
	return out
}

// CheckEventThreshold creates a checker that triggers when the number of
// primary-agent events since the last summary exceeds the given threshold.
// Sub-agent events (FilterKey != AppName) are excluded from the count so
// that child agent activity does not inflate the parent threshold.
func CheckEventThreshold(eventCount int) Checker {
	return func(sess *session.Session) bool {
		delta := filterDeltaEvents(sess)
		if len(delta) == 0 {
			return false
		}
		primary := filterPrimaryEvents(delta, sess.AppName)
		return len(primary) > eventCount
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
func checkTokenThresholdFromText(
	ctx context.Context,
	tokenCount int,
	conversationText string,
) bool {
	if conversationText == "" {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// SimpleTokenCounter.CountTokens currently never returns an error.
	tokens, _ := getTokenCounter().CountTokens(
		ctx,
		model.Message{Content: conversationText},
	)
	return tokens > tokenCount
}

// CheckTokenThreshold creates a checker that triggers when the estimated
// token count of the primary-agent events since the last summary exceeds
// the given threshold. Sub-agent events (FilterKey != AppName) are excluded
// so that child agent tokens do not inflate the parent threshold check.
//
// Note:
// Token accounting via model usage is not stable once session summary
// injection is enabled. For consistent gating, we estimate tokens from
// the delta events.
//
// Because Checker does not accept a context, this legacy helper evaluates
// token counts with context.Background(). Use CheckTokenThresholdContext or
// WithTokenThreshold when token counting depends on request-scoped context.
func CheckTokenThreshold(tokenCount int) Checker {
	return func(sess *session.Session) bool {
		return checkTokenThreshold(context.Background(), tokenCount, sess)
	}
}

// CheckTokenThresholdContext creates a context-aware checker that triggers
// when the estimated token count of the primary-agent events since the last
// summary exceeds the given threshold.
func CheckTokenThresholdContext(tokenCount int) ContextChecker {
	return func(ctx context.Context, sess *session.Session) bool {
		return checkTokenThreshold(ctx, tokenCount, sess)
	}
}

func checkTokenThreshold(
	ctx context.Context,
	tokenCount int,
	sess *session.Session,
) bool {
	delta := filterDeltaEvents(sess)
	if len(delta) == 0 {
		return false
	}
	primary := filterPrimaryEvents(delta, sess.AppName)
	if len(primary) == 0 {
		return false
	}
	conversationText := extractConversationText(
		primary, nil, nil,
	)
	return checkTokenThresholdFromText(
		ctx,
		tokenCount,
		conversationText,
	)
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

func wrapChecker(check Checker) ContextChecker {
	return func(_ context.Context, sess *session.Session) bool {
		return check(sess)
	}
}

func allContextChecks(checks []ContextChecker) ContextChecker {
	return func(ctx context.Context, sess *session.Session) bool {
		for _, check := range checks {
			if !check(ctx, sess) {
				return false
			}
		}
		return true
	}
}

func anyContextChecks(checks []ContextChecker) ContextChecker {
	return func(ctx context.Context, sess *session.Session) bool {
		for _, check := range checks {
			if check(ctx, sess) {
				return true
			}
		}
		return false
	}
}

// Default context-threshold constants.
const (
	// defaultContextThresholdRatio is the default fraction of the model's
	// context window that triggers summarization.
	defaultContextThresholdRatio = 0.5

	// defaultContextThresholdMinTokens is the absolute minimum token count
	// before summarization can trigger, regardless of ratio. This prevents
	// premature summarization for very small context windows.
	defaultContextThresholdMinTokens = 2000

	// defaultContextThresholdFallbackWindow is the context window used when
	// the model cannot be identified from the invocation context or the
	// model registry.
	defaultContextThresholdFallbackWindow = 8192
)

// ContextThresholdOption configures the context-threshold checker.
type ContextThresholdOption func(*contextThresholdOptions)

// contextThresholdOptions holds configuration for CheckContextThreshold.
type contextThresholdOptions struct {
	// thresholdRatio is the fraction of context window that triggers
	// summarization. Default: 0.5 (50%).
	thresholdRatio float64

	// fallbackContextWindow is used when the model's context window
	// cannot be determined from the invocation context or registry.
	// Default: 8192.
	fallbackContextWindow int

	// minTokenThreshold is the absolute minimum token count before
	// summarization can trigger, regardless of ratio.
	// Default: 2000.
	minTokenThreshold int
}

// WithContextThresholdRatio sets the fraction of the model's context
// window at which summarization triggers. Default: 0.5 (50%).
// Values outside (0, 1] are ignored.
func WithContextThresholdRatio(ratio float64) ContextThresholdOption {
	return func(o *contextThresholdOptions) {
		if ratio > 0 && ratio <= 1 {
			o.thresholdRatio = ratio
		}
	}
}

// WithContextThresholdFallbackWindow sets the context window used when
// the model cannot be identified at runtime. Default: 8192.
func WithContextThresholdFallbackWindow(tokens int) ContextThresholdOption {
	return func(o *contextThresholdOptions) {
		if tokens > 0 {
			o.fallbackContextWindow = tokens
		}
	}
}

// WithContextThresholdMinTokens sets the absolute minimum token count
// before summarization can trigger. Default: 2000.
func WithContextThresholdMinTokens(tokens int) ContextThresholdOption {
	return func(o *contextThresholdOptions) {
		if tokens >= 0 {
			o.minTokenThreshold = tokens
		}
	}
}

// CheckContextThreshold creates a context-aware checker that dynamically
// resolves the model's context window at evaluation time and triggers
// summarization when the estimated token count of delta events exceeds
// a percentage of that context window.
//
// Unlike CheckTokenThreshold which uses a fixed token count, this
// checker adapts automatically when the user switches models — the
// threshold is recalculated on every evaluation based on the model
// currently attached to the invocation in ctx.
//
// This provides a zero-configuration experience similar to Codex CLI
// and Claude Code, where the framework automatically decides when to
// compress conversation history based on the model's capacity.
//
// When the model cannot be determined from ctx, falls back to
// the summarizer model's context window (when used via WithContextThreshold),
// then to the configured fallbackContextWindow (default 8192).
func CheckContextThreshold(opts ...ContextThresholdOption) ContextChecker {
	o := contextThresholdOptions{
		thresholdRatio:        defaultContextThresholdRatio,
		fallbackContextWindow: defaultContextThresholdFallbackWindow,
		minTokenThreshold:     defaultContextThresholdMinTokens,
	}
	for _, opt := range opts {
		opt(&o)
	}

	return func(ctx context.Context, sess *session.Session) bool {
		contextWindow := resolveContextWindowFromCtx(
			ctx, o.fallbackContextWindow,
		)
		threshold := int(float64(contextWindow) * o.thresholdRatio)
		if threshold < o.minTokenThreshold {
			threshold = o.minTokenThreshold
		}
		return checkTokenThreshold(ctx, threshold, sess)
	}
}

// resolveContextWindowFromCtx attempts to determine the model's context
// window from the current request context. It tries, in order:
//  1. invocation.Model from ctx → model registry lookup by name
//  2. user-configured fallback
//  3. framework default (8192)
func resolveContextWindowFromCtx(ctx context.Context, fallback int) int {
	if ctx != nil {
		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
			if inv.Model != nil {
				name := inv.Model.Info().Name
				if w, ok := model.LookupModelContextWindow(name); ok {
					return w
				}
			}
		}
	}
	if fallback > 0 {
		return fallback
	}
	return defaultContextThresholdFallbackWindow
}
