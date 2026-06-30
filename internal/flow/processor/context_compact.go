//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// DefaultContextCompactionKeepRecentRequests preserves the latest N
	// completed requests in full when request-side context compaction is enabled.
	DefaultContextCompactionKeepRecentRequests = 1
	// DefaultContextCompactionToolResultMaxTokens is the default token
	// threshold above which historical tool results are replaced with a
	// placeholder.
	DefaultContextCompactionToolResultMaxTokens = 1024

	// DefaultContextCompactionOversizedToolResultMaxTokens is the recommended
	// token threshold above which ANY tool result (including current request)
	// is truncated to head+tail when Pass 2 is opted into.
	//
	// NOTE: this constant is only the suggested value to pass to
	// WithContextCompactionOversizedToolResultMaxTokens; it is NOT applied
	// automatically. Pass 2 only runs when both EnableContextCompaction is
	// true and the threshold is greater than 0. The default for the option
	// itself is 0 (disabled) so that EnableContextCompaction=false truly
	// means "framework will not modify tool results".
	DefaultContextCompactionOversizedToolResultMaxTokens = 8192

	// historicalToolResultPlaceholder replaces historical tool result content
	// after context compaction. The message MUST make it clear that the call
	// succeeded and returned data that was already consumed, otherwise the
	// model may interpret the elided payload as a failed/missing call and
	// retry side-effecting tools at the top of the context window.
	historicalToolResultPlaceholder = "[elided] Previous tool call " +
		"succeeded and its result was already consumed by the assistant; " +
		"payload has been dropped to save context. Use the available " +
		"summary or recovery hints first. Re-run only read-only or " +
		"idempotent tools when exact data is essential; do not repeat " +
		"side-effecting operations just to recover this payload."
	sessionLoadToolName         = "session_load"
	policyToolResultPlaceholder = "Tool result omitted by context " +
		"compaction policy."
)

type toolResultRecoveryRef struct {
	EventID              string
	ToolCallID           string
	ToolName             string
	Reason               string
	SessionLoadAvailable bool
}

func toolResultRecoveryRefForMessage(
	evt event.Event,
	msg model.Message,
	reason string,
) toolResultRecoveryRef {
	return toolResultRecoveryRef{
		EventID:    strings.TrimSpace(evt.ID),
		ToolCallID: strings.TrimSpace(msg.ToolID),
		ToolName:   strings.TrimSpace(msg.ToolName),
		Reason:     reason,
	}
}

func (cfg ContextCompactionConfig) recoveryRefForMessage(
	evt event.Event,
	msg model.Message,
	reason string,
) toolResultRecoveryRef {
	ref := toolResultRecoveryRefForMessage(evt, msg, reason)
	ref.SessionLoadAvailable = cfg.SessionLoadRecoveryEnabled
	return ref
}

func recoverableToolResultPlaceholder(ref toolResultRecoveryRef) string {
	if ref.EventID == "" && ref.ToolCallID == "" {
		if ref.Reason == "current_invocation_summary" {
			return compactedToolResultPlaceholder
		}
		return historicalToolResultPlaceholder
	}
	var b strings.Builder
	switch ref.Reason {
	case "current_invocation_summary":
		b.WriteString(compactedToolResultPlaceholder)
	default:
		b.WriteString(historicalToolResultPlaceholder)
	}
	writeRecoveryRefLines(&b, ref)
	b.WriteString(toolResultRecoveryInstruction(ref))
	return b.String()
}

