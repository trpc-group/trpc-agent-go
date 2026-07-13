//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package storage persists review tasks and findings.
package storage

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

// ArtifactRecord stores a generated report artifact.
type ArtifactRecord struct {
	ID      string
	TaskID  string
	Name    string
	Content string
}

// PermissionRecord stores a permission gate decision.
type PermissionRecord struct {
	ID       string
	TaskID   string
	ToolName string
	Command  string
	Action   string
	Reason   string
}

// SandboxRunRecord stores a sandbox execution attempt.
type SandboxRunRecord struct {
	ID         string
	TaskID     string
	Command    string
	Runtime    string
	Status     string
	ExitCode   int
	DurationMs int
	Stdout     string
	Stderr     string
	ErrorType  string
}

// ReviewRecord is the persisted review snapshot.
type ReviewRecord struct {
	TaskID              string
	Status              string
	InputSummary        string
	RepoPath            string
	CreatedAt           time.Time
	FinishedAt          time.Time
	DurationMs          int
	Findings            []findings.Finding
	Warnings            []findings.Finding
	Metrics             findings.ReviewMetrics
	Artifacts           []ArtifactRecord
	PermissionDecisions []PermissionRecord
	SandboxRuns         []SandboxRunRecord
}

// Store persists and retrieves review records.
type Store interface {
	Init(ctx context.Context) error
	SaveReview(ctx context.Context, review *ReviewRecord) error
	GetReview(ctx context.Context, taskID string) (*ReviewRecord, error)
	Close() error
}
