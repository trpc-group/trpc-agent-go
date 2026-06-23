//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

func recordPromptCacheUsage(
	trace *debugrecorder.Trace,
	sessionID string,
	requestID string,
	usage *gwproto.Usage,
) {
	if trace == nil || usage == nil {
		return
	}
	_ = trace.Record(
		debugrecorder.KindPromptCache,
		promptCacheUsageRecord(sessionID, requestID, usage),
	)
}

func promptCacheUsageRecord(
	sessionID string,
	requestID string,
	usage *gwproto.Usage,
) map[string]any {
	prompt := usage.PromptTokens
	details := usage.PromptDetails
	cached := promptCachedTokens(details)

	record := map[string]any{
		"session_id":        sessionID,
		"request_id":        requestID,
		"prompt_tokens":     prompt,
		"cached_tokens":     cached,
		"uncached_tokens":   nonNegativeDelta(prompt, cached),
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
	}
	if prompt > 0 {
		record["cache_hit_ratio"] = float64(cached) / float64(prompt)
	}
	addPromptCacheDetails(record, "", details)

	lastPrompt := usage.LastPromptTokens
	if lastPrompt > 0 {
		lastDetails := usage.LastDetails
		lastCached := promptCachedTokens(lastDetails)
		record["last_prompt_tokens"] = lastPrompt
		record["last_cached_tokens"] = lastCached
		record["last_uncached_tokens"] = nonNegativeDelta(
			lastPrompt,
			lastCached,
		)
		record["last_cache_hit_ratio"] = float64(lastCached) /
			float64(lastPrompt)
		addPromptCacheDetails(record, "last_", lastDetails)
	}
	return record
}

func addPromptCacheDetails(
	record map[string]any,
	prefix string,
	details *gwproto.PromptDetails,
) {
	if details == nil {
		return
	}
	if details.CacheCreationTokens != 0 {
		record[prefix+"cache_creation_tokens"] =
			details.CacheCreationTokens
	}
	if details.CacheReadTokens != 0 {
		record[prefix+"cache_read_tokens"] = details.CacheReadTokens
	}
}

func promptCachedTokens(details *gwproto.PromptDetails) int {
	if details == nil {
		return 0
	}
	if details.CacheReadTokens > 0 {
		return details.CacheReadTokens
	}
	return details.CachedTokens
}

func nonNegativeDelta(total int, part int) int {
	if total <= part {
		return 0
	}
	return total - part
}
