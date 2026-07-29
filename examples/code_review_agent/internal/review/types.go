//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package review defines the canonical code review domain model.
package review

import (
	"errors"
	"fmt"
	"time"
)

// SchemaVersion identifies the canonical review schema used by this example.
const SchemaVersion = "review/v1"

var errInvalidValue = errors.New("invalid value")

// Status describes the lifecycle state of a task or sandbox run.
type Status string

const (
	// StatusPending indicates that work has not started.
	StatusPending Status = "pending"
	// StatusRunning indicates that work is in progress.
	StatusRunning Status = "running"
	// StatusCompleted indicates that work finished successfully.
	StatusCompleted Status = "completed"
	// StatusFailed indicates that work finished with an error.
	StatusFailed Status = "failed"
	// StatusCanceled indicates that work stopped after cancellation.
	StatusCanceled Status = "canceled"
	// StatusSkipped indicates that a sandbox run was intentionally not started.
	StatusSkipped Status = "skipped"
	// StatusTimedOut indicates that a sandbox run exceeded its time limit.
	StatusTimedOut Status = "timed_out"
	// StatusUnavailable indicates that an optional sandbox checker was unavailable.
	StatusUnavailable Status = "unavailable"
)

// Phase identifies the current stage of a review task.
type Phase string

const (
	// PhaseCreated indicates that the task record has been created.
	PhaseCreated Phase = "created"
	// PhaseInput indicates that review input is being normalized and parsed.
	PhaseInput Phase = "input"
	// PhaseRules indicates that deterministic rules are running.
	PhaseRules Phase = "rules"
	// PhaseSandbox indicates that governed sandbox checks are running.
	PhaseSandbox Phase = "sandbox"
	// PhaseAssist indicates that optional model-assisted review is running.
	PhaseAssist Phase = "assist"
	// PhaseFinalize indicates that canonical reports and artifacts are being finalized.
	PhaseFinalize Phase = "finalize"
	// PhaseCompleted indicates that task finalization has completed.
	PhaseCompleted Phase = "completed"
)

// InputSource identifies how review input was obtained.
type InputSource string

const (
	// InputSourceDiffFile identifies a unified diff file input.
	InputSourceDiffFile InputSource = "diff_file"
	// InputSourceRepository identifies a Git working tree input.
	InputSourceRepository InputSource = "repository"
	// InputSourceFixture identifies a bundled fixture input.
	InputSourceFixture InputSource = "fixture"
)

// Severity describes the impact of a finding.
type Severity string

const (
	// SeverityCritical identifies an immediately exploitable or catastrophic issue.
	SeverityCritical Severity = "critical"
	// SeverityHigh identifies a serious issue that should be fixed promptly.
	SeverityHigh Severity = "high"
	// SeverityMedium identifies a material issue with limited impact.
	SeverityMedium Severity = "medium"
	// SeverityLow identifies a minor issue.
	SeverityLow Severity = "low"
	// SeverityInfo identifies an informational observation.
	SeverityInfo Severity = "info"
)

// Confidence describes the evidence strength for a finding.
type Confidence string

const (
	// ConfidenceHigh indicates strong evidence with little ambiguity.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium indicates credible evidence with some ambiguity.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow indicates evidence that requires human confirmation.
	ConfidenceLow Confidence = "low"
)

// Source identifies the producer of a finding.
type Source string

const (
	// SourceRule identifies a deterministic rule finding.
	SourceRule Source = "rule"
	// SourceTool identifies a sandbox tool finding.
	SourceTool Source = "tool"
	// SourceModel identifies a model-assisted finding.
	SourceModel Source = "model"
)

// Disposition identifies how a validated finding is presented.
type Disposition string

const (
	// DispositionFinding identifies a canonical actionable finding.
	DispositionFinding Disposition = "finding"
	// DispositionWarning identifies a non-finding warning.
	DispositionWarning Disposition = "warning"
	// DispositionNeedsHumanReview identifies a finding requiring human review.
	DispositionNeedsHumanReview Disposition = "needs_human_review"
)

// DecisionKind identifies the governance control that made a decision.
type DecisionKind string

const (
	// DecisionKindFilter identifies a tool filter decision.
	DecisionKindFilter DecisionKind = "filter"
	// DecisionKindPermission identifies a permission policy decision.
	DecisionKindPermission DecisionKind = "permission"
)

// DecisionAction identifies the outcome of a governance decision.
type DecisionAction string

