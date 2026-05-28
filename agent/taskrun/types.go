//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package taskrun defines the control-plane API for persistent background
// task runs that execute agents through runner.Run.
//
// It is separate from runner.ManagedRunner, which controls only active
// runner invocations by request ID.
package taskrun

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// RuntimeStateKeyRun marks the current invocation as a task run.
	RuntimeStateKeyRun = "taskrun.run"
	// RuntimeStateKeyRunID stores the current task run id.
	RuntimeStateKeyRunID = "taskrun.run_id"
	// RuntimeStateKeyParentSessionID stores the parent session id.
	RuntimeStateKeyParentSessionID = "taskrun.parent_session_id"
)

// ErrRunNotFound indicates that a task run does not exist.
var ErrRunNotFound = errors.New("taskrun: run not found")

// ErrRunAlreadyExists indicates that a requested task run id already exists.
var ErrRunAlreadyExists = errors.New("taskrun: run already exists")

// ErrNotStarted indicates that a task run controller has not been started.
var ErrNotStarted = errors.New("taskrun: not started")

// Status describes the lifecycle state of a task run.
type Status string

const (
	// StatusQueued means the run was accepted but has not started yet.
	StatusQueued Status = "queued"
	// StatusRunning means the child agent is executing.
	StatusRunning Status = "running"
	// StatusFinalizing means the child agent exited and final metadata is
	// being attached.
	StatusFinalizing Status = "finalizing"
	// StatusCanceling means cancellation was requested and the child agent
	// has not exited yet.
	StatusCanceling Status = "canceling"
	// StatusCompleted means the child agent completed successfully.
	StatusCompleted Status = "completed"
	// StatusFailed means the child agent failed.
	StatusFailed Status = "failed"
	// StatusCanceled means the child agent exited after cancellation.
	StatusCanceled Status = "canceled"
)

// Run is the persisted control-plane view of one delegated task run.
type Run struct {
	ID              string            `json:"id,omitempty"`
	OwnerUserID     string            `json:"owner_user_id,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	ParentAppName   string            `json:"parent_app_name,omitempty"`
	AppName         string            `json:"app_name,omitempty"`
	ChildSessionID  string            `json:"child_session_id,omitempty"`
	RequestID       string            `json:"request_id,omitempty"`
	AgentName       string            `json:"agent_name,omitempty"`
	Task            string            `json:"task,omitempty"`
	Status          Status            `json:"status,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	Result          string            `json:"result,omitempty"`
	Error           string            `json:"error,omitempty"`
	Progress        *Progress         `json:"progress,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	StartedAt       *time.Time        `json:"started_at,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
}

// Progress is a lightweight, best-effort view of events observed for a run.
//
// Full task transcripts remain in the child session identified by
// Run.ChildSessionID. Progress intentionally stores only small counters that
// are useful for polling and status displays.
type Progress struct {
	EventCount       int        `json:"event_count,omitempty"`
	ToolCallCount    int        `json:"tool_call_count,omitempty"`
	ToolResultCount  int        `json:"tool_result_count,omitempty"`
	PromptTokens     int        `json:"prompt_tokens,omitempty"`
	CompletionTokens int        `json:"completion_tokens,omitempty"`
	TotalTokens      int        `json:"total_tokens,omitempty"`
	LastEventAt      *time.Time `json:"last_event_at,omitempty"`
}

// ListFilter limits the runs returned by List.
type ListFilter struct {
	OwnerUserID     string
	ParentSessionID string
	ParentAppName   string
	Status          Status
}

// RuntimeStateKeys configures the runtime-state keys injected into the child
// runner invocation. Zero value uses the taskrun defaults.
type RuntimeStateKeys struct {
	Run             string
	RunID           string
	ParentSessionID string
}

// SpawnRequest describes a new delegated task run.
type SpawnRequest struct {
	// ID is optional. When empty, the controller assigns one.
	ID              string
	OwnerUserID     string
	ParentSessionID string
	ParentAppName   string
	AppName         string
	ChildSessionID  string
	RequestID       string
	AgentName       string
	Task            string
	Timeout         time.Duration
	// RuntimeState is local runner state for implementations that call
	// runner.Run directly. It is not a cross-node serialization contract.
	RuntimeState map[string]any
	// RunOptions are local runner options for implementations that call
	// runner.Run directly. They are not a cross-node serialization contract.
	RunOptions []agent.RunOption
	// RunContext adds local context values for implementations that call
	// runner.Run directly. It is not a cross-node serialization contract.
	RunContext func(context.Context) context.Context
	// RuntimeStateKeys overrides the keys injected by implementations that
	// call runner.Run directly. Zero value uses the taskrun defaults.
	RuntimeStateKeys RuntimeStateKeys
	// InjectedContextMessages are local runner context messages for
	// implementations that call runner.Run directly.
	InjectedContextMessages []model.Message
	Metadata                map[string]string
}

// Controller is the control-plane API exposed by a task run runtime.
type Controller interface {
	Spawn(ctx context.Context, req SpawnRequest) (Run, error)
	List(ctx context.Context, filter ListFilter) ([]Run, error)
	Get(ctx context.Context, runID string) (*Run, error)
	Cancel(ctx context.Context, runID string) (*Run, bool, error)
	Wait(ctx context.Context, runID string) (*Run, error)
}

// Observer receives lifecycle updates after they have been persisted.
type Observer interface {
	OnRunUpdate(ctx context.Context, run Run)
}

// ObserverFunc adapts a function into an Observer.
type ObserverFunc func(ctx context.Context, run Run)

// OnRunUpdate implements Observer.
func (f ObserverFunc) OnRunUpdate(ctx context.Context, run Run) {
	if f != nil {
		f(ctx, run)
	}
}

// IsTerminal reports whether the status will no longer change under normal
// execution.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}
