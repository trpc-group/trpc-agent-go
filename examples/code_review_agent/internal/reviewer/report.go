//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

const (
	jsonReportName     = "review_report.json"
	markdownReportName = "review_report.md"
)

// ReviewOutcome exposes the same reports that are persisted through Artifact
// Service so a CLI caller can materialize them without rebuilding another
// representation.
type ReviewOutcome struct {
	TaskID         string
	JSONReport     []byte
	MarkdownReport []byte
	References     store.ReportReferences
}

type monitoringSummary struct {
	TotalDurationMS             int64          `json:"total_duration_ms"`
	SandboxDurationMS           int64          `json:"sandbox_duration_ms"`
	ToolCallCount               int            `json:"tool_call_count"`
	PermissionInterceptionCount int            `json:"permission_interception_count"`
	FindingCount                int            `json:"finding_count"`
	ResultKindDistribution      map[string]int `json:"result_kind_distribution"`
	SeverityDistribution        map[string]int `json:"severity_distribution"`
	ExceptionDistribution       map[string]int `json:"exception_distribution"`
}

type reportDocument struct {
	SchemaVersion string               `json:"schema_version"`
	Task          reportTask           `json:"task"`
	Input         reportInput          `json:"input"`
	Monitoring    monitoringSummary    `json:"monitoring"`
	Permissions   []reportPermission   `json:"permission_decisions"`
	SandboxRuns   []reportSandboxRun   `json:"sandbox_runs"`
	Findings      []reportReviewResult `json:"findings"`
	Warnings      []reportReviewResult `json:"warnings"`
	HumanReview   []reportReviewResult `json:"needs_human_review"`
	Conclusion    string               `json:"conclusion"`
}