const (
	// DecisionActionAllow permits the operation.
	DecisionActionAllow DecisionAction = "allow"
	// DecisionActionDeny rejects the operation.
	DecisionActionDeny DecisionAction = "deny"
	// DecisionActionAsk requires approval that the non-interactive CLI cannot provide.
	DecisionActionAsk DecisionAction = "ask"
)

// Task records the lifecycle state of one review.
type Task struct {
	SchemaVersion string    `json:"schema_version"`
	ID            string    `json:"id"`
	Status        Status    `json:"status"`
	Phase         Phase     `json:"phase"`
	Mode          string    `json:"mode"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	TerminalError string    `json:"terminal_error,omitempty"`
}

// Validate checks that Task contains a supported schema and closed enum values.
func (t Task) Validate() error {
	if err := validateSchema(t.SchemaVersion); err != nil {
		return fmt.Errorf("validate task: %w", err)
	}
	if err := requireString("id", t.ID); err != nil {
		return fmt.Errorf("validate task: %w", err)
	}
	if !t.Status.validTask() {
		return fmt.Errorf("validate task status %q: %w", t.Status, errInvalidValue)
	}
	if !t.Phase.valid() {
		return fmt.Errorf("validate task phase %q: %w", t.Phase, errInvalidValue)
	}
	if err := requireString("mode", t.Mode); err != nil {
		return fmt.Errorf("validate task: %w", err)
	}
	return nil
}

// ReviewInput records the normalized source and changed-file summary for a task.
type ReviewInput struct {
	SchemaVersion string      `json:"schema_version"`
	TaskID        string      `json:"task_id"`
	Source        InputSource `json:"source"`
	Digest        string      `json:"digest"`
	ChangedFiles  []string    `json:"changed_files"`
}

// Validate checks that ReviewInput contains a supported source and required identifiers.
func (i ReviewInput) Validate() error {
	if err := validateSchema(i.SchemaVersion); err != nil {
		return fmt.Errorf("validate review input: %w", err)
	}
	if err := requireString("task id", i.TaskID); err != nil {
		return fmt.Errorf("validate review input: %w", err)
	}
	if !i.Source.valid() {
		return fmt.Errorf("validate review input source %q: %w", i.Source, errInvalidValue)
	}
	if err := requireString("digest", i.Digest); err != nil {
		return fmt.Errorf("validate review input: %w", err)
	}
	return nil
}

// SandboxRun records one bounded checker execution.
type SandboxRun struct {
	SchemaVersion string        `json:"schema_version"`
	TaskID        string        `json:"task_id"`
	Command       string        `json:"command"`
	Status        Status        `json:"status"`
	Duration      time.Duration `json:"duration"`
	ExitCode      int           `json:"exit_code"`
	TimedOut      bool          `json:"timed_out"`
	Stdout        string        `json:"stdout,omitempty"`
	Stderr        string        `json:"stderr,omitempty"`
	Truncated     bool          `json:"truncated"`
}

// Validate checks that SandboxRun contains a supported status and required identifiers.
func (r SandboxRun) Validate() error {
	if err := validateSchema(r.SchemaVersion); err != nil {
		return fmt.Errorf("validate sandbox run: %w", err)
	}
	if err := requireString("task id", r.TaskID); err != nil {
		return fmt.Errorf("validate sandbox run: %w", err)
	}
	if err := requireString("command", r.Command); err != nil {
		return fmt.Errorf("validate sandbox run: %w", err)
	}
	if !r.Status.validSandboxRun() {
		return fmt.Errorf("validate sandbox run status %q: %w", r.Status, errInvalidValue)
	}
	return nil
}

// GovernanceDecision records one filter or permission decision.
type GovernanceDecision struct {
	SchemaVersion string         `json:"schema_version"`
	TaskID        string         `json:"task_id,omitempty"`
	Kind          DecisionKind   `json:"kind"`
	Tool          string         `json:"tool"`
	Action        DecisionAction `json:"action"`
	Reason        string         `json:"reason"`
	Rule          string         `json:"rule"`
}

// Validate checks that GovernanceDecision contains supported closed enum values.
func (d GovernanceDecision) Validate() error {
	if err := validateSchema(d.SchemaVersion); err != nil {
		return fmt.Errorf("validate governance decision: %w", err)
	}
	if !d.Kind.valid() {
		return fmt.Errorf("validate governance decision kind %q: %w", d.Kind, errInvalidValue)
	}
	if !d.Action.valid() {
		return fmt.Errorf("validate governance decision action %q: %w", d.Action, errInvalidValue)
	}
	for name, value := range map[string]string{
		"tool":   d.Tool,
		"reason": d.Reason,
		"rule":   d.Rule,
	} {
		if err := requireString(name, value); err != nil {
			return fmt.Errorf("validate governance decision: %w", err)
		}
	}
	return nil
}

// Finding is the canonical versioned review finding contract.
type Finding struct {
	SchemaVersion  string      `json:"schema_version"`
	TaskID         string      `json:"task_id,omitempty"`
	Severity       Severity    `json:"severity"`
	Category       string      `json:"category"`
	File           string      `json:"file"`
	Line           int         `json:"line"`
	EndLine        int         `json:"end_line,omitempty"`
	Title          string      `json:"title"`
	Evidence       string      `json:"evidence"`
	Recommendation string      `json:"recommendation"`
	Confidence     Confidence  `json:"confidence"`
	Source         Source      `json:"source"`
	RuleID         string      `json:"rule_id"`
	Fingerprint    string      `json:"fingerprint"`
	Disposition    Disposition `json:"disposition"`
}

// Validate checks the version, required fields, ranges, and closed enums of Finding.
func (f Finding) Validate() error {
	if err := validateSchema(f.SchemaVersion); err != nil {
		return fmt.Errorf("validate finding: %w", err)
	}
	if !f.Severity.valid() {
		return fmt.Errorf("validate finding severity %q: %w", f.Severity, errInvalidValue)
	}
	if !f.Confidence.valid() {
		return fmt.Errorf("validate finding confidence %q: %w", f.Confidence, errInvalidValue)
	}
	if !f.Source.valid() {
		return fmt.Errorf("validate finding source %q: %w", f.Source, errInvalidValue)
	}
	if !f.Disposition.valid() {
		return fmt.Errorf("validate finding disposition %q: %w", f.Disposition, errInvalidValue)
	}
	for name, value := range map[string]string{
		"category":       f.Category,
		"file":           f.File,
		"title":          f.Title,
		"evidence":       f.Evidence,
		"recommendation": f.Recommendation,
		"rule id":        f.RuleID,
		"fingerprint":    f.Fingerprint,
	} {
		if err := requireString(name, value); err != nil {
			return fmt.Errorf("validate finding: %w", err)
		}
	}
	if f.Line < 1 {
		return fmt.Errorf("validate finding line %d: %w", f.Line, errInvalidValue)
	}
	if f.EndLine != 0 && f.EndLine < f.Line {
		return fmt.Errorf("validate finding end line %d: %w", f.EndLine, errInvalidValue)
	}
	return nil
}

// ArtifactRecord identifies a pinned artifact and its integrity metadata.
type ArtifactRecord struct {
	SchemaVersion string `json:"schema_version"`
	TaskID        string `json:"task_id"`
	Name          string `json:"name"`
	Reference     string `json:"reference"`
	Digest        string `json:"digest"`
	MIMEType      string `json:"mime_type"`
	Size          int64  `json:"size"`
}

// Validate checks that ArtifactRecord contains required metadata and a non-negative size.
func (a ArtifactRecord) Validate() error {
	if err := validateSchema(a.SchemaVersion); err != nil {
		return fmt.Errorf("validate artifact record: %w", err)
	}
	for name, value := range map[string]string{
		"task id":   a.TaskID,
		"name":      a.Name,
		"reference": a.Reference,
		"digest":    a.Digest,
		"mime type": a.MIMEType,
	} {
		if err := requireString(name, value); err != nil {
			return fmt.Errorf("validate artifact record: %w", err)
		}
	}
	if a.Size < 0 {
		return fmt.Errorf("validate artifact record size %d: %w", a.Size, errInvalidValue)
	}
	return nil
}

// Metrics contains the exact per-task monitoring summary.
type Metrics struct {
	SchemaVersion    string           `json:"schema_version"`
	TotalDuration    time.Duration    `json:"total_duration"`
	SandboxDuration  time.Duration    `json:"sandbox_duration"`
	ToolInvocations  int              `json:"tool_invocations"`
	PermissionBlocks int              `json:"permission_blocks"`
	FindingTotal     int              `json:"finding_total"`
	SeverityCounts   map[Severity]int `json:"severity_counts"`
	WarningCount     int              `json:"warning_count"`
	HumanReviewCount int              `json:"human_review_count"`
	ErrorTypeCounts  map[string]int   `json:"error_type_counts"`
}

// Validate checks that Metrics uses the supported schema and contains no
// negative durations or counts and only known severities.
func (m Metrics) Validate() error {
	if err := validateSchema(m.SchemaVersion); err != nil {
		return fmt.Errorf("validate metrics: %w", err)
	}
	if m.TotalDuration < 0 || m.SandboxDuration < 0 {
		return fmt.Errorf("validate metrics duration: %w", errInvalidValue)
	}
	for name, value := range map[string]int{
		"tool invocations":   m.ToolInvocations,
		"permission blocks":  m.PermissionBlocks,
		"finding total":      m.FindingTotal,
		"warning count":      m.WarningCount,
		"human review count": m.HumanReviewCount,
	} {
		if value < 0 {
			return fmt.Errorf("validate metrics %s %d: %w", name, value, errInvalidValue)
		}
	}
	for severity, count := range m.SeverityCounts {
		if !severity.valid() || count < 0 {
			return fmt.Errorf("validate metrics severity %q count %d: %w", severity, count, errInvalidValue)
		}
	}
	for errorType, count := range m.ErrorTypeCounts {
		if errorType == "" || count < 0 {
			return fmt.Errorf("validate metrics error type %q count %d: %w", errorType, count, errInvalidValue)
		}
	}
	return nil
}

// Report is the canonical aggregate used to produce JSON and Markdown artifacts.
type Report struct {
	SchemaVersion       string               `json:"schema_version"`
	Task                Task                 `json:"task"`
	Input               ReviewInput          `json:"input"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	GovernanceDecisions []GovernanceDecision `json:"governance_decisions"`
	Findings            []Finding            `json:"findings"`
	Artifacts           []ArtifactRecord     `json:"artifacts"`
	Metrics             Metrics              `json:"metrics"`
	Conclusion          string               `json:"conclusion"`
}

