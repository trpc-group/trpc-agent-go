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
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

func (e *engine) optimize(
	ctx context.Context,
	structure *structureState,
	profile *promptiter.Profile,
	aggregation *AggregationResult,
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
	patches := make([]promptiter.SurfacePatch, 0, len(aggregation.Surfaces))
	for _, aggregatedSurface := range aggregation.Surfaces {
		surface, err := resolveProfileSurface(structure, overrideIndex, aggregatedSurface.SurfaceID)
		if err != nil {
			return nil, err
		}
		response, err := e.optimizer.Optimize(ctx, &optimizer.Request{
			Surface:  &surface,
			Gradient: cloneAggregatedGradient(aggregatedSurface),
		})
		if err != nil {
			return nil, fmt.Errorf("optimize surface %q: %w", aggregatedSurface.SurfaceID, err)
		}
		if response == nil || response.Patch == nil {
			return nil, fmt.Errorf("optimize surface %q returned empty result", aggregatedSurface.SurfaceID)
		}
		patches = append(patches, *response.Patch)
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
