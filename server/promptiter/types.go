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
// request and response shapes used by run, structure, and trace reporter
// operations.
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

// TraceReporterConfig controls optional trace reporting to server endpoints.
type TraceReporterConfig struct {
	// Enabled enables or disables trace reporter emission.
	Enabled bool `json:"enabled"`
	// SampleRate defines the fraction of traces to report when enabled.
	SampleRate float64 `json:"sample_rate"`
}

// GetTraceReporterConfigResponse returns the current trace reporter config.
type GetTraceReporterConfigResponse struct {
	// Config stores effective trace reporter settings from runtime.
	Config *TraceReporterConfig `json:"config"`
}

// PutTraceReporterConfigRequest carries trace reporter settings update input.
type PutTraceReporterConfigRequest struct {
	// Config is the target trace reporter configuration.
	Config *TraceReporterConfig `json:"config"`
}

// PutTraceReporterConfigResponse returns trace reporter settings after update.
type PutTraceReporterConfigResponse struct {
	// Config stores the persisted trace reporter configuration.
	Config *TraceReporterConfig `json:"config"`
}
