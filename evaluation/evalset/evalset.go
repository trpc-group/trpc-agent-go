//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalset provides evaluation set for evaluation.
package evalset

import (
	"context"
	"time"
)

// EvalSet represents a collection of evaluation cases.
type EvalSet struct {
	// EvalSetID uniquely identifies this evaluation set.
	EvalSetID string `json:"eval_set_id"`
	// Name of the evaluation set.
	Name string `json:"name,omitempty"`
	// Description of the evaluation set.
	Description string `json:"description,omitempty"`
	// EvalCases contains all the evaluation cases.
	EvalCases []EvalCase `json:"eval_cases"`
	// CreationTimestamp when this eval set was created.
	CreationTimestamp time.Time `json:"creation_timestamp"`
}

// Manager defines the interface for managing evaluation sets.
type Manager interface {
	// Get returns an EvalSet identified by evalSetID.
	Get(ctx context.Context, evalSetID string) (*EvalSet, error)
	// Create creates and returns an empty EvalSet given the evalSetID.
	Create(ctx context.Context, evalSetID string) (*EvalSet, error)
	// GetCase returns an EvalCase if found, otherwise nil.
	GetCase(ctx context.Context, evalSetID, evalCaseID string) (*EvalCase, error)
	// AddCase adds the given EvalCase to an existing EvalSet identified by evalSetID.
	AddCase(ctx context.Context, evalSetID string, evalCase *EvalCase) error
	// UpdateCase updates an existing EvalCase given the evalSetID.
	UpdateCase(ctx context.Context, evalSetID string, updatedEvalCase *EvalCase) error
	// DeleteCase deletes the given EvalCase identified by evalSetID and evalCaseID.
	DeleteCase(ctx context.Context, evalSetID, evalCaseID string) error
}