func recoverableTruncationMarker(
	ref toolResultRecoveryRef,
	omittedChars int,
) string {
	if ref.EventID == "" && ref.ToolCallID == "" {
		return fmt.Sprintf("\n\n[... %d characters truncated ...]\n\n", omittedChars)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[... %d characters truncated from tool result", omittedChars)
	if ref.EventID != "" {
		fmt.Fprintf(&b, "; event_id=%s", ref.EventID)
	}
	if ref.ToolCallID != "" {
		fmt.Fprintf(&b, "; tool_call_id=%s", ref.ToolCallID)
	}
	if ref.ToolName != "" {
		fmt.Fprintf(&b, "; tool_name=%s", ref.ToolName)
	}
	if ref.SessionLoadAvailable {
		b.WriteString("; use session_load ...")
	} else {
		b.WriteString("; re-run only safe read-only/idempotent tools")
	}
	b.WriteString("]\n\n")
	return b.String()
}

func compactRecoverableTruncationMarker(
	ref toolResultRecoveryRef,
	omittedChars int,
) string {
	if ref.EventID == "" && ref.ToolCallID == "" {
		return fmt.Sprintf("\n\n[... %d characters truncated ...]\n\n", omittedChars)
	}
	var b strings.Builder
	b.WriteString("\n\n[... ")
	wroteField := false
	if ref.EventID != "" {
		fmt.Fprintf(&b, "event_id=%s", ref.EventID)
		wroteField = true
	}
	if ref.ToolCallID != "" {
		if wroteField {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "tool_call_id=%s", ref.ToolCallID)
		wroteField = true
	}
	if wroteField {
		b.WriteString("; ")
	}
	if ref.SessionLoadAvailable {
		b.WriteString("session_load")
	} else {
		b.WriteString("compacted")
	}
	b.WriteString("]\n\n")
	return b.String()
}

func toolResultRecoveryInstruction(ref toolResultRecoveryRef) string {
	if ref.SessionLoadAvailable {
		return "\nUse session_load with event_id and content_offset/content_limit if the full result is needed."
	}
	return "\nThe full result is not available through the current tool surface. " +
		"Use the available summary if it is enough. If exact data is " +
		"essential, re-run only tools that are read-only, idempotent, or safe " +
		"to repeat; do not repeat side-effecting operations just to recover " +
		"this payload."
}

func writeRecoveryRefLines(b *strings.Builder, ref toolResultRecoveryRef) {
	if ref.EventID != "" {
		fmt.Fprintf(b, "\nevent_id: %s", ref.EventID)
	} else if ref.ToolCallID == "" {
		b.WriteString("\nrecoverable: false")
	}
	if ref.ToolCallID != "" {
		fmt.Fprintf(b, "\ntool_call_id: %s", ref.ToolCallID)
	}
	if ref.ToolName != "" {
		fmt.Fprintf(b, "\ntool_name: %s", ref.ToolName)
	}
	if ref.Reason != "" {
		fmt.Fprintf(b, "\nreason: %s", ref.Reason)
	}
}

func isRecoverablePlaceholderContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == historicalToolResultPlaceholder {
		return true
	}
	if content == compactedToolResultPlaceholder {
		return true
	}
	return strings.HasPrefix(content, historicalToolResultPlaceholder+"\n") ||
		strings.HasPrefix(content, compactedToolResultPlaceholder+"\n")
}

// ContextCompactionConfig controls request-side history compaction applied
// while projecting session events into a model request.
type ContextCompactionConfig struct {
	Enabled             bool
	KeepRecentRequests  int
	ToolResultMaxTokens int
	// OversizedToolResultMaxTokens is the token threshold above which any tool
	// result (including current-request results) is truncated using head+tail
	// preservation. Like Pass 1, this also requires Enabled=true; it will not
	// fire when context compaction is turned off, even if a positive threshold
	// is configured. 0 disables it regardless of Enabled.
	OversizedToolResultMaxTokens int
	// TokenCounter estimates request and tool-result size for compaction decisions.
	// When nil, SimpleTokenCounter is used.
	TokenCounter model.TokenCounter
	// SkipRecentFunc returns how many tail events should be treated as recent
	// and protected from historical tool-result compaction.
	SkipRecentFunc ContextCompactionSkipRecentFunc
	// SessionLoadRecoveryEnabled controls whether compacted tool-result
	// placeholders may instruct the model to recover payload slices with
	// session_load. It is derived from the actual request tool surface.
	SessionLoadRecoveryEnabled bool

	toolResultCompactionRules toolResultCompactionRules
}

// ContextCompactionSkipRecentFunc determines how many recent events should be
// protected from historical tool-result compaction.
type ContextCompactionSkipRecentFunc func(events []event.Event) int

// ContextCompactionStats reports how much prompt history was compacted during
// request projection.
type ContextCompactionStats struct {
	ToolResultsCompacted int
	EstimatedTokensSaved int
}

type toolResultCompactionRules struct {
	forceCleanToolNames map[string]struct{}
	keepToolNames       map[string]struct{}
}

func normalizeContextCompactionConfig(
	cfg ContextCompactionConfig,
) ContextCompactionConfig {
	if cfg.KeepRecentRequests < 0 {
		cfg.KeepRecentRequests = 0
	}
	if cfg.ToolResultMaxTokens < 0 {
		cfg.ToolResultMaxTokens = 0
	}
	if cfg.OversizedToolResultMaxTokens < 0 {
		cfg.OversizedToolResultMaxTokens = 0
	}
	if cfg.TokenCounter == nil {
		cfg.TokenCounter = model.NewSimpleTokenCounter()
	}
	cfg.toolResultCompactionRules.forceCleanToolNames = normalizeToolNameSet(
		cfg.toolResultCompactionRules.forceCleanToolNames,
	)
	cfg.toolResultCompactionRules.keepToolNames = normalizeToolNameSet(
		cfg.toolResultCompactionRules.keepToolNames,
	)
	return cfg
}

func normalizeToolNameSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for name := range in {
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func compactIncrementEvents(
	ctx context.Context,
	events []event.Event,
	currentRequestID string,
	currentInvocationID string,
	cfg ContextCompactionConfig,
) ([]event.Event, ContextCompactionStats) {
	cfg = normalizeContextCompactionConfig(cfg)
	if len(events) == 0 {
		return events, ContextCompactionStats{}
	}

	forceCleanActive := cfg.Enabled && cfg.hasForceCleanToolResults()
	pass1Active := cfg.Enabled && cfg.ToolResultMaxTokens > 0
	pass2Active := cfg.Enabled && cfg.OversizedToolResultMaxTokens > 0
	if !forceCleanActive && !pass1Active && !pass2Active {
		return events, ContextCompactionStats{}
	}

	compacted := make([]event.Event, len(events))
	copy(compacted, events)

	var stats ContextCompactionStats
	currentKey := compactionUnitKey(currentRequestID, currentInvocationID)
	protectedRequestIDs := collectProtectedRequestIDs(
		events,
		currentKey,
		cfg.KeepRecentRequests,
		cfg.SkipRecentFunc,
	)

	// Pass 0: named tool results → full placeholder replacement.
	// This is explicit user policy and applies before threshold-based passes.
	if forceCleanActive {
		passEvents, passStats := applyForceCleanToolResultPass(
			ctx,
			compacted,
			protectedRequestIDs,
			cfg,
		)
		compacted = passEvents
		stats = mergeContextCompactionStats(stats, passStats)
	}

	// Pass 1: historical tool results → full placeholder replacement.
	// Gated on Enabled (requires context compaction to be on).
	if pass1Active {
		if currentKey != "" {
			passEvents, passStats := applyHistoricalToolResultPass(
				ctx,
				compacted,
				protectedRequestIDs,
				cfg.ToolResultMaxTokens,
				cfg,
			)
			compacted = passEvents
			stats = mergeContextCompactionStats(stats, passStats)
		}
	}

	// Pass 2: oversized tool results (including current request) → head+tail
	// truncation. Gated on EnableContextCompaction together with Pass 1, so
	// the framework never silently rewrites tool results when context
	// compaction is disabled.
	if pass2Active {
		passEvents, passStats := applyOversizedToolResultPass(
			ctx,
			compacted,
			cfg.OversizedToolResultMaxTokens,
			cfg,
		)
		compacted = passEvents
		stats = mergeContextCompactionStats(stats, passStats)
	}

	return compacted, stats
}

func (cfg ContextCompactionConfig) hasForceCleanToolResults() bool {
	return len(cfg.toolResultCompactionRules.forceCleanToolNames) > 0
}

func (cfg ContextCompactionConfig) keepToolResult(msg model.Message) bool {
	if msg.ToolName == "" {
		return false
	}
	_, ok := cfg.toolResultCompactionRules.keepToolNames[msg.ToolName]
	return ok
}

func (cfg ContextCompactionConfig) forceCleanToolResult(msg model.Message) bool {
	if msg.ToolName == "" || cfg.keepToolResult(msg) {
		return false
	}
	_, ok := cfg.toolResultCompactionRules.forceCleanToolNames[msg.ToolName]
	return ok
}

func applyForceCleanToolResultPass(
	ctx context.Context,
	events []event.Event,
	protectedRequestIDs map[string]struct{},
	cfg ContextCompactionConfig,
) ([]event.Event, ContextCompactionStats) {
	var stats ContextCompactionStats
	for i := range events {
		if isProtectedCompactionEvent(events[i], protectedRequestIDs) {
			continue
		}
		evt, changed, compactedCount, savedTokens := rewriteToolResultEventMessages(
			ctx,
			events[i],
			0,
			cfg.SessionLoadRecoveryEnabled,
			func(ctx context.Context, msg model.Message, _ int, _ toolResultRecoveryRef) (model.Message, bool, int) {
				if !cfg.forceCleanToolResult(msg) {
					return msg, false, 0
				}
				return cleanToolResultMessageWithCounter(ctx, msg, cfg.TokenCounter)
			},
			"policy_force_clean",
		)
		if !changed {
			continue
		}
		events[i] = evt
		stats.ToolResultsCompacted += compactedCount
		stats.EstimatedTokensSaved += savedTokens
	}
	return events, stats
}

func applyHistoricalToolResultPass(
	ctx context.Context,
	events []event.Event,
	protectedRequestIDs map[string]struct{},
	maxTokens int,
	cfg ContextCompactionConfig,
) ([]event.Event, ContextCompactionStats) {
	var stats ContextCompactionStats
	for i := range events {
		evt, changed, compactedCount, savedTokens := compactHistoricalToolResultEvent(
			ctx,
			events[i],
			protectedRequestIDs,
			maxTokens,
			cfg,
		)
		if !changed {
			continue
		}
		events[i] = evt
		stats.ToolResultsCompacted += compactedCount
		stats.EstimatedTokensSaved += savedTokens
	}
	return events, stats
}

func isProtectedCompactionEvent(
	evt event.Event,
	protectedRequestIDs map[string]struct{},
) bool {
	unitKey := compactionUnitKey(evt.RequestID, evt.InvocationID)
	if unitKey == "" {
		return false
	}
	_, keep := protectedRequestIDs[unitKey]
	return keep
}

func compactHistoricalToolResultEvent(
	ctx context.Context,
	evt event.Event,
	protectedRequestIDs map[string]struct{},
	maxTokens int,
	cfg ContextCompactionConfig,
) (event.Event, bool, int, int) {
	if compactionUnitKey(evt.RequestID, evt.InvocationID) == "" {
		return evt, false, 0, 0
	}
	if isProtectedCompactionEvent(evt, protectedRequestIDs) {
		return evt, false, 0, 0
	}
	return rewriteToolResultEventMessages(
		ctx,
		evt,
		maxTokens,
		cfg.SessionLoadRecoveryEnabled,
		func(ctx context.Context, msg model.Message, maxTokens int, ref toolResultRecoveryRef) (model.Message, bool, int) {
			if cfg.keepToolResult(msg) {
				return msg, false, 0
			}
			return compactHistoricalToolResultMessageWithCounterAndRef(
				ctx, msg, maxTokens, cfg.TokenCounter, ref,
			)
		},
		"historical_compaction",
	)
}

func applyOversizedToolResultPass(
	ctx context.Context,
	events []event.Event,
	maxTokens int,
	cfg ContextCompactionConfig,
) ([]event.Event, ContextCompactionStats) {
	var stats ContextCompactionStats
	for i := range events {
		evt, changed, compactedCount, savedTokens := rewriteToolResultEventMessages(
			ctx,
			events[i],
			maxTokens,
			cfg.SessionLoadRecoveryEnabled,
			func(ctx context.Context, msg model.Message, maxTokens int, ref toolResultRecoveryRef) (model.Message, bool, int) {
				if cfg.keepToolResult(msg) {
					return msg, false, 0
				}
				return truncateOversizedToolResultMessageWithCounterAndRef(
					ctx, msg, maxTokens, cfg.TokenCounter, ref,
				)
			},
			"oversized_truncation",
		)
		if !changed {
			continue
		}
		events[i] = evt
		stats.ToolResultsCompacted += compactedCount
		stats.EstimatedTokensSaved += savedTokens
	}
	return events, stats
}

func rewriteToolResultEventMessages(
	ctx context.Context,
	evt event.Event,
	maxTokens int,
	sessionLoadAvailable bool,
	rewrite func(context.Context, model.Message, int, toolResultRecoveryRef) (model.Message, bool, int),
	reason string,
) (event.Event, bool, int, int) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return evt, false, 0, 0
	}

	var (
		choiceChanged   bool
		compactedCount  int
		totalSavedToken int
	)
	clonedResponse := evt.Response
	for j := range evt.Response.Choices {
		msg, changed, savedTokens := rewrite(
			ctx,
			evt.Response.Choices[j].Message,
			maxTokens,
			func() toolResultRecoveryRef {
				ref := toolResultRecoveryRefForMessage(
					evt,
					evt.Response.Choices[j].Message,
					reason,
				)
				ref.SessionLoadAvailable = sessionLoadAvailable
				return ref
			}(),
		)
		if !changed {
			continue
		}
		if !choiceChanged {
			clonedResponse = evt.Response.Clone()
			choiceChanged = true
		}
		clonedResponse.Choices[j].Message = msg
		compactedCount++
		totalSavedToken += savedTokens
	}
	if !choiceChanged {
		return evt, false, 0, 0
	}

	evt.Response = clonedResponse
	return evt, true, compactedCount, totalSavedToken
}

