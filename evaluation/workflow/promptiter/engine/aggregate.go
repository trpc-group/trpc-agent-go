//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
)

// AggregationResult groups all surfaces after sample gradient merge.
type AggregationResult struct {
	// Surfaces stores merged gradients to feed optimizer per surface.
	Surfaces []promptiter.AggregatedSurfaceGradient
}

// aggregate requests per-surface aggregation and normalizes gradient inputs.
func (e *engine) aggregate(ctx context.Context) error {
	req := &aggregator.Request{}
	rsp, err := e.aggregator.Aggregate(ctx, req)
	if err != nil {
		return err
	}
	_ = rsp
	return nil
}
