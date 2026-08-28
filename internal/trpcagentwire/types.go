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
	// ExpectedRequestID is encoded as expectedRequestID and must identify the
	// current persisted head request. The empty value is invalid.
	ExpectedRequestID string `json:"expectedRequestID"`
}

// RunOptions contains the shared client-server options for one remote run.
type RunOptions struct {
	// RequestID is encoded as requestID and identifies the new run. The server
	// generates a value when it is empty, except that replacement requires the
	// caller to provide a non-empty value distinct from ExpectedRequestID.
	RequestID string `json:"requestID,omitempty"`
	// ExecutionTraceEnabled is encoded as executionTraceEnabled and requests an
	// execution trace when true. Its zero value disables tracing.
	ExecutionTraceEnabled bool `json:"executionTraceEnabled,omitempty"`
	// RuntimeState is encoded as runtimeState and carries JSON-compatible values
	// merged into the run state. Graph command values are restored by the server
	// after JSON decoding; resume state cannot be combined with replacement.
	RuntimeState map[string]any `json:"runtimeState,omitempty"`
	// LatestTurnReplacement is encoded as latestTurnReplacement. A nil value
	// disables replacement; a non-nil value requires a valid ExpectedRequestID.
	LatestTurnReplacement *LatestTurnReplacement `json:"latestTurnReplacement,omitempty"`
}

// RunResponse contains the shared response payload for one remote run.
type RunResponse struct {
	// Status is encoded as status and reports the terminal execution status.
	Status atrace.TraceStatus `json:"status"`
	// Events is encoded as events and contains the collected run events. The
	// field is omitted when no events are returned.
	Events []event.Event `json:"events,omitempty"`
	// Messages is encoded as messages and contains the canonical run messages.
	// The field is omitted when no messages are returned.
	Messages []model.Message `json:"messages,omitempty"`
	// ExecutionTrace is encoded as executionTrace and is nil when tracing was
	// not requested or no trace was produced.
	ExecutionTrace *atrace.Trace `json:"executionTrace,omitempty"`
	// ErrorMessage is encoded as errorMessage and is empty for successful runs.
	ErrorMessage string `json:"errorMessage,omitempty"`
	// DirectRunError is encoded as directRunError and reports whether execution
	// failed before an event stream was established.
	DirectRunError bool `json:"directRunError,omitempty"`
	// DirectRunErrorKind is encoded as directRunErrorKind and identifies a
	// recognized direct-run error. Its empty value means no recognized kind.
	DirectRunErrorKind DirectRunErrorKind `json:"directRunErrorKind,omitempty"`
}