func mergeContextCompactionStats(
	base ContextCompactionStats,
	delta ContextCompactionStats,
) ContextCompactionStats {
	base.ToolResultsCompacted += delta.ToolResultsCompacted
	base.EstimatedTokensSaved += delta.EstimatedTokensSaved
	return base
}

func collectProtectedRequestIDs(
	events []event.Event,
	currentKey string,
	keepRecentRequests int,
	skipRecentFunc ContextCompactionSkipRecentFunc,
) map[string]struct{} {
	protected := map[string]struct{}{currentKey: {}}
	protectRecentEvents(protected, events, skipRecentFunc)
	if keepRecentRequests <= 0 {
		return protected
	}

	completed := collectCompletedCompactionUnitKeys(events)
	for i := len(events) - 1; i >= 0 && keepRecentRequests > 0; i-- {
		unitKey := compactionUnitKey(events[i].RequestID, events[i].InvocationID)
		if unitKey == "" || unitKey == currentKey {
			continue
		}
		if !completed[unitKey] {
			continue
		}
		if _, exists := protected[unitKey]; exists {
			continue
		}
		protected[unitKey] = struct{}{}
		keepRecentRequests--
	}
	return protected
}

func protectRecentEvents(
	protected map[string]struct{},
	events []event.Event,
	skipRecentFunc ContextCompactionSkipRecentFunc,
) {
	if skipRecentFunc == nil || len(events) == 0 {
		return
	}
	skipCount := skipRecentFunc(events)
	if skipCount <= 0 {
		return
	}
	if skipCount > len(events) {
		skipCount = len(events)
	}
	for i := len(events) - skipCount; i < len(events); i++ {
		unitKey := compactionUnitKey(events[i].RequestID, events[i].InvocationID)
		if unitKey == "" {
			continue
		}
		protected[unitKey] = struct{}{}
	}
}

