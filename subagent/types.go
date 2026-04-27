//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package subagent provides a generic runtime for dynamic background
// agent runs.
package subagent

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// RuntimeStateKeyRun marks the current invocation as a subagent run.
	RuntimeStateKeyRun = "subagent.run"
	// RuntimeStateKeyRunID stores the current subagent run id.
	RuntimeStateKeyRunID = "subagent.run_id"
	// RuntimeStateKeyParentSessionID stores the parent session id.
	RuntimeStateKeyParentSessionID = "subagent.parent_session_id"
)

const (
	defaultStoredResultRunes  = 4000
	defaultStoredSummaryRunes = 240
)

const (
	statusCanceledSummary = "canceled"
)

// ErrRunNotFound indicates that a subagent run does not exist.
var ErrRunNotFound = errors.New("subagent: run not found")

// Status describes the lifecycle state of a subagent run.
type Status string

const (
	// StatusQueued means the run was accepted but has not started yet.
	StatusQueued Status = "queued"
	// StatusRunning means the child agent is executing.
	StatusRunning Status = "running"
	// StatusCompleted means the child agent completed successfully.
	StatusCompleted Status = "completed"
	// StatusFailed means the child agent failed.
	StatusFailed Status = "failed"
	// StatusCanceled means cancellation was requested or observed.
	StatusCanceled Status = "canceled"
)

// Run is the persisted control-plane view of one dynamic subagent run.
type Run struct {
	ID              string            `json:"id,omitempty"`
	OwnerUserID     string            `json:"owner_user_id,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	ChildSessionID  string            `json:"child_session_id,omitempty"`
	RequestID       string            `json:"request_id,omitempty"`
	AgentName       string            `json:"agent_name,omitempty"`
	Task            string            `json:"task,omitempty"`
	Status          Status            `json:"status,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	Result          string            `json:"result,omitempty"`
	Error           string            `json:"error,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	StartedAt       *time.Time        `json:"started_at,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
}

// ListFilter limits the runs returned by List.
type ListFilter struct {
	OwnerUserID     string
	ParentSessionID string
	Status          Status
}

// SpawnRequest describes a new dynamic subagent run.
type SpawnRequest struct {
	OwnerUserID             string
	ParentSessionID         string
	ChildSessionID          string
	RequestID               string
	AgentName               string
	Task                    string
	Timeout                 time.Duration
	RuntimeState            map[string]any
	InjectedContextMessages []model.Message
	Metadata                map[string]string
}

// Controller is the control-plane API exposed by a subagent runtime.
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

func (r Run) clone() Run {
	out := r
	if r.StartedAt != nil {
		startedAt := *r.StartedAt
		out.StartedAt = &startedAt
	}
	if r.FinishedAt != nil {
		finishedAt := *r.FinishedAt
		out.FinishedAt = &finishedAt
	}
	if r.Metadata != nil {
		out.Metadata = make(map[string]string, len(r.Metadata))
		for key, value := range r.Metadata {
			out.Metadata[key] = value
		}
	}
	return out
}

func cloneRuns(runs []Run) []Run {
	out := make([]Run, 0, len(runs))
	for _, run := range runs {
		if run.ID == "" {
			continue
		}
		out = append(out, run.clone())
	}
	return out
}

func cloneTime(value time.Time) *time.Time {
	copied := value
	return &copied
}
