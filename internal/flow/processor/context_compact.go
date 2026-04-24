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

	historicalToolResultPlaceholder = "Historical tool result omitted to save context."
)

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
}

// ContextCompactionStats reports how much prompt history was compacted during
// request projection.
type ContextCompactionStats struct {
	ToolResultsCompacted int
	EstimatedTokensSaved int
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
	return cfg
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

	pass1Active := cfg.Enabled && cfg.ToolResultMaxTokens > 0
	pass2Active := cfg.Enabled && cfg.OversizedToolResultMaxTokens > 0
	if !pass1Active && !pass2Active {
		return events, ContextCompactionStats{}
	}

	compacted := make([]event.Event, len(events))
	copy(compacted, events)

	var stats ContextCompactionStats

	// Pass 1: historical tool results → full placeholder replacement.
	// Gated on Enabled (requires context compaction to be on).
	if pass1Active {
		currentKey := compactionUnitKey(currentRequestID, currentInvocationID)
		if currentKey != "" {
			passEvents, passStats := applyHistoricalToolResultPass(
				ctx,
				compacted,
				currentKey,
				cfg.KeepRecentRequests,
				cfg.ToolResultMaxTokens,
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
		)
		compacted = passEvents
		stats = mergeContextCompactionStats(stats, passStats)
	}

	return compacted, stats
}

func applyHistoricalToolResultPass(
	ctx context.Context,
	events []event.Event,
	currentKey string,
	keepRecentRequests int,
	maxTokens int,
) ([]event.Event, ContextCompactionStats) {
	protectedRequestIDs := collectProtectedRequestIDs(
		events,
		currentKey,
		keepRecentRequests,
	)

	var stats ContextCompactionStats
	for i := range events {
		evt, changed, compactedCount, savedTokens := compactHistoricalToolResultEvent(
			ctx,
			events[i],
			protectedRequestIDs,
			maxTokens,
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

func compactHistoricalToolResultEvent(
	ctx context.Context,
	evt event.Event,
	protectedRequestIDs map[string]struct{},
	maxTokens int,
) (event.Event, bool, int, int) {
	unitKey := compactionUnitKey(evt.RequestID, evt.InvocationID)
	if unitKey == "" {
		return evt, false, 0, 0
	}
	if _, keep := protectedRequestIDs[unitKey]; keep {
		return evt, false, 0, 0
	}
	return rewriteToolResultEventMessages(
		ctx,
		evt,
		maxTokens,
		compactHistoricalToolResultMessage,
	)
}

func applyOversizedToolResultPass(
	ctx context.Context,
	events []event.Event,
	maxTokens int,
) ([]event.Event, ContextCompactionStats) {
	var stats ContextCompactionStats
	for i := range events {
		evt, changed, compactedCount, savedTokens := rewriteToolResultEventMessages(
			ctx,
			events[i],
			maxTokens,
			truncateOversizedToolResultMessage,
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
	rewrite func(context.Context, model.Message, int) (model.Message, bool, int),
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
) map[string]struct{} {
	protected := map[string]struct{}{currentKey: {}}
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
	if msg.Role != model.RoleTool || msg.ToolID == "" || maxTokens <= 0 {
		return msg, false, 0
	}
	if msg.Content == "" && len(msg.ContentParts) == 0 {
		return msg, false, 0
	}
	if msg.Content == historicalToolResultPlaceholder {
		return msg, false, 0
	}

	counter := model.NewSimpleTokenCounter()
	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil || originalTokens <= maxTokens {
		return msg, false, 0
	}

	// Approximate the character budget from the token budget.
	// SimpleTokenCounter uses ~4 chars/token, so we reverse that.
	maxChars := maxTokens * 4
	truncated := truncateMiddle(msg.Content, maxChars)

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

// truncateMiddle keeps the first half and last half of the content (by
// character count) up to maxChars total, inserting a marker in the middle
// showing how much was removed. This preserves the beginning (usually
// contains key structure/headers) and end (usually contains conclusions)
// of the tool output.
func truncateMiddle(s string, maxChars int) string {
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxChars {
		return s
	}

	removed := runeCount - maxChars
	marker := fmt.Sprintf("\n\n[... %d characters truncated ...]\n\n", removed)
	markerLen := utf8.RuneCountInString(marker)

	available := maxChars - markerLen
	if available < 2 {
		runes := []rune(s)
		return string(runes[:maxChars])
	}
	halfBudget := available / 2

	runes := []rune(s)
	head := string(runes[:halfBudget])
	tail := string(runes[runeCount-halfBudget:])
	return head + marker + tail
}

func compactHistoricalToolResultMessage(
	ctx context.Context,
	msg model.Message,
	maxTokens int,
) (model.Message, bool, int) {
	if msg.Role != model.RoleTool || msg.ToolID == "" || maxTokens <= 0 {
		return msg, false, 0
	}
	if msg.Content == historicalToolResultPlaceholder &&
		len(msg.ContentParts) == 0 {
		return msg, false, 0
	}

	// SimpleTokenCounter is intentionally heuristic-based; Phase 1 only needs a
	// cheap approximation to decide whether a historical tool result is worth
	// replacing with a placeholder.
	counter := model.NewSimpleTokenCounter()
	originalTokens, err := counter.CountTokens(ctx, msg)
	if err != nil || originalTokens <= maxTokens {
		return msg, false, 0
	}

	compacted := model.Message{
		Role:     msg.Role,
		Content:  historicalToolResultPlaceholder,
		ToolID:   msg.ToolID,
		ToolName: msg.ToolName,
	}
	compactedTokens, err := counter.CountTokens(ctx, compacted)
	if err != nil || compactedTokens >= originalTokens {
		return msg, false, 0
	}

	return compacted, true, originalTokens - compactedTokens
}
