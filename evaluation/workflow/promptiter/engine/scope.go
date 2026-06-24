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
	"errors"
	"fmt"
)

type targetSurfaceSet map[string]struct{}

func compileTargetSurfaceIDs(
	structure *structureState,
	targetSurfaceIDs []string,
) (targetSurfaceSet, error) {
	if targetSurfaceIDs == nil {
		return nil, nil
	}
	if len(targetSurfaceIDs) == 0 {
		return nil, errors.New("target surface ids must not be empty")
	}
	if structure == nil {
		return nil, errors.New("structure state is nil")
	}
	targets := make(targetSurfaceSet, len(targetSurfaceIDs))
	for _, surfaceID := range targetSurfaceIDs {
		if surfaceID == "" {
			return nil, errors.New("target surface ids must not contain empty values")
		}
		if _, ok := structure.surfaceIndex[surfaceID]; !ok {
			return nil, fmt.Errorf("target surface id %q is unknown", surfaceID)
		}
		targets[surfaceID] = struct{}{}
	}
	return targets, nil
}

func (s targetSurfaceSet) contains(surfaceID string) bool {
	if s == nil {
		return true
	}
	_, ok := s[surfaceID]
	return ok
}
