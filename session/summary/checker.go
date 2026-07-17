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
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelcontext"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummaryscope "trpc.group/trpc-go/trpc-agent-go/session/internal/summaryscope"
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

type checkEvaluator func(context.Context, *session.Session) Check

var (
	defaultTokenCounterMu sync.RWMutex
	defaultTokenCounter   model.TokenCounter = model.NewSimpleTokenCounter()
)

const tokenThresholdConversationTextStateKey = session.StateTempPrefix +
	"summary:token_threshold_conversation_text"
const tokenThresholdReasoningContentStateKey = session.StateTempPrefix +
	"summary:token_threshold_reasoning_content"

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

// filterDeltaEvents returns visible events after the last summarized boundary
// stored in session state. Masked (Pensieve-pruned) events are excluded via
// GetVisibleEvents. Exact boundaries use the last event ID when available;
// timestamp-only boundaries keep same-timestamp events to avoid dropping
// uncovered history.
func filterDeltaEvents(sess *session.Session) []event.Event {
	if sess == nil {
		return nil
	}
	events := sess.GetVisibleEvents()
	if len(events) == 0 {
		return nil
	}

	if rawID, ok := sess.GetState(lastIncludedEventIDKey); ok && len(rawID) > 0 {
		for i, e := range events {
			if e.ID == string(rawID) {
				return events[i+1:]
			}
		}
	}

	raw, ok := sess.GetState(lastIncludedTsKey)
	if !ok || len(raw) == 0 {
		return events
	}

	lastTs, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		log.Warnf(
			"invalid %s in session state (session_id=%s): %v",
			lastIncludedTsKey,
			sess.ID,
			err,
		)
		return events
	}

	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		if !e.Timestamp.Before(lastTs) {
			out = append(out, e)
		}
	}
	return out
}

func effectiveFilterKey(e event.Event) string {
	if e.FilterKey != "" {
		return e.FilterKey
	}
	if e.Version != event.CurrentVersion {
		return e.Branch
	}
	return ""
}

func filterSummaryInputEventsForSession(
	events []event.Event,
	sess *session.Session,
) []event.Event {
	if sess == nil {
		return events
	}
	if scopeKey := isummaryscope.GetScopeFilterKey(sess); scopeKey != "" {
		return filterEventsInScope(events, scopeKey)
	}
	return events
}

func filterThresholdEventsForSession(
	events []event.Event,
	sess *session.Session,
) []event.Event {
	if sess == nil {
		return events
	}
	if scopeKey := isummaryscope.GetScopeFilterKey(sess); scopeKey != "" {
		return filterEventsInScope(events, scopeKey)
	}
	return filterEventsWithExactKey(events, sess.AppName)
}

