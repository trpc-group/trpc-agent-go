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
	"errors"
	"fmt"
	"runtime"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
)

// AggregationOptions configures aggregation-stage execution behavior.
type AggregationOptions struct {
	// SurfaceParallelismEnabled enables concurrent aggregation across target surfaces.
	SurfaceParallelismEnabled bool
	// SurfaceParallelism caps concurrent aggregation across target surfaces when SurfaceParallelismEnabled is true. Zero uses GOMAXPROCS.
	SurfaceParallelism int
}

// AggregationResult groups all surfaces after sample gradient merge.
type AggregationResult struct {
	// Surfaces stores merged gradients to feed optimizer per surface.
	Surfaces []promptiter.AggregatedSurfaceGradient
}

func (e *engine) aggregate(
	ctx context.Context,
	structure *structureState,
	backward *BackwardResult,
	targetSurfaceSet targetSurfaceSet,
	options AggregationOptions,
) (*AggregationResult, error) {
	if e.aggregator == nil {
		return nil, errors.New("aggregator is nil")
	}
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	grouped := make(map[string][]promptiter.SurfaceGradient)
	if backward != nil {
		for _, caseResult := range backward.Cases {
			for _, stepGradient := range caseResult.StepGradients {
				for _, gradient := range stepGradient.Gradients {
					if !targetSurfaceSet.contains(gradient.SurfaceID) {
						return nil, fmt.Errorf("step gradient surface id %q is outside target surfaces", gradient.SurfaceID)
					}
					grouped[gradient.SurfaceID] = append(grouped[gradient.SurfaceID], gradient)
				}
			}
		}
	}
	surfaceIDs := make([]string, 0, len(grouped))
	for surfaceID := range grouped {
		surfaceIDs = append(surfaceIDs, surfaceID)
	}
	sort.Strings(surfaceIDs)
	result := &AggregationResult{
		Surfaces: make([]promptiter.AggregatedSurfaceGradient, 0, len(surfaceIDs)),
	}
	surfaces := make([]promptiter.AggregatedSurfaceGradient, len(surfaceIDs))
	parallelism := 0
	if options.SurfaceParallelismEnabled {
		parallelism = options.SurfaceParallelism
		if parallelism <= 0 {
			parallelism = runtime.GOMAXPROCS(0)
		}
	}
	if err := runIndexedParallel(ctx, len(surfaceIDs), parallelism, func(ctx context.Context, index int) error {
		surfaceID := surfaceIDs[index]
		surface, ok := structure.surfaceIndex[surfaceID]
		if !ok {
			return fmt.Errorf("aggregated surface id %q is unknown", surfaceID)
		}
		response, err := e.aggregator.Aggregate(ctx, &aggregator.Request{
			SurfaceID: surfaceID,
			NodeID:    surface.NodeID,
			Type:      surface.Type,
			Gradients: grouped[surfaceID],
		})
		if err != nil {
			return fmt.Errorf("aggregate surface %q: %w", surfaceID, err)
		}
		if response == nil || response.Gradient == nil {
			return fmt.Errorf("aggregate surface %q returned empty result", surfaceID)
		}
		surfaces[index] = *response.Gradient
		return nil
	}); err != nil {
		return nil, err
	}
	result.Surfaces = append(result.Surfaces, surfaces...)
	return result, nil
}
