//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package inprocess provides a single-process taskrun controller.
//
// It starts task runs in goroutines and stores active cancel/wait state in
// memory. Distributed deployments should provide their own taskrun.Controller
// backed by external storage, queueing, and leases.
package inprocess

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
)

const (
	// RuntimeStateKeyRun marks the current invocation as a task run.
	RuntimeStateKeyRun = taskrun.RuntimeStateKeyRun
	// RuntimeStateKeyRunID stores the current task run id.
	RuntimeStateKeyRunID = taskrun.RuntimeStateKeyRunID
	// RuntimeStateKeyParentSessionID stores the parent session id.
	RuntimeStateKeyParentSessionID = taskrun.RuntimeStateKeyParentSessionID
)

const (
	// StatusQueued means the run was accepted but has not started yet.
	StatusQueued = taskrun.StatusQueued
	// StatusRunning means the child agent is executing.
	StatusRunning = taskrun.StatusRunning
	// StatusCompleted means the child agent completed successfully.
	StatusCompleted = taskrun.StatusCompleted
	// StatusFailed means the child agent failed.
	StatusFailed = taskrun.StatusFailed
	// StatusCanceled means cancellation was requested or observed.
	StatusCanceled = taskrun.StatusCanceled
)

const (
	defaultStoredResultRunes  = 4000
	defaultStoredSummaryRunes = 240

	statusCanceledSummary = "canceled"
)

// ErrRunNotFound indicates that a task run does not exist.
var ErrRunNotFound = taskrun.ErrRunNotFound

// ErrRunAlreadyExists indicates that a requested task run id already exists.
var ErrRunAlreadyExists = taskrun.ErrRunAlreadyExists

// ErrNotStarted indicates that a task run service has not been started.
var ErrNotStarted = taskrun.ErrNotStarted

// Run is the persisted control-plane view of one delegated task run.
type Run = taskrun.Run

// Progress is the lightweight event progress for one delegated task run.
type Progress = taskrun.Progress

// Status describes the lifecycle state of a task run.
type Status = taskrun.Status

// SpawnRequest describes a new delegated task run.
type SpawnRequest = taskrun.SpawnRequest

// ListFilter limits the runs returned by List.
type ListFilter = taskrun.ListFilter

// RuntimeStateKeys configures runtime-state keys for child runner calls.
type RuntimeStateKeys = taskrun.RuntimeStateKeys

// Observer receives lifecycle updates after they have been persisted.
type Observer = taskrun.Observer

// ObserverFunc adapts a function into an Observer.
type ObserverFunc = taskrun.ObserverFunc

func cloneRun(r Run) Run {
	out := r
	if r.StartedAt != nil {
		startedAt := *r.StartedAt
		out.StartedAt = &startedAt
	}
	if r.FinishedAt != nil {
		finishedAt := *r.FinishedAt
		out.FinishedAt = &finishedAt
	}
	if r.Progress != nil {
		out.Progress = cloneProgress(r.Progress)
	}
	if r.Metadata != nil {
		out.Metadata = make(map[string]string, len(r.Metadata))
		for key, value := range r.Metadata {
			out.Metadata[key] = value
		}
	}
	return out
}

func cloneProgress(progress *Progress) *Progress {
	if progress == nil {
		return nil
	}
	out := *progress
	if progress.LastEventAt != nil {
		lastEventAt := *progress.LastEventAt
		out.LastEventAt = &lastEventAt
	}
	return &out
}

func cloneRuns(runs []Run) []Run {
	out := make([]Run, 0, len(runs))
	for _, run := range runs {
		if run.ID == "" {
			continue
		}
		out = append(out, cloneRun(run))
	}
	return out
}

func cloneTime(value time.Time) *time.Time {
	copied := value
	return &copied
}
