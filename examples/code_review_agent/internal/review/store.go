//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"context"
	"time"
)

// ReviewStore defines the persistence operations required by the review
// orchestrator.
type ReviewStore interface {
	CreateTask(context.Context, Task) error
	TransitionPhase(context.Context, string, Phase, time.Time) error
	RecordSandboxRun(context.Context, SandboxRun) error
	RecordGovernanceDecision(context.Context, GovernanceDecision) error
	FailTask(context.Context, string, Phase, string, time.Time) error
	CancelTask(context.Context, string, Phase, string, time.Time) error
	CompleteTask(context.Context, Completion) error
	GetReview(context.Context, string) (StoredReview, error)
}

// Completion contains the records committed atomically when a review succeeds.
type Completion struct {
	TaskID    string
	UpdatedAt time.Time
	Input     ReviewInput
	Findings  []Finding
	// Artifacts contains semantic evidence included in the canonical report.
	Artifacts []ArtifactRecord
	// PublicationArtifacts contains the JSON and Markdown records created by
	// publishing the canonical report. They are not part of the report itself.
	PublicationArtifacts []ArtifactRecord
	Metrics              Metrics
	Report               ReportMetadata
	Conclusion           string
}

// ReportMetadata identifies the canonical report and its published artifacts.
type ReportMetadata struct {
	SchemaVersion             string
	TaskID                    string
	Digest                    string
	JSONArtifactReference     string
	MarkdownArtifactReference string
}

// StoredReview contains the reconstructed domain report and persisted report
// metadata.
type StoredReview struct {
	Report               Report
	PublicationArtifacts []ArtifactRecord
	Metadata             ReportMetadata
}
