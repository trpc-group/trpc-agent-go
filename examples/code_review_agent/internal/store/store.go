//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// InputRecord stores a redacted diff input and summary.
type InputRecord struct {
	TaskID           string
	DiffSummary      string
	ChangedFilesJSON string
	RedactedDiff     string
}

// ReportRecord stores generated report locations and metrics.
type ReportRecord struct {
	TaskID       string
	JSONPath     string
	MarkdownPath string
	Conclusion   string
	MetricsJSON  string
}

// TaskReport is the query shape used by reports and tests.
type TaskReport struct {
	Task                review.ReviewTask
	Input               InputRecord
	Findings            []review.Finding
	SandboxRuns         []review.SandboxRun
	PermissionDecisions []review.PermissionDecisionRecord
	Artifacts           []review.ArtifactRecord
	Report              ReportRecord
}

// Store isolates review orchestration from a concrete SQL backend.
type Store interface {
	Close() error
	CreateTask(ctx context.Context, task review.ReviewTask) error
	FinishTask(ctx context.Context, taskID string, status string, errText string, finishedAt time.Time) error
	RecordInput(ctx context.Context, input InputRecord) error
	RecordSandboxRun(ctx context.Context, run review.SandboxRun) error
	RecordPermissionDecision(ctx context.Context, decision review.PermissionDecisionRecord) error
	SaveFindings(ctx context.Context, taskID string, findings []review.Finding) error
	SaveArtifacts(ctx context.Context, artifacts []review.ArtifactRecord) error
	SaveReport(ctx context.Context, report ReportRecord) error
	LoadTaskReport(ctx context.Context, taskID string) (TaskReport, error)
}
