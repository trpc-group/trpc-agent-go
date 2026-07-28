//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finding defines the core types for code review findings, tasks, and reports.
package finding

import "time"

// Severity defines the severity level of a code review finding.
type Severity string

const (
	// SeverityCritical indicates a critical issue that must be fixed.
	SeverityCritical Severity = "critical"
	// SeverityHigh indicates a high-severity issue that should be fixed.
	SeverityHigh Severity = "high"
	// SeverityMedium indicates a medium-severity issue.
	SeverityMedium Severity = "medium"
	// SeverityLow indicates a low-severity issue.
	SeverityLow Severity = "low"
	// SeverityWarning indicates a warning-level issue (low confidence).
	SeverityWarning Severity = "warning"
	// SeverityInfo indicates an informational observation.
	SeverityInfo Severity = "info"
)

// Category defines the category of a code review finding.
type Category string

const (
	// CategorySecurity indicates a security-related issue.
	CategorySecurity Category = "security"
	// CategoryGoroutineLeak indicates a goroutine or context leak issue.
	CategoryGoroutineLeak Category = "goroutine_leak"
	// CategoryResourceLeak indicates a resource leak issue.
	CategoryResourceLeak Category = "resource_leak"
	// CategoryErrorHandling indicates an error handling issue.
	CategoryErrorHandling Category = "error_handling"
	// CategoryMissingTest indicates a missing test issue.
	CategoryMissingTest Category = "missing_test"
	// CategoryDBLifecycle indicates a database transaction or connection lifecycle issue.
	CategoryDBLifecycle Category = "db_lifecycle"
	// CategorySensitiveInfo indicates a sensitive information leak issue.
	CategorySensitiveInfo Category = "sensitive_info"
	// CategoryBestPractice indicates a best practice violation.
	CategoryBestPractice Category = "best_practice"
)

// Confidence defines the confidence level of a finding.
type Confidence string

const (
	// ConfidenceHigh indicates high confidence in the finding.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium indicates medium confidence.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow indicates low confidence; should enter warnings.
	ConfidenceLow Confidence = "low"
)

// Source defines the origin of a finding.
type Source string

const (
	// SourceGoVet indicates the finding comes from go vet.
	SourceGoVet Source = "go_vet"
	// SourceStaticcheck indicates the finding comes from staticcheck.
	SourceStaticcheck Source = "staticcheck"
	// SourceGosec indicates the finding comes from gosec.
	SourceGosec Source = "gosec"
	// SourceCustomRule indicates the finding comes from a custom rule.
	SourceCustomRule Source = "custom_rule"
	// SourceDiffPattern indicates the finding comes from diff pattern matching.
	SourceDiffPattern Source = "diff_pattern"
	// SourceLLM indicates the finding comes from LLM review.
	SourceLLM Source = "llm_review"
)

// Finding is a structured representation of a code review finding.
type Finding struct {
	ID             string     `json:"id"`
	Severity       Severity   `json:"severity"`
	Category       Category   `json:"category"`
	File           string     `json:"file"`
	Line           int        `json:"line"`
	Column         int        `json:"column,omitempty"`
	Title          string     `json:"title"`
	Evidence       string     `json:"evidence"`
	Recommendation string     `json:"recommendation"`
	Confidence     Confidence `json:"confidence"`
	Source         Source     `json:"source"`
	RuleID         string     `json:"rule_id"`
	HunkID         string     `json:"hunk_id,omitempty"`
	IsDuplicate    bool       `json:"is_duplicate,omitempty"`
	IsWarning      bool       `json:"is_warning,omitempty"`
	Sanitized      bool       `json:"sanitized,omitempty"`
}

