//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagentwire

import (
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// LatestTurnReplacement identifies the turn a remote run may replace.
type LatestTurnReplacement struct {
	ExpectedRequestID string `json:"expectedRequestID"`
}

// RunOptions contains the shared client-server options for one remote run.
type RunOptions struct {
	RequestID             string                 `json:"requestID,omitempty"`
	ExecutionTraceEnabled bool                   `json:"executionTraceEnabled,omitempty"`
	RuntimeState          map[string]any         `json:"runtimeState,omitempty"`
	LatestTurnReplacement *LatestTurnReplacement `json:"latestTurnReplacement,omitempty"`
}

// RunResponse contains the shared response payload for one remote run.
type RunResponse struct {
	Status             atrace.TraceStatus `json:"status"`
	Events             []event.Event      `json:"events,omitempty"`
	Messages           []model.Message    `json:"messages,omitempty"`
	ExecutionTrace     *atrace.Trace      `json:"executionTrace,omitempty"`
	ErrorMessage       string             `json:"errorMessage,omitempty"`
	DirectRunError     bool               `json:"directRunError,omitempty"`
	DirectRunErrorKind DirectRunErrorKind `json:"directRunErrorKind,omitempty"`
}
