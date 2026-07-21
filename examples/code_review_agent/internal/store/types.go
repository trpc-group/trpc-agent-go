//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store

import "time"

// ReviewTaskRecord is the lifecycle root and the lookup bridge to the complete
// framework Session key.
type ReviewTaskRecord struct {
	TaskID                string
	AppName               string
	UserID                string
	Status                string
	InputKind             string
	InputSummaryJSON      string
	InputArtifactName     string
	InputArtifactVersion  *int
	MonitoringSummaryJSON string
	Conclusion            string
	JSONReportName        string
	JSONReportVersion     *int
	MarkdownReportName    string
	MarkdownReportVersion *int
	StartedAt             time.Time
	FinishedAt            time.Time
	ErrorType             string
	ErrorMessage          string
}

// TaskInputRecord is the input projection produced after parsing, masking, and
// saving the complete masked diff artifact. Keeping this update separate from
// SaveTask prevents input preparation from accidentally overwriting lifecycle
// fields that were established when the task started.
type TaskInputRecord struct {
	InputKind            string
	InputSummaryJSON     string
	InputArtifactName    string
	InputArtifactVersion int
}

// PermissionDecisionRecord records a system governance decision made before
// an operation executes.
type PermissionDecisionRecord struct {
	ToolCallID     string
	DecisionKind   string
	Operation      string
	ToolName       string
	CommandPreview string
	Decision       string
	Reason         string
	DecidedAt      time.Time
}

// SandboxRunRecord records facts observed by the governed execution wrapper.
type SandboxRunRecord struct {
	ToolCallID         string
	Backend            string
	Workdir            string
	CommandPreview     string
	EnvAllowlistJSON   string
	Timeout            time.Duration
	OutputLimitBytes   int64
	ArtifactLimitBytes int64
	Status             string
	ExitCode           *int
	TimedOut           bool
	StdoutSummary      string
	StderrSummary      string
	StdoutTruncated    bool
	StderrTruncated    bool
	RedactionCount     int
	StartedAt          time.Time
	FinishedAt         time.Time
	Duration           time.Duration
	ErrorType          string
	ErrorMessage       string
}

// ReviewResultRecord is a finding, warning, or human-review item submitted by
// the Agent through submit_review_results.
type ReviewResultRecord struct {
	ResultKind     string
	Severity       string
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string
	RuleID         string
	CreatedAt      time.Time
}

// ReviewResultCounts reports the committed result projection by result kind.
type ReviewResultCounts struct {
	FindingCount     int
	WarningCount     int
	HumanReviewCount int
}

// ReportReferences identifies the JSON and Markdown report artifacts generated
// from one stored task snapshot.
type ReportReferences struct {
	JSONName        string
	JSONVersion     int
	MarkdownName    string
	MarkdownVersion int
}

// TaskFinalization is the only write that moves a task out of running. Keeping
// status, monitoring, error classification, and report references together
// prevents readers from observing a completed task with a partial projection.
type TaskFinalization struct {
	Status                string
	MonitoringSummaryJSON string
	Reports               *ReportReferences
	FinishedAt            time.Time
	ErrorType             string
	ErrorMessage          string
}

// ReviewSnapshot is the complete task-scoped projection used to build reports
// and monitoring summaries. Framework Session events remain in Session Service.
type ReviewSnapshot struct {
	Task                ReviewTaskRecord
	PermissionDecisions []PermissionDecisionRecord
	SandboxRuns         []SandboxRunRecord
	Results             []ReviewResultRecord
}
