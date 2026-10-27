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

// Prompt cache fields reported by OpenAI-compatible gateways.
//
// The OpenAI schema only defines prompt_tokens_details.cached_tokens. Gateways
// that serve Anthropic-style caching add cache reads and cache writes to
// prompt_tokens_details, and the SDK keeps those unknown nested fields in
// respjson ExtraFields, keyed by their original JSON name.
const (
	// extraFieldCacheReadTokens is the number of prompt tokens served from cache.
	extraFieldCacheReadTokens = "cache_read_tokens"
	// extraFieldCacheCreationTokens is the number of prompt tokens written to cache.
	extraFieldCacheCreationTokens = "cache_creation_tokens"
)

// usageCacheTokens extracts prompt cache read and cache write counts from a
// usage payload. Both counts are 0 when the gateway does not report them.
func usageCacheTokens(usage openai.CompletionUsage) (cacheRead, cacheCreation int) {
	fields := usage.PromptTokensDetails.JSON.ExtraFields
	// Streaming calls this once per chunk, so skip the parsing work entirely
	// when the gateway reported no extra fields.
	if len(fields) == 0 {
		return 0, 0
	}
	return extraTokenCount(fields[extraFieldCacheReadTokens]),
		extraTokenCount(fields[extraFieldCacheCreationTokens])
}

// setUsageCacheTokens stamps cache token counts onto a usage payload so that
// completionUsageToModelUsage can convert them. Positive counts are added to a
// copy of the existing fields, leaving the provider-supplied metadata intact.
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