func collectCompletedCompactionUnitKeys(events []event.Event) map[string]bool {
	completed := make(map[string]bool)
	for _, evt := range events {
		if evt.Response == nil || !evt.Response.Done {
			continue
		}
		unitKey := compactionUnitKey(evt.RequestID, evt.InvocationID)
		if unitKey == "" {
			continue
		}
		completed[unitKey] = true
	}
	return completed
}

func compactionUnitKey(requestID, invocationID string) string {
	switch {
	case requestID != "":
		return "req:" + requestID
	case invocationID != "":
		return "inv:" + invocationID
	default:
		return ""
	}
}

// truncateOversizedToolResultMessage applies head+tail truncation to any tool
// result whose estimated token count exceeds maxTokens. Unlike the historical
// placeholder compaction, this preserves the beginning and end of the content
// so the model can still see key information. Inspired by Codex's
// truncate_middle_chars and Claude Code's per-tool maxResultSizeChars.
//
// TODO: text ContentParts are preserved as-is; truncating individual text parts
// inside ContentParts is deferred until multimodal tool results are common.
func truncateOversizedToolResultMessage(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
) (model.Message, bool, int) {
	return truncateOversizedToolResultMessageWithCounter(
		ctx,
		msg,
		maxTokens,
		model.NewSimpleTokenCounter(),
	)
}

