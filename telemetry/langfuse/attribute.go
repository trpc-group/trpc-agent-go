//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

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

	// Internal
	asRoot = "langfuse.internal.as_root"
)
