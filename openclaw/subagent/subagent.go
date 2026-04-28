//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package subagent exposes the public control-plane view for OpenClaw
// background subagents.
package subagent

import (
	"errors"
	"time"
)

var ErrRunNotFound = errors.New("subagent: run not found")

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

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

type ListFilter struct {
	ParentSessionID string
}

type Service interface {
	ListForUser(userID string, filter ListFilter) []Run
	GetForUser(userID string, runID string) (*Run, error)
	CancelForUser(
		userID string,
		runID string,
	) (*Run, bool, error)
}

func (s Status) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}