type reportTask struct {
	TaskID       string `json:"task_id"`
	AppName      string `json:"app_name"`
	UserID       string `json:"user_id"`
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	ErrorType    string `json:"error_type,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type reportInput struct {
	Kind            string          `json:"kind"`
	Summary         json.RawMessage `json:"summary"`
	ArtifactName    string          `json:"artifact_name"`
	ArtifactVersion *int            `json:"artifact_version"`
}

type reportPermission struct {
	ToolCallID     string `json:"tool_call_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	Operation      string `json:"operation"`
	CommandPreview string `json:"command_preview,omitempty"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
	DecidedAt      string `json:"decided_at"`
}

type reportSandboxRun struct {
	ToolCallID       string `json:"tool_call_id,omitempty"`
	Backend          string `json:"backend"`
	CommandPreview   string `json:"command_preview"`
	Workdir          string `json:"workdir,omitempty"`
	TimeoutMS        int64  `json:"timeout_ms"`
	OutputLimitBytes int64  `json:"output_limit_bytes"`
	Status           string `json:"status"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	TimedOut         bool   `json:"timed_out"`
	OutputSummary    string `json:"output_summary,omitempty"`
	OutputTruncated  bool   `json:"output_truncated"`
	RedactionCount   int    `json:"redaction_count"`
	DurationMS       int64  `json:"duration_ms"`
	ErrorType        string `json:"error_type,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

type reportReviewResult struct {
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation,omitempty"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
}

// finalizeReviewTask publishes reports first and then atomically publishes the
// terminal Review Store projection. If either report or the final projection
// fails, report artifacts are removed so a task never advertises a half-written
// report pair.
func (r *reviewer) finalizeReviewTask(
	ctx context.Context,
	taskID string,
	info artifact.SessionInfo,
	tracker *reviewRunTracker,
	runErr error,
) (ReviewOutcome, error) {
	finishedAt := time.Now()
	status, errorType, errorMessage := terminalTaskOutcome(runErr)
	errorMessage = r.recorder.mask(errorMessage)
	snapshot, err := r.recorder.Snapshot(ctx, taskID)
	if err != nil {
		monitoring := buildMonitoringSummary(store.ReviewSnapshot{}, tracker, finishedAt, "snapshot_failure")
		monitoringJSON, encodeErr := json.Marshal(monitoring)
		if encodeErr != nil {
			monitoringJSON = []byte(`{"exception_distribution":{"snapshot_failure":1}}`)
		}
		finishErr := r.recorder.FinalizeTask(ctx, taskID, store.TaskFinalization{
			Status: "failed", MonitoringSummaryJSON: string(monitoringJSON),
			FinishedAt: finishedAt, ErrorType: "snapshot_failure",
			ErrorMessage: r.recorder.mask(err.Error()),
		})
		return ReviewOutcome{TaskID: taskID}, errors.Join(err, encodeErr, finishErr)
	}
	monitoring := buildMonitoringSummary(snapshot, tracker, finishedAt, errorType)
	monitoringJSON, err := json.Marshal(monitoring)
	if err != nil {
		encodeErr := fmt.Errorf("encode monitoring summary: %w", err)
		finishErr := r.finalizeTaskWithoutReports(
			ctx, taskID, snapshot, tracker, finishedAt, errors.Join(runErr, encodeErr),
		)
		return ReviewOutcome{TaskID: taskID}, errors.Join(encodeErr, finishErr)
	}

	document := buildReportDocument(
		snapshot, monitoring, status, finishedAt, errorType, errorMessage,
	)
	jsonReport, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		encodeErr := fmt.Errorf("encode JSON report: %w", err)
		finishErr := r.finalizeTaskWithoutReports(
			ctx, taskID, snapshot, tracker, finishedAt, errors.Join(runErr, encodeErr),
		)
		return ReviewOutcome{TaskID: taskID}, errors.Join(encodeErr, finishErr)
	}
	markdownReport := []byte(renderMarkdownReport(document))
	jsonVersion, err := r.dependencies.ArtifactService.SaveArtifact(ctx, info, jsonReportName, &artifact.Artifact{
		Data: jsonReport, MimeType: "application/json", Name: jsonReportName,
	})
	if err != nil {
		artifactErr := fmt.Errorf("save JSON review report: %w", err)
		finishErr := r.finalizeTaskWithoutReports(
			ctx, taskID, snapshot, tracker, finishedAt, errors.Join(runErr, artifactErr),
		)
		return ReviewOutcome{TaskID: taskID}, errors.Join(artifactErr, finishErr)
	}
	markdownVersion, err := r.dependencies.ArtifactService.SaveArtifact(ctx, info, markdownReportName, &artifact.Artifact{
		Data: markdownReport, MimeType: "text/markdown", Name: markdownReportName,
	})
	if err != nil {
		cleanupErr := r.dependencies.ArtifactService.DeleteArtifact(ctx, info, jsonReportName)
		artifactErr := fmt.Errorf("save Markdown review report: %w", err)
		finishErr := r.finalizeTaskWithoutReports(
			ctx, taskID, snapshot, tracker, finishedAt, errors.Join(runErr, artifactErr),
		)
		return ReviewOutcome{TaskID: taskID}, errors.Join(artifactErr, cleanupErr, finishErr)
	}
	refs := store.ReportReferences{
		JSONName: jsonReportName, JSONVersion: jsonVersion,
		MarkdownName: markdownReportName, MarkdownVersion: markdownVersion,
	}
	if err := r.recorder.FinalizeTask(ctx, taskID, store.TaskFinalization{
		Status: status, MonitoringSummaryJSON: string(monitoringJSON),
		Reports: &refs, FinishedAt: finishedAt,
		ErrorType: errorType, ErrorMessage: errorMessage,
	}); err != nil {
		jsonCleanupErr := r.dependencies.ArtifactService.DeleteArtifact(ctx, info, jsonReportName)
		markdownCleanupErr := r.dependencies.ArtifactService.DeleteArtifact(ctx, info, markdownReportName)
		return ReviewOutcome{TaskID: taskID}, errors.Join(err, jsonCleanupErr, markdownCleanupErr)
	}
	return ReviewOutcome{
		TaskID: taskID, JSONReport: jsonReport, MarkdownReport: markdownReport,
		References: refs,
	}, nil
}

func (r *reviewer) finalizeTaskWithoutReports(
	ctx context.Context,
	taskID string,
	snapshot store.ReviewSnapshot,
	tracker *reviewRunTracker,
	finishedAt time.Time,
	runErr error,
) error {
	status, errorType, errorMessage := terminalTaskOutcome(runErr)
	monitoring := buildMonitoringSummary(snapshot, tracker, finishedAt, errorType)
	monitoringJSON, err := json.Marshal(monitoring)
	if err != nil {
		monitoringJSON = []byte(`{"exception_distribution":{"finalization_failure":1}}`)
	}
	finishErr := r.recorder.FinalizeTask(ctx, taskID, store.TaskFinalization{
		Status: status, MonitoringSummaryJSON: string(monitoringJSON),
		FinishedAt: finishedAt, ErrorType: errorType,
		ErrorMessage: r.recorder.mask(errorMessage),
	})
	return errors.Join(err, finishErr)
}

func terminalTaskOutcome(runErr error) (status, errorType, errorMessage string) {
	if runErr == nil {
		return "completed", "", ""
	}
	switch {
	case errors.Is(runErr, context.Canceled):
		return "canceled", "canceled", runErr.Error()
	case errors.Is(runErr, context.DeadlineExceeded):
		return "failed", "timeout", runErr.Error()
	default:
		return "failed", "review_failure", runErr.Error()
	}
}

// buildMonitoringSummary treats durable permission and sandbox rows as the
// reportable facts. In-memory counters cover failures before persistence; max
// reconciliation prevents the same callback-observed failure or ask decision
// from being counted twice once its durable row exists.
func buildMonitoringSummary(
	snapshot store.ReviewSnapshot,
	tracker *reviewRunTracker,
	finishedAt time.Time,
	terminalErrorType string,
) monitoringSummary {
	summary := monitoringSummary{
		ResultKindDistribution: make(map[string]int),
		SeverityDistribution:   make(map[string]int),
		ExceptionDistribution:  make(map[string]int),
	}
	if !snapshot.Task.StartedAt.IsZero() {
		summary.TotalDurationMS = finishedAt.Sub(snapshot.Task.StartedAt).Milliseconds()
	}
	toolCalls := make(map[string]struct{})
	interceptions := make(map[string]struct{})
	for index, decision := range snapshot.PermissionDecisions {
		toolCallID := decision.ToolCallID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("row:%d", index)
		}
		toolCalls[toolCallID] = struct{}{}
		if decision.Decision == "ask" || decision.Decision == "deny" {
			interceptions[toolCallID] = struct{}{}
		}
	}
	summary.ToolCallCount = len(toolCalls)
	summary.PermissionInterceptionCount = len(interceptions)
	if tracker != nil {
		tracker.mu.Lock()
		if summary.TotalDurationMS == 0 {
			summary.TotalDurationMS = finishedAt.Sub(tracker.startedAt).Milliseconds()
		}
		summary.ToolCallCount = max(summary.ToolCallCount, tracker.toolCalls)
		summary.PermissionInterceptionCount = max(
			summary.PermissionInterceptionCount,
			tracker.permissionInterceptions,
		)
		for kind, count := range tracker.exceptions {
			summary.ExceptionDistribution[kind] = count
		}
		tracker.mu.Unlock()
	}
	for _, result := range snapshot.Results {
		summary.ResultKindDistribution[result.ResultKind]++
		summary.SeverityDistribution[result.Severity]++
		if result.ResultKind == "finding" {
			summary.FindingCount++
		}
	}
	for _, run := range snapshot.SandboxRuns {
		summary.SandboxDurationMS += run.Duration.Milliseconds()
		if run.Status != "succeeded" {
			kind := run.ErrorType
			if kind == "" {
				kind = run.Status
			}
			// The callback tracker observes the same sandbox failure before it
			// is persisted. The durable row replaces that provisional count.
			summary.ExceptionDistribution[kind] = max(
				summary.ExceptionDistribution[kind],
				countSandboxExceptions(snapshot.SandboxRuns, kind),
			)
		}
	}
	if terminalErrorType != "" {
		summary.ExceptionDistribution[terminalErrorType]++
	}
	return summary
}

func countSandboxExceptions(runs []store.SandboxRunRecord, kind string) int {
	count := 0
	for _, run := range runs {
		runKind := run.ErrorType
		if runKind == "" && run.Status != "succeeded" {
			runKind = run.Status
		}
		if runKind == kind {
			count++
		}
	}
	return count
}

func buildReportDocument(
	snapshot store.ReviewSnapshot,
	monitoring monitoringSummary,
	status string,
	finishedAt time.Time,
	errorType string,
	errorMessage string,
) reportDocument {
	summaryJSON := json.RawMessage(snapshot.Task.InputSummaryJSON)
	if !json.Valid(summaryJSON) {
		summaryJSON = json.RawMessage(`{}`)
	}
	document := reportDocument{
		SchemaVersion: "1.0",
		Task: reportTask{
			TaskID: snapshot.Task.TaskID, AppName: snapshot.Task.AppName,
			UserID: snapshot.Task.UserID, SessionID: snapshot.Task.TaskID,
			Status: status, StartedAt: snapshot.Task.StartedAt.UTC().Format(time.RFC3339Nano),
			FinishedAt: finishedAt.UTC().Format(time.RFC3339Nano),
			ErrorType:  errorType, ErrorMessage: errorMessage,
		},
		Input: reportInput{
			Kind: snapshot.Task.InputKind, Summary: summaryJSON,
			ArtifactName:    snapshot.Task.InputArtifactName,
			ArtifactVersion: snapshot.Task.InputArtifactVersion,
		},
		Monitoring: monitoring, Conclusion: snapshot.Task.Conclusion,
		Permissions: []reportPermission{}, SandboxRuns: []reportSandboxRun{},
		Findings: []reportReviewResult{}, Warnings: []reportReviewResult{},
		HumanReview: []reportReviewResult{},
	}
	for _, decision := range snapshot.PermissionDecisions {
		document.Permissions = append(document.Permissions, reportPermission{
			ToolCallID: decision.ToolCallID, ToolName: decision.ToolName,
			Operation: decision.Operation, CommandPreview: decision.CommandPreview,
			Decision: decision.Decision, Reason: decision.Reason,
			DecidedAt: decision.DecidedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	for _, run := range snapshot.SandboxRuns {
		document.SandboxRuns = append(document.SandboxRuns, reportSandboxRun{
			ToolCallID: run.ToolCallID, Backend: run.Backend,
			CommandPreview: run.CommandPreview, Workdir: run.Workdir,
			TimeoutMS: run.Timeout.Milliseconds(), OutputLimitBytes: run.OutputLimitBytes,
			Status: run.Status, ExitCode: run.ExitCode, TimedOut: run.TimedOut,
			OutputSummary: run.StdoutSummary, OutputTruncated: run.StdoutTruncated,
			RedactionCount: run.RedactionCount, DurationMS: run.Duration.Milliseconds(),
			ErrorType: run.ErrorType, ErrorMessage: run.ErrorMessage,
		})
	}
	for _, result := range snapshot.Results {
		reportResult := reportReviewResult{
			Severity: result.Severity, Category: result.Category, File: result.File,
			Line: result.Line, Title: result.Title, Evidence: result.Evidence,
			Recommendation: result.Recommendation, Confidence: result.Confidence,
			Source: result.Source, RuleID: result.RuleID,
		}
		switch result.ResultKind {
		case "finding":
			document.Findings = append(document.Findings, reportResult)
		case "warning":
			document.Warnings = append(document.Warnings, reportResult)
		case "needs_human_review":
			document.HumanReview = append(document.HumanReview, reportResult)
		}
	}
	return document
}

func renderMarkdownReport(document reportDocument) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Code Review Report\n\nTask: `%s`\n\nStatus: %s\n\nInput: %s\n\n",
		document.Task.TaskID, document.Task.Status, document.Input.Kind)
	fmt.Fprintf(&builder, "## Conclusion\n\n%s\n\n", emptyReportValue(document.Conclusion, "No conclusion was submitted."))
	fmt.Fprintf(&builder, "## Summary\n\n- Findings: %d\n- Warnings: %d\n- Needs human review: %d\n- Tool calls: %d\n- Permission interceptions: %d\n- Total duration: %d ms\n- Sandbox duration: %d ms\n- Severity distribution: %s\n- Exception distribution: %s\n\n",
		len(document.Findings), len(document.Warnings), len(document.HumanReview),
		document.Monitoring.ToolCallCount, document.Monitoring.PermissionInterceptionCount,
		document.Monitoring.TotalDurationMS, document.Monitoring.SandboxDurationMS,
		formatDistribution(document.Monitoring.SeverityDistribution),
		formatDistribution(document.Monitoring.ExceptionDistribution))
	renderResultSection(&builder, "Findings", document.Findings)
	renderResultSection(&builder, "Warnings", document.Warnings)
	renderResultSection(&builder, "Needs Human Review", document.HumanReview)
	renderGovernanceSection(&builder, document.Permissions)
	builder.WriteString("## Sandbox Runs\n\n")
	if len(document.SandboxRuns) == 0 {
		builder.WriteString("None.\n\n")
	}
	for _, run := range document.SandboxRuns {
		fmt.Fprintf(&builder, "- `%s` — %s, exit=%s, duration=%d ms, truncated=%t\n",
			run.CommandPreview, run.Status, formatExitCode(run.ExitCode), run.DurationMS, run.OutputTruncated)
	}
	return builder.String()
}

func renderGovernanceSection(builder *strings.Builder, decisions []reportPermission) {
	builder.WriteString("## Governance Interceptions\n\n")
	intercepted := make(map[string]bool)
	for index, decision := range decisions {
		if decision.Decision != "ask" && decision.Decision != "deny" {
			continue
		}
		key := decision.ToolCallID
		if key == "" {
			key = fmt.Sprintf("row:%d", index)
		}
		intercepted[key] = true
	}
	written := 0
	for index, decision := range decisions {
		key := decision.ToolCallID
		if key == "" {
			key = fmt.Sprintf("row:%d", index)
		}
		// Plain allow decisions are routine tool calls. An allow following ask
		// is part of the interception lifecycle and must remain visible.
		if !intercepted[key] {
			continue
		}
		written++
		fmt.Fprintf(builder, "- %s `%s`", decision.Decision, decision.ToolName)
		if decision.CommandPreview != "" {
			fmt.Fprintf(builder, " — `%s`", decision.CommandPreview)
		}
		if decision.Reason != "" {
			fmt.Fprintf(builder, ": %s", decision.Reason)
		}
		builder.WriteByte('\n')
	}
	if written == 0 {
		builder.WriteString("None.\n")
	}
	builder.WriteByte('\n')
}

func formatDistribution(distribution map[string]int) string {
	if len(distribution) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(distribution))
	for key := range distribution {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, distribution[key]))
	}
	return strings.Join(parts, ", ")
}

func renderResultSection(builder *strings.Builder, title string, results []reportReviewResult) {
	fmt.Fprintf(builder, "## %s\n\n", title)
	if len(results) == 0 {
		builder.WriteString("None.\n\n")
		return
	}
	for _, result := range results {
		location := result.File
		if result.Line > 0 {
			location = fmt.Sprintf("%s:%d", result.File, result.Line)
		}
		fmt.Fprintf(builder, "### %s\n\n- Severity: %s\n- Category: %s\n- Location: `%s`\n- Confidence: %.2f\n- Source: %s (`%s`)\n\nEvidence: %s\n\nRecommendation: %s\n\n",
			result.Title, result.Severity, result.Category, location, result.Confidence,
			result.Source, result.RuleID, result.Evidence,
			emptyReportValue(result.Recommendation, "No recommendation supplied."))
	}
}

func formatExitCode(exitCode *int) string {
	if exitCode == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *exitCode)
}

func emptyReportValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
