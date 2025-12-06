//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package invocationsaggregator

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

type InvocationsAggregator interface {
	// AggregateInvocations aggregates per-invocation results into the final evaluation.
	AggregateInvocations(ctx context.Context,
		results []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error)
}
