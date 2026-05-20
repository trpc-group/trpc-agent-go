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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

// OptimizerOptions configures optimizer-stage execution behavior.
type OptimizerOptions struct {
	// SurfaceParallelismEnabled enables concurrent optimization across target surfaces.
	SurfaceParallelismEnabled bool
	// SurfaceParallelism caps concurrent optimization across target surfaces when SurfaceParallelismEnabled is true. Zero uses GOMAXPROCS.
	SurfaceParallelism int
}

func (e *engine) optimize(
	ctx context.Context,
	structure *structureState,
	profile *promptiter.Profile,
	aggregation *AggregationResult,
	targetSurfaceSet targetSurfaceSet,
	options OptimizerOptions,
) (*promptiter.PatchSet, error) {
	if e.optimizer == nil {
		return nil, errors.New("optimizer is nil")
	}
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	if aggregation == nil || len(aggregation.Surfaces) == 0 {
		return &promptiter.PatchSet{Patches: []promptiter.SurfacePatch{}}, nil
	}
	overrideIndex := buildOverrideIndex(profile)
	patches := make([]promptiter.SurfacePatch, len(aggregation.Surfaces))
	parallelism := 0
	if options.SurfaceParallelismEnabled {
		parallelism = options.SurfaceParallelism
		if parallelism <= 0 {
			parallelism = runtime.GOMAXPROCS(0)
		}
	}
	if err := runIndexedParallel(ctx, len(aggregation.Surfaces), parallelism, func(ctx context.Context, index int) error {
		aggregatedSurface := aggregation.Surfaces[index]
		if !targetSurfaceSet.contains(aggregatedSurface.SurfaceID) {
			return fmt.Errorf("aggregated surface id %q is outside target surfaces", aggregatedSurface.SurfaceID)
		}
		surface, err := resolveProfileSurface(structure, overrideIndex, aggregatedSurface.SurfaceID)
		if err != nil {
			return err
		}
		response, err := e.optimizer.Optimize(ctx, &optimizer.Request{
			Surface:  &surface,
			Gradient: cloneAggregatedGradient(aggregatedSurface),
		})
		if err != nil {
			return fmt.Errorf("optimize surface %q: %w", aggregatedSurface.SurfaceID, err)
		}
		if response == nil || response.Patch == nil {
			return fmt.Errorf("optimize surface %q returned empty result", aggregatedSurface.SurfaceID)
		}
		patches[index] = *response.Patch
		return nil
	}); err != nil {
		return nil, err
	}
	sort.SliceStable(patches, func(i, j int) bool {
		return patches[i].SurfaceID < patches[j].SurfaceID
	})
	return &promptiter.PatchSet{Patches: patches}, nil
}

func cloneAggregatedGradient(gradient promptiter.AggregatedSurfaceGradient) *promptiter.AggregatedSurfaceGradient {
	cloned := &promptiter.AggregatedSurfaceGradient{
		SurfaceID: gradient.SurfaceID,
		NodeID:    gradient.NodeID,
		Type:      gradient.Type,
		Gradients: append([]promptiter.SurfaceGradient(nil), gradient.Gradients...),
	}
	return cloned
}
