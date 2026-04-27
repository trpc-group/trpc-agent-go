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

import coresubagent "trpc.group/trpc-go/trpc-agent-go/subagent"

var ErrRunNotFound = coresubagent.ErrRunNotFound

type Status = coresubagent.Status

const (
	StatusQueued    = coresubagent.StatusQueued
	StatusRunning   = coresubagent.StatusRunning
	StatusCompleted = coresubagent.StatusCompleted
	StatusFailed    = coresubagent.StatusFailed
	StatusCanceled  = coresubagent.StatusCanceled
)

type Run = coresubagent.Run

type ListFilter = coresubagent.ListFilter

type Service interface {
	ListForUser(userID string, filter ListFilter) []Run
	GetForUser(userID string, runID string) (*Run, error)
	CancelForUser(userID string, runID string) (*Run, bool, error)
}
