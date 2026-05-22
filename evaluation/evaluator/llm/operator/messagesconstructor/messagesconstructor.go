//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package messagesconstructor builds judge prompts from invocation context.
package messagesconstructor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessagesConstructor defines the interface for building judge prompts.
type MessagesConstructor interface {
	// ConstructMessages builds prompts for the judge model.
	// LLMBaseEvaluator passes per-invocation prefix slices: actuals[:i+1] and expecteds[:i+1].
	// Implementations should treat the slices as grounding context up to the current invocation.
	ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) ([]model.Message, error)
}

// StructuredOutputMessagesConstructor extends MessagesConstructor with a structured output contract.
type StructuredOutputMessagesConstructor interface {
	MessagesConstructor
	// StructuredOutput returns the structured output schema for the judge model.
	// LLMBaseEvaluator calls it with the same per-invocation prefix slices used for ConstructMessages.
	StructuredOutput(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) (*model.StructuredOutput, error)
}