// ChangedFileInfo describes a single changed file in a diff.
type ChangedFileInfo struct {
	File       string `json:"file"`
	Status     string `json:"status"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	Package    string `json:"package,omitempty"`
	IsTestFile bool   `json:"is_test_file"`
}

// ReviewTask represents a complete code review task.
type ReviewTask struct {
	ID                string            `json:"id"`
	DiffSource        string            `json:"diff_source"`
	DiffSummary       string            `json:"diff_summary"`
	ChangedFiles      []ChangedFileInfo `json:"changed_files"`
	Status            string            `json:"status"`
	FindingCount      int               `json:"finding_count"`
	HighRiskCount     int               `json:"high_risk_count"`
	MediumRiskCount   int               `json:"medium_risk_count"`
	LowRiskCount      int               `json:"low_risk_count"`
	WarningCount      int               `json:"warning_count"`
	PermissionDenied  int               `json:"permission_denied"`
	PermissionAsked   int               `json:"permission_asked"`
	TotalDurationMs   int64             `json:"total_duration_ms"`
	SandboxDurationMs int64             `json:"sandbox_duration_ms"`
	ToolCallCount     int               `json:"tool_call_count"`
	DryRun            bool              `json:"dry_run"`
	ReportJSON        string            `json:"report_json,omitempty"`
	ReportMD          string            `json:"report_md,omitempty"`
	Error             string            `json:"error,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// SandboxRun represents a single sandbox execution record.
type SandboxRun struct {
	ID               string    `json:"id"`
	TaskID           string    `json:"task_id"`
	Backend          string    `json:"backend"`
	Command          string    `json:"command"`
	ExitCode         int       `json:"exit_code"`
	StdoutSummary    string    `json:"stdout_summary"`
	StderrSummary    string    `json:"stderr_summary"`
	DurationMs       int64     `json:"duration_ms"`
	Timeout          bool      `json:"timeout"`
	PermissionAction string    `json:"permission_action"`
	ArtifactIDs      []string  `json:"artifact_ids,omitempty"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// PermissionDecision represents a permission check decision record.
type PermissionDecision struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id"`
	ToolName     string    `json:"tool_name"`
	Command      string    `json:"command"`
	SanitizedCmd string    `json:"sanitized_cmd"`
	Decision     string    `json:"decision"`
	Reason       string    `json:"reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReviewReport is the final output of a code review.
type ReviewReport struct {
	TaskID          string                      `json:"task_id"`
	DiffSummary     string                      `json:"diff_summary"`
	Findings        []Finding                   `json:"findings"`
	Warnings        []Finding                   `json:"warnings"`
	RiskSummary     RiskSummary                 `json:"risk_summary"`
	PermissionLog   []PermissionDecisionSummary `json:"permission_log"`
	SandboxSummary  SandboxSummary              `json:"sandbox_summary"`
	Monitoring      MonitoringSummary           `json:"monitoring"`
	Recommendations []string                    `json:"recommendations"`
	GeneratedAt     time.Time                   `json:"generated_at"`
}

// PermissionDecisionSummary is a summary entry for the permission log in the report.
type PermissionDecisionSummary struct {
	ToolName string `json:"tool_name"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// RiskSummary aggregates risk statistics.
type RiskSummary struct {
	Total      int              `json:"total"`
	BySeverity map[Severity]int `json:"by_severity"`
	ByCategory map[Category]int `json:"by_category"`
	NeedReview int              `json:"need_human_review"`
}

// SandboxSummary aggregates sandbox execution statistics.
type SandboxSummary struct {
	TotalRuns       int   `json:"total_runs"`
	Succeeded       int   `json:"succeeded"`
	Failed          int   `json:"failed"`
	TimedOut        int   `json:"timed_out"`
	TotalDurationMs int64 `json:"total_duration_ms"`
}

// MonitoringSummary contains monitoring metrics for a review task.
type MonitoringSummary struct {
	TotalDurationMs       int64            `json:"total_duration_ms"`
	SandboxDurationMs     int64            `json:"sandbox_duration_ms"`
	ToolCallCount         int              `json:"tool_call_count"`
	PermissionDenied      int              `json:"permission_denied"`
	PermissionAsked       int              `json:"permission_asked"`
	FindingCount          int              `json:"finding_count"`
	WarningCount          int              `json:"warning_count"`
	SeverityDist          map[Severity]int `json:"severity_distribution"`
	ErrorCount            int              `json:"error_count"`
	ErrorTypeDistribution map[string]int   `json:"error_type_distribution,omitempty"`
}
