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

	historicalToolResultPlaceholder = "Historical tool result omitted to save context."
)

// ContextCompactionConfig controls request-side history compaction applied
// while projecting session events into a model request.
type ContextCompactionConfig struct {
	Enabled             bool
	KeepRecentRequests  int
	ToolResultMaxTokens int
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
	currentKey := compactionUnitKey(currentRequestID, currentInvocationID)
	if !cfg.Enabled || cfg.ToolResultMaxTokens <= 0 ||
		len(events) == 0 || currentKey == "" {
		return events, ContextCompactionStats{}
	}

	protectedRequestIDs := collectProtectedRequestIDs(
		events,
		currentKey,
		cfg.KeepRecentRequests,
	)

	compacted := make([]event.Event, len(events))
	copy(compacted, events)

	var stats ContextCompactionStats
	for i := range compacted {
		evt := compacted[i]
		unitKey := compactionUnitKey(evt.RequestID, evt.InvocationID)
		if unitKey == "" {
			continue
		}
		if _, keep := protectedRequestIDs[unitKey]; keep {
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}

		var choiceChanged bool
		clonedResponse := evt.Response
		for j := range evt.Response.Choices {
			msg, compactedMsg, savedTokens := compactHistoricalToolResultMessage(
				ctx,
				evt.Response.Choices[j].Message,
				cfg.ToolResultMaxTokens,
			)
			if !compactedMsg {
				continue
			}
			if !choiceChanged {
				clonedResponse = evt.Response.Clone()
				choiceChanged = true
			}
			clonedResponse.Choices[j].Message = msg
			stats.ToolResultsCompacted++
			stats.EstimatedTokensSaved += savedTokens
		}

		if choiceChanged {
			evt.Response = clonedResponse
			compacted[i] = evt
		}
	}

	return compacted, stats
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