// filterEventsInScope keeps only events in the requested branch scope plus
// synthetic events with an empty filter key.
func filterEventsInScope(events []event.Event, scopeKey string) []event.Event {
	if scopeKey == "" || len(events) == 0 {
		return events
	}
	out := make([]event.Event, 0, len(events))
	prefix := scopeKey + event.FilterKeyDelimiter
	for _, e := range events {
		fk := effectiveFilterKey(e)
		if fk == "" || fk == scopeKey || strings.HasPrefix(fk, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// filterEventsWithExactKey keeps only events whose effective filter key
// matches filterKey exactly, plus synthetic events with an empty filter key.
// This isolates full-session threshold checks to primary-agent activity.
func filterEventsWithExactKey(events []event.Event, filterKey string) []event.Event {
	if filterKey == "" || len(events) == 0 {
		return events
	}

	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		fk := effectiveFilterKey(e)
		if fk == "" || fk == filterKey {
			out = append(out, e)
		}
	}
	return out
}

// CheckEventThreshold creates a checker that triggers when the number of
// threshold events since the last summary exceeds the given threshold.
// Full-session checks count only primary-agent activity, while branch-scoped
// checks count the scoped branch and its descendants.
func CheckEventThreshold(eventCount int) Checker {
	evaluate := evaluateEventThreshold(eventCount)
	return func(sess *session.Session) bool {
		return evaluate(context.Background(), sess).Passed
	}
}

func evaluateEventThreshold(eventCount int) checkEvaluator {
	return func(_ context.Context, sess *session.Session) Check {
		check := Check{
			Name:      checkNameEventThreshold,
			Metric:    metricEvents,
			Threshold: eventCount,
			Unit:      unitEvents,
		}
		delta := filterDeltaEvents(sess)
		if len(delta) == 0 {
			return check
		}
		thresholdEvents := filterThresholdEventsForSession(delta, sess)
		check.Value = len(thresholdEvents)
		check.Passed = check.Value > eventCount
		return check
	}
}

// CheckTimeThreshold creates a checker that triggers when the time elapsed
// since the last relevant event is greater than the given interval. Scoped
// branch checks use the last event in that branch subtree; full-session checks
// use the last event in the session.
func CheckTimeThreshold(interval time.Duration) Checker {
	evaluate := evaluateTimeThreshold(interval)
	return func(sess *session.Session) bool {
		return evaluate(context.Background(), sess).Passed
	}
}

func evaluateTimeThreshold(interval time.Duration) checkEvaluator {
	return func(_ context.Context, sess *session.Session) Check {
		check := Check{
			Name:      checkNameTimeThreshold,
			Metric:    metricDuration,
			Threshold: int(interval / time.Millisecond),
			Unit:      unitMilliseconds,
		}
		if sess == nil {
			return check
		}
		visible := sess.GetVisibleEvents()
		if len(visible) == 0 {
			return check
		}
		relevant := filterSummaryInputEventsForSession(visible, sess)
		if len(relevant) == 0 {
			return check
		}
		lastEvent := relevant[len(relevant)-1]
		elapsed := time.Since(lastEvent.Timestamp)
		check.Value = int(elapsed / time.Millisecond)
		check.Passed = elapsed > interval
		return check
	}
}

// checkTokenThresholdFromMessage checks if the token count of the given message exceeds the threshold.
func checkTokenThresholdFromMessage(
	ctx context.Context,
	tokenCount int,
	message model.Message,
) bool {
	tokens, ok := countTokenThresholdMessage(ctx, message)
	return ok && tokens > tokenCount
}

func countTokenThresholdMessage(
	ctx context.Context,
	message model.Message,
) (int, bool) {
	if strings.TrimSpace(message.Content) == "" &&
		strings.TrimSpace(message.ReasoningContent) == "" {
		return 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tokens, err := getTokenCounter().CountTokens(
		ctx,
		message,
	)
	if err != nil {
		log.DebugfContext(ctx, "summary token count failed: %v", err)
		return 0, false
	}
	return tokens, true
}

// CheckTokenThreshold creates a checker that triggers when the estimated
// token count of the threshold events since the last summary exceeds the given
// threshold. Full-session checks count only primary-agent activity, while
// branch-scoped checks count the scoped branch and its descendants. When a
// summarizer injects effective summary text into the session state, that text
// takes precedence over the default event extraction logic.
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
	evaluate := evaluateTokenThreshold(tokenCount)
	return func(ctx context.Context, sess *session.Session) bool {
		return evaluate(ctx, sess).Passed
	}
}

func checkTokenThreshold(
	ctx context.Context,
	tokenCount int,
	sess *session.Session,
) bool {
	return evaluateTokenThreshold(tokenCount)(ctx, sess).Passed
}

func evaluateTokenThreshold(tokenCount int) checkEvaluator {
	return func(ctx context.Context, sess *session.Session) Check {
		check := Check{
			Name:      checkNameTokenThreshold,
			Metric:    metricTokens,
			Threshold: tokenCount,
			Unit:      unitTokens,
		}

		var message model.Message
		if message, ok := getInjectedTokenThresholdMessage(sess); ok {
			tokens, counted := countTokenThresholdMessage(ctx, message)
			check.Value = tokens
			check.Passed = counted && tokens > tokenCount
			return check
		}
		delta := filterDeltaEvents(sess)
		if len(delta) == 0 {
			return check
		}
		thresholdEvents := filterThresholdEventsForSession(delta, sess)
		if len(thresholdEvents) == 0 {
			return check
		}
		message = extractTokenThresholdMessage(thresholdEvents)
		tokens, counted := countTokenThresholdMessage(ctx, message)
		check.Value = tokens
		check.Passed = counted && tokens > tokenCount
		return check
	}
}

func getInjectedTokenThresholdMessage(sess *session.Session) (model.Message, bool) {
	if sess == nil {
		return model.Message{}, false
	}
	content, hasContent := sess.GetState(tokenThresholdConversationTextStateKey)
	reasoning, hasReasoning := sess.GetState(tokenThresholdReasoningContentStateKey)
	if !hasContent && !hasReasoning {
		return model.Message{}, false
	}
	return model.Message{
		Content:          string(content),
		ReasoningContent: string(reasoning),
	}, true
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

func wrapChecker(check Checker) checkEvaluator {
	return func(_ context.Context, sess *session.Session) Check {
		return Check{
			Name:   checkNameCustom,
			Metric: metricCustom,
			Passed: check(sess),
		}
	}
}

func wrapContextChecker(check ContextChecker) checkEvaluator {
	return func(ctx context.Context, sess *session.Session) Check {
		return Check{
			Name:   checkNameCustom,
			Metric: metricCustom,
			Passed: check(ctx, sess),
		}
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

	// fallbackContextWindowSet reports whether fallbackContextWindow was
	// configured explicitly by the user.
	fallbackContextWindowSet bool

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
			o.fallbackContextWindowSet = true
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
	evaluate := evaluateContextThreshold(opts...)
	return func(ctx context.Context, sess *session.Session) bool {
		return evaluate(ctx, sess).Passed
	}
}

func evaluateContextThreshold(opts ...ContextThresholdOption) checkEvaluator {
	o := contextThresholdOptions{
		thresholdRatio:        defaultContextThresholdRatio,
		fallbackContextWindow: defaultContextThresholdFallbackWindow,
		minTokenThreshold:     defaultContextThresholdMinTokens,
	}
	for _, opt := range opts {
		opt(&o)
	}

	return func(ctx context.Context, sess *session.Session) Check {
		contextWindow := resolveContextWindowFromCtx(
			ctx, o.fallbackContextWindow,
		)
		threshold := int(float64(contextWindow) * o.thresholdRatio)
		if threshold < o.minTokenThreshold {
			threshold = o.minTokenThreshold
		}
		check := evaluateTokenThreshold(threshold)(ctx, sess)
		check.Name = checkNameContextThreshold
		check.ContextWindow = contextWindow
		check.ThresholdRatio = o.thresholdRatio
		return check
	}
}

// resolveContextWindowFromCtx attempts to determine the model's context
// window from the current request context. It tries, in order:
//  1. per-run model context window override
//  2. invocation.Model from ctx -> model instance configuration, then registry
//  3. user-configured fallback
//  4. framework default (8192)
func resolveContextWindowFromCtx(ctx context.Context, fallback int) int {
	if ctx != nil {
		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
			if w, ok := agent.ModelContextWindowFromRunOptions(
				&inv.RunOptions,
			); ok {
				return w
			}
			if inv.Model != nil {
				if w, ok := modelcontext.ResolveContextWindow(inv.Model); ok {
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