func truncateOversizedToolResultMessageWithCounter(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
	counter model.TokenCounter,
) (model.Message, bool, int) {
	ref := toolResultRecoveryRef{
		ToolCallID: msg.ToolID,
		ToolName:   msg.ToolName,
		Reason:     "oversized_truncation",
	}
	return truncateOversizedToolResultMessageWithCounterAndRef(
		ctx,
		msg,
		maxTokens,
		counter,
		ref,
	)
}

func truncateOversizedToolResultMessageWithCounterAndRef(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
	counter model.TokenCounter,
	ref toolResultRecoveryRef,
) (model.Message, bool, int) {
	if msg.Role != model.RoleTool || msg.ToolID == "" || maxTokens <= 0 {
		return msg, false, 0
	}
	if msg.Content == "" && len(msg.ContentParts) == 0 {
		return msg, false, 0
	}
	if msg.ToolName == sessionLoadToolName {
		return msg, false, 0
	}
	if isRecoverablePlaceholderContent(msg.Content) ||
		msg.Content == policyToolResultPlaceholder {
		return msg, false, 0
	}
	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}

	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil || originalTokens <= maxTokens {
		return msg, false, 0
	}

	truncated, ok := truncateMiddleToTokenBudget(ctx, msg, maxTokens, counter, ref)
	if !ok {
		return msg, false, 0
	}

	result := msg
	result.Content = truncated
	if len(msg.ContentParts) > 0 {
		result.ContentParts = append([]model.ContentPart(nil), msg.ContentParts...)
	}
	if len(msg.ToolCalls) > 0 {
		result.ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
	}
	resultTokens, err := counter.CountTokens(ctx, result)
	if err != nil || resultTokens >= originalTokens {
		return msg, false, 0
	}

	return result, true, originalTokens - resultTokens
}

