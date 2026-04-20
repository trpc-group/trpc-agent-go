//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter exposes HTTP payload contracts for PromptIter control APIs.
//
// The package bridges external callers and internal workflow types by translating
// request and response shapes used by run and structure operations.
package promptiter

import (
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	engine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// RunRequest carries PromptIter run input from management endpoints.
type RunRequest struct {
	// Run is the workflow configuration submitted to trigger an optimization run.
	Run *engine.RunRequest `json:"run"`
}

// RunResponse returns the result of a completed PromptIter run request.
type RunResponse struct {
	// Result is the run output produced by the engine orchestration.
	Result *engine.RunResult `json:"result"`
}

// GetStructureResponse returns a structure snapshot for an optimization target.
type GetStructureResponse struct {
	// Structure is the target structure snapshot shared with clients.
	Structure *astructure.Snapshot `json:"structure"`
}