// Validate checks the report schema and each nested canonical record.
func (r Report) Validate() error {
	if err := validateSchema(r.SchemaVersion); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	if err := r.Task.Validate(); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	if err := r.Input.Validate(); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	for index, run := range r.SandboxRuns {
		if err := run.Validate(); err != nil {
			return fmt.Errorf("validate report sandbox run %d: %w", index, err)
		}
	}
	for index, decision := range r.GovernanceDecisions {
		if err := decision.Validate(); err != nil {
			return fmt.Errorf("validate report governance decision %d: %w", index, err)
		}
	}
	for index, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			return fmt.Errorf("validate report finding %d: %w", index, err)
		}
	}
	for index, artifact := range r.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("validate report artifact %d: %w", index, err)
		}
	}
	if err := r.Metrics.Validate(); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	if err := requireString("conclusion", r.Conclusion); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	return nil
}

func validateSchema(version string) error {
	if version != SchemaVersion {
		return fmt.Errorf("schema version %q: %w", version, errInvalidValue)
	}
	return nil
}

func requireString(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required: %w", name, errInvalidValue)
	}
	return nil
}

func (s Status) validTask() bool {
	switch s {
	case StatusPending, StatusRunning, StatusCompleted, StatusFailed,
		StatusCanceled:
		return true
	default:
		return false
	}
}

