//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"strconv"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/respjson"
)

// Prompt cache fields reported by OpenAI models and OpenAI-compatible gateways.
//
// The openai-go v1 schema only defines prompt_tokens_details.cached_tokens.
// Newer OpenAI models and OpenAI-compatible providers/gateways report prompt
// cache writes (and gateway-specific cache reads) as extra fields inside
// prompt_tokens_details or top-level usage, and the SDK retains those unknown
// fields in respjson ExtraFields keyed by their original JSON name.
const (
	// extraFieldCacheReadTokens is the number of prompt tokens served from cache.
	extraFieldCacheReadTokens = "cache_read_tokens" // #nosec G101 - JSON field name, not a credential.
	// extraFieldCacheCreationTokens is the gateway-style cache write token count.
	extraFieldCacheCreationTokens = "cache_creation_tokens" // #nosec G101 - JSON field name, not a credential.
	// extraFieldCacheWriteTokens is the OpenAI/compatible cache write token count
	// (e.g., OpenAI, Azure OpenAI, OpenRouter, Moonshot/Kimi, Tencent TokenHub).
	extraFieldCacheWriteTokens = "cache_write_tokens" // #nosec G101 - JSON field name, not a credential.
	// extraFieldCacheCreationInputTokens is the Anthropic-style cache write token
	// count used by some OpenAI-compatible endpoints (e.g., DashScope/Bailian).
	extraFieldCacheCreationInputTokens = "cache_creation_input_tokens" // #nosec G101 - JSON field name, not a credential.
)

// cacheWriteFieldPrecedence is the alias order used to resolve the prompt
// cache write count, identical for prompt_tokens_details and top-level usage.
// The OpenAI field name comes first, followed by the gateway-style and
// Anthropic-style aliases.
var cacheWriteFieldPrecedence = []string{
	extraFieldCacheWriteTokens,
	extraFieldCacheCreationTokens,
	extraFieldCacheCreationInputTokens,
}

// usageCacheTokens extracts prompt cache read and cache write counts from a
// usage payload. Both counts are 0 when the provider does not report them.
//
// Cache writes are resolved with cacheWriteFieldPrecedence, first in
// prompt_tokens_details and then in top-level usage as a fallback. The first
// positive integer wins, so aliases reported together are never summed and
// zero-valued placeholder fields do not shadow a real count.
func usageCacheTokens(usage openai.CompletionUsage) (cacheRead, cacheCreation int) {
	detailFields := usage.PromptTokensDetails.JSON.ExtraFields
	usageFields := usage.JSON.ExtraFields
	// Streaming calls this once per chunk, so skip the parsing work entirely
	// when the provider reported no extra fields.
	if len(detailFields) == 0 && len(usageFields) == 0 {
		return 0, 0
	}
	cacheRead = extraTokenCount(detailFields[extraFieldCacheReadTokens])
	cacheCreation = firstPositiveExtraTokenCount(detailFields, cacheWriteFieldPrecedence...)
	if cacheCreation == 0 && len(usageFields) > 0 {
		cacheCreation = firstPositiveExtraTokenCount(usageFields, cacheWriteFieldPrecedence...)
	}
	return cacheRead, cacheCreation
}

// firstPositiveExtraTokenCount returns the first positive integer parsed from
// fields using the given keys in priority order, or 0 if none is positive.
func firstPositiveExtraTokenCount(fields map[string]respjson.Field, keys ...string) int {
	if len(fields) == 0 {
		return 0
	}
	for _, key := range keys {
		if count := extraTokenCount(fields[key]); count > 0 {
			return count
		}
	}
	return 0
}

// setUsageCacheTokens stamps cache token counts onto a usage payload so that
// completionUsageToModelUsage can convert them. Positive counts are written
// under the canonical keys into a copy of the existing fields, leaving
// unrelated provider-supplied metadata intact.
func setUsageCacheTokens(usage *openai.CompletionUsage, cacheRead, cacheCreation int) {
	if cacheRead <= 0 && cacheCreation <= 0 {
		return
	}
	fields := make(map[string]respjson.Field, len(usage.PromptTokensDetails.JSON.ExtraFields)+2)
	for name, field := range usage.PromptTokensDetails.JSON.ExtraFields {
		fields[name] = field
	}
	if cacheRead > 0 {
		fields[extraFieldCacheReadTokens] = respjson.NewField(strconv.Itoa(cacheRead))
	}
	if cacheCreation > 0 {
		fields[extraFieldCacheCreationTokens] = respjson.NewField(strconv.Itoa(cacheCreation))
		delete(fields, extraFieldCacheWriteTokens)
		delete(fields, extraFieldCacheCreationInputTokens)
	}
	usage.PromptTokensDetails.JSON.ExtraFields = fields
}

// extraTokenCount parses a token count, returning 0 when the field is absent or
// not a non-negative integer.
//
// Field.Valid is intentionally not checked: the SDK marks extra fields as
// invalid while still keeping their raw JSON.
func extraTokenCount(field respjson.Field) int {
	raw := field.Raw()
	if raw == "" {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || count < 0 {
		return 0
	}
	return count
}