func truncateMiddleToTokenBudget(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
	counter model.TokenCounter,
	ref toolResultRecoveryRef,
) (string, bool) {
	if msg.Content == "" || counter == nil || maxTokens <= 0 {
		return "", false
	}

	runeCount := utf8.RuneCountInString(msg.Content)
	low, high := 0, runeCount-1
	best := ""
	found := false
	for low <= high {
		mid := low + (high-low)/2
		candidate := truncateMiddleWithRef(msg.Content, mid, ref)
		candidateMsg := msg
		candidateMsg.Content = candidate
		tokens, err := counter.CountTokens(ctx, candidateMsg)
		if err != nil {
			return "", false
		}
		if tokens <= maxTokens {
			best = candidate
			found = true
			low = mid + 1
			continue
		}
		high = mid - 1
	}
	return best, found
}

// truncateMiddle keeps the first half and last half of the content (by
// character count) up to maxChars total, inserting a marker in the middle
// showing how much was removed. This preserves the beginning (usually
// contains key structure/headers) and end (usually contains conclusions)
// of the tool output.
func truncateMiddle(s string, maxChars int) string {
	return truncateMiddleWithRef(s, maxChars, toolResultRecoveryRef{})
}

func truncateMiddleWithRef(
	s string,
	maxChars int,
	ref toolResultRecoveryRef,
) string {
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxChars {
		return s
	}

	removed := runeCount - maxChars
	marker := fmt.Sprintf("\n\n[... %d characters truncated ...]\n\n", removed)
	if ref.EventID != "" || ref.ToolCallID != "" {
		marker = recoverableTruncationMarker(ref, removed)
	}
	markerLen := utf8.RuneCountInString(marker)

	available := maxChars - markerLen
	if available < 2 && (ref.EventID != "" || ref.ToolCallID != "") {
		marker = compactRecoverableTruncationMarker(ref, removed)
		markerLen = utf8.RuneCountInString(marker)
		available = maxChars - markerLen
	}
	if available < 2 {
		runes := []rune(s)
		if ref.EventID != "" || ref.ToolCallID != "" {
			return marker
		}
		return string(runes[:maxChars])
	}
	halfBudget := available / 2

	runes := []rune(s)
	head := string(runes[:halfBudget])
	tail := string(runes[runeCount-halfBudget:])
	return head + marker + tail
}

