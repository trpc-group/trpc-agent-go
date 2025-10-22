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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
)

// EvalSet represents a collection of evaluation cases.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalSet struct {
	// EvalSetID uniquely identifies this evaluation set.
	EvalSetID string `json:"eval_set_id,omitempty"`
	// Name of the evaluation set.
	Name string `json:"name,omitempty"`
	// Description of the evaluation set.
	Description string `json:"description,omitempty"`
	// EvalCases contains all the evaluation cases.
	EvalCases []*EvalCase `json:"eval_cases,omitempty"`
	// CreationTimestamp when this eval set was created.
	CreationTimestamp *epochtime.EpochTime `json:"creation_timestamp,omitempty"`
}

// Manager defines the interface that an evaluation set manager must satisfy.
type Manager interface {
	// Get gets an EvalSet identified by evalSetID.
	Get(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// Create creates an EvalSet identified by evalSetID.
	Create(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// List lists all EvalSet IDs for the given appName.
	List(ctx context.Context, appName string) ([]string, error)
	// Delete deletes EvalSet identified by evalSetID.
	Delete(ctx context.Context, appName, evalSetID string) error
	// GetCase gets an EvalCase identified by evalSetID and evalCaseID.
	GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*EvalCase, error)
	// AddCase adds an EvalCase to an existing EvalSet identified by evalSetID.
	AddCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// UpdateCase updates an EvalCase identified by evalSetID and evalCaseID.
	UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// DeleteCase deletes an EvalCase identified by evalSetID and evalCaseID.
	DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error
}
