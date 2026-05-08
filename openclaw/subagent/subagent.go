//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package subagent exposes the OpenClaw subagent control-plane view.
package subagent

import (
	"errors"
	"time"
)

const (
	// RuntimeStateKeyRun marks an OpenClaw subagent invocation.
	RuntimeStateKeyRun = "openclaw.subagent.run"
	// RuntimeStateKeyRunID stores the OpenClaw subagent run id.
	RuntimeStateKeyRunID = "openclaw.subagent.run_id"
	// RuntimeStateKeyParentSessionID stores the parent session id.
	RuntimeStateKeyParentSessionID = "openclaw.subagent.parent_session_id"
)

// ErrRunNotFound indicates that an OpenClaw subagent run does not exist.
var ErrRunNotFound = errors.New("subagent: run not found")

// ErrRunAlreadyExists indicates that a requested subagent run id exists.
var ErrRunAlreadyExists = errors.New("subagent: run already exists")

// ErrNotStarted indicates that the subagent service has not been started.
var ErrNotStarted = errors.New("subagent: not started")

// Status describes the lifecycle state of an OpenClaw subagent run.
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

// Run is the OpenClaw product-facing view of one subagent run.
type Run struct {
	ID              string     `json:"id,omitempty"`
	ParentSessionID string     `json:"parent_session_id,omitempty"`
	ChildSessionID  string     `json:"child_session_id,omitempty"`
	Task            string     `json:"task,omitempty"`
	Status          Status     `json:"status,omitempty"`
	Summary         string     `json:"summary,omitempty"`
	Result          string     `json:"result,omitempty"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// ListFilter limits the subagent runs returned by ListForUser.
type ListFilter struct {
	ParentSessionID string
	Status          Status
}

type Service interface {
	ListForUser(userID string, filter ListFilter) []Run
	GetForUser(userID string, runID string) (*Run, error)
	CancelForUser(userID string, runID string) (*Run, bool, error)
}