func cleanToolResultMessageWithCounter(
	ctx context.Context,
	msg model.Message,
	counter model.TokenCounter,
) (model.Message, bool, int) {
	if msg.Role != model.RoleTool || msg.ToolID == "" {
		return msg, false, 0
	}
	if msg.Content == "" && len(msg.ContentParts) == 0 {
		return msg, false, 0
	}
	if (isRecoverablePlaceholderContent(msg.Content) ||
		msg.Content == policyToolResultPlaceholder) &&
		len(msg.ContentParts) == 0 {
		return msg, false, 0
	}
	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}

	// Force-clean is policy-driven, not threshold-driven. Even if token
	// counting fails, still replace the payload with policyToolResultPlaceholder;
	// savedTokens falls back to 0 because the exact savings are unknown.
	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil {
		originalTokens = 0
	}
	compacted := model.Message{
		Role:     msg.Role,
		Content:  policyToolResultPlaceholder,
		ToolID:   msg.ToolID,
		ToolName: msg.ToolName,
	}
	compactedTokens, err := counter.CountTokens(ctx, compacted)
	if err != nil {
		compactedTokens = 0
	}
	savedTokens := originalTokens - compactedTokens
	if savedTokens < 0 {
		savedTokens = 0
	}
	return compacted, true, savedTokens
}

func compactHistoricalToolResultMessage(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
) (model.Message, bool, int) {
	return compactHistoricalToolResultMessageWithCounter(
		ctx,
		msg,
		maxTokens,
		model.NewSimpleTokenCounter(),
	)
}

func compactHistoricalToolResultMessageWithCounter(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
	counter model.TokenCounter,
) (model.Message, bool, int) {
	ref := toolResultRecoveryRef{
		ToolCallID: msg.ToolID,
		ToolName:   msg.ToolName,
		Reason:     "historical_compaction",
	}
	return compactHistoricalToolResultMessageWithCounterAndRef(
		ctx,
		msg,
		maxTokens,
		counter,
		ref,
	)
}

func compactHistoricalToolResultMessageWithCounterAndRef(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
	counter model.TokenCounter,
	ref toolResultRecoveryRef,
) (model.Message, bool, int) {
	if msg.Role != model.RoleTool || msg.ToolID == "" || maxTokens <= 0 {
		return msg, false, 0
	}
	if isRecoverablePlaceholderContent(msg.Content) && len(msg.ContentParts) == 0 {
		return msg, false, 0
	}

	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}
	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil || originalTokens <= maxTokens {
		return msg, false, 0
	}

	compacted := model.Message{
		Role:     msg.Role,
		Content:  recoverableToolResultPlaceholder(ref),
		ToolID:   msg.ToolID,
		ToolName: msg.ToolName,
	}
	compactedTokens, err := counter.CountTokens(ctx, compacted)
	if err != nil || compactedTokens >= originalTokens {
		return msg, false, 0
	}

	return compacted, true, originalTokens - compactedTokens
}
