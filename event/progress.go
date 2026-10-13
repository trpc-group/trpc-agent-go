//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

// ProgressExtensionKey stores a Progress snapshot on an event. It does not
// change the event's object type, invocation identity, or persistence policy.
const ProgressExtensionKey = "trpc_agent.progress"

// ProgressStatus describes the state of one operation, not the enclosing run.
// Consumers should tolerate unknown future statuses without treating them as
// successful completion.
type ProgressStatus string

const (
	// ProgressStatusStarted indicates that an operation is starting.
	ProgressStatusStarted ProgressStatus = "started"
	// ProgressStatusRunning reports an operation's current progress.
	ProgressStatusRunning ProgressStatus = "running"
	// ProgressStatusCompleted indicates successful completion.
	ProgressStatusCompleted ProgressStatus = "completed"
	// ProgressStatusSkipped indicates that an operation ended without a new result.
	ProgressStatusSkipped ProgressStatus = "skipped"
	// ProgressStatusFailed indicates an operation failure, which may be recoverable.
	ProgressStatusFailed ProgressStatus = "failed"
	// ProgressStatusCancelled indicates that an operation was cancelled.
	ProgressStatusCancelled ProgressStatus = "cancelled"
)

// Progress is a snapshot of one observable operation. Store it with
// SetExtension or WithExtension using ProgressExtensionKey, and read it with
// GetExtension. OperationID, Kind, and Status must be non-empty; the zero value
// is not a valid snapshot. Invalid extensions are ignored by StreamModeProgress
// and the built-in AG-UI progress adapter.
//
// Producers reuse OperationID across updates and allocate a new ID for each
// independent operation or retry. Event.ID identifies an individual notification;
// invocation and parent identity remain on Event. Snapshots may arrive without
// a started event, and cancellation or disconnection can prevent a terminal
// event from arriving. Consumers should reconcile unfinished operations with the
// enclosing run's outcome. There is no ordering guarantee between operations.
type Progress struct {
	// OperationID correlates snapshots of one operation and must be unique
	// within its invocation. It is not an event ID or an operation type.
	OperationID string `json:"operation_id"`
	// Kind identifies the operation for application selection and localization,
	// for example "context_compaction" or "graph_node".
	Kind string `json:"kind"`
	// Status describes the operation's current lifecycle state.
	Status ProgressStatus `json:"status"`
	// Percent is an optional cumulative percentage in [0, 100]. Nil means
	// unknown, while a pointer to zero means known zero progress. Reaching 100
	// does not imply successful completion; Status determines the outcome.
	Percent *float64 `json:"percent,omitempty"`
	// Message is an optional display hint. Applications should use Kind and
	// Status for logic and may replace Message with localized text.
	Message string `json:"message,omitempty"`
}