func (s Status) validSandboxRun() bool {
	switch s {
	case StatusPending, StatusRunning, StatusCompleted, StatusFailed,
		StatusCanceled, StatusSkipped, StatusTimedOut, StatusUnavailable:
		return true
	default:
		return false
	}
}

func (p Phase) valid() bool {
	switch p {
	case PhaseCreated, PhaseInput, PhaseRules, PhaseSandbox, PhaseAssist,
		PhaseFinalize, PhaseCompleted:
		return true
	default:
		return false
	}
}

func (s InputSource) valid() bool {
	switch s {
	case InputSourceDiffFile, InputSourceRepository, InputSourceFixture:
		return true
	default:
		return false
	}
}

func (s Severity) valid() bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo:
		return true
	default:
		return false
	}
}

func (c Confidence) valid() bool {
	switch c {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return true
	default:
		return false
	}
}

func (s Source) valid() bool {
	switch s {
	case SourceRule, SourceTool, SourceModel:
		return true
	default:
		return false
	}
}

func (d Disposition) valid() bool {
	switch d {
	case DispositionFinding, DispositionWarning, DispositionNeedsHumanReview:
		return true
	default:
		return false
	}
}

func (k DecisionKind) valid() bool {
	switch k {
	case DecisionKindFilter, DecisionKindPermission:
		return true
	default:
		return false
	}
}

func (a DecisionAction) valid() bool {
	switch a {
	case DecisionActionAllow, DecisionActionDeny, DecisionActionAsk:
		return true
	default:
		return false
	}
}
