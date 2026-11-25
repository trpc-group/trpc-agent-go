//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"encoding/json"

	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// Langfuse-Trace attributes
const (
	traceName      = "langfuse.trace.name"
	traceUserID    = "langfuse.user.id"
	traceSessionID = "langfuse.session.id"
	traceTags      = "langfuse.trace.tags"
	tracePublic    = "langfuse.trace.public"
	traceMetadata  = "langfuse.trace.metadata"
	traceInput     = "langfuse.trace.input"
	traceOutput    = "langfuse.trace.output"

	// Langfuse-observation attributes
	observationType          = "langfuse.observation.type"
	observationMetadata      = "langfuse.observation.metadata"
	observationLevel         = "langfuse.observation.level"
	observationStatusMessage = "langfuse.observation.status_message"
	observationInput         = "langfuse.observation.input"
	observationOutput        = "langfuse.observation.output"

	// Langfuse-observation of type Generation attributes
	observationCompletionStartTime = "langfuse.observation.completion_start_time"
	observationModel               = "langfuse.observation.model.name"
	observationModelParameters     = "langfuse.observation.model.parameters"
	observationUsageDetails        = "langfuse.observation.usage_details"
	observationCostDetails         = "langfuse.observation.cost_details"
	observationPromptName          = "langfuse.observation.prompt.name"
	observationPromptVersion       = "langfuse.observation.prompt.version"

	// Langfuse-observation types (see https://langfuse.com/docs/observability/features/observation-types)
	observationTypeEvent      = "event"
	observationTypeSpan       = "span"
	observationTypeGeneration = "generation"
	observationTypeAgent      = "agent"
	observationTypeTool       = "tool"
	observationTypeChain      = "chain"
	observationTypeRetriever  = "retriever"
	observationTypeEvaluator  = "evaluator"
	observationTypeEmbedding  = "embedding"
	observationTypeGuardrail  = "guardrail"

	// General
	environment = "langfuse.environment"
	release     = "langfuse.release"
	version     = "langfuse.version"
)

// usageDetails collects token usage metrics for Langfuse's usage_details JSON field.
// Fields follow Langfuse conventions and use omitempty to exclude zero-value fields.
type usageDetails struct {
	Input              int64 `json:"input,omitempty"`
	Output             int64 `json:"output,omitempty"`
	InputCached        int64 `json:"input_cached,omitempty"`
	InputCacheRead     int64 `json:"input_cache_read,omitempty"`
	InputCacheCreation int64 `json:"input_cache_creation,omitempty"`
}

// empty reports whether all fields are zero.
func (u *usageDetails) empty() bool {
	return *u == (usageDetails{})
}

// normalized returns mutually exclusive usage buckets as required by Langfuse.
//
// When cache_accounting is explicitly provided on the span:
//   - "inclusive" (OpenAI Chat Completions, OpenAI Responses API, Gemini):
//     Input includes cache read and cache write tokens, so both are deducted from
//     Input (including write-only turns where InputCached == 0).
//   - "exclusive" (Anthropic, Bedrock): Input already represents uncached input
//     tokens and is left untouched while dropping the duplicate InputCached alias.
//
// When cache_accounting is absent (legacy spans), it falls back to inferring
// from cache buckets for backward compatibility.
func (u usageDetails) normalized(accounting string) usageDetails {
	switch accounting {
	case semconvtrace.ValueGenAIUsageInputTokensCacheAccountingInclusive:
		if u.InputCacheRead != 0 {
			u.InputCached = 0
			u.Input = max(u.Input-u.InputCacheRead-u.InputCacheCreation, 0)
			return u
		}
		u.Input = max(u.Input-u.InputCached-u.InputCacheCreation, 0)
		return u

	case semconvtrace.ValueGenAIUsageInputTokensCacheAccountingExclusive:
		if u.InputCacheRead != 0 || u.InputCacheCreation != 0 {
			u.InputCached = 0
		}
		return u
	}

	// Backward-compatible fallback when cache_accounting is not present on the span.
	if u.InputCacheRead != 0 {
		// Anthropic/Bedrock and explicit cache_read_tokens buckets:
		// drop the duplicate InputCached compatibility alias.
		u.InputCached = 0
		return u
	}
	if u.InputCached != 0 {
		// OpenAI-compatible inclusive semantics: keep cached and cache_creation in
		// their own buckets and convert the inclusive input count to the uncached remainder.
		u.Input = max(u.Input-u.InputCached-u.InputCacheCreation, 0)
		return u
	}
	return u
}

// observationInputPrompt is the Langfuse observation.input shape.
//
// tools is passed through as raw JSON from gen_ai.request.tool.definitions.
// messages is kept as raw JSON.
type observationInputPrompt struct {
	Tools    json.RawMessage `json:"tools,omitempty"`
	Messages json.RawMessage `json:"messages,omitempty"`
}
