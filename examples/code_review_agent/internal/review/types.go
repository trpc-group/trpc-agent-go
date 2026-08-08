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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion identifies the canonical review schema used by this example.
const SchemaVersion = "review/v1"

var errInvalidValue = errors.New("invalid value")

// TaskStatus describes the lifecycle state of a review task.
type TaskStatus string

const (
	// TaskStatusPending indicates that task work has not started.
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusRunning indicates that task work is in progress.
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusCompleted indicates that the task finished successfully.
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusFailed indicates that the task finished with an error.
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusCanceled indicates that the task stopped after cancellation.
	TaskStatusCanceled TaskStatus = "canceled"
)

// SandboxStatus describes the lifecycle state of a sandbox run.
type SandboxStatus string

const (
	// SandboxStatusPending indicates that the run has not started.
	SandboxStatusPending SandboxStatus = "pending"
	// SandboxStatusRunning indicates that the run is in progress.
	SandboxStatusRunning SandboxStatus = "running"
	// SandboxStatusCompleted indicates that the command exited successfully.
	SandboxStatusCompleted SandboxStatus = "completed"
	// SandboxStatusFailed indicates that the command exited unsuccessfully.
	SandboxStatusFailed SandboxStatus = "failed"
	// SandboxStatusCanceled indicates that the run stopped after cancellation.
	SandboxStatusCanceled SandboxStatus = "canceled"
	// SandboxStatusSkipped indicates that the run was intentionally not started.
	SandboxStatusSkipped SandboxStatus = "skipped"
	// SandboxStatusTimedOut indicates that the run exceeded its time limit.
	SandboxStatusTimedOut SandboxStatus = "timed_out"
	// SandboxStatusUnavailable indicates that an optional checker was unavailable.
	SandboxStatusUnavailable SandboxStatus = "unavailable"
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

// Mode identifies the configured review execution mode.
type Mode string

const (
	// ModeRuleOnly runs deterministic review without constructing a model.
	ModeRuleOnly Mode = "rule-only"
	// ModeFakeModel runs the deterministic fake-model integration path.
	ModeFakeModel Mode = "fake-model"
	// ModeModel runs deterministic review with optional model assistance.
	ModeModel Mode = "model"
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

// ChangeLayer identifies the source-state transition containing a finding.
type ChangeLayer string

const (
	// ChangeLayerUnified identifies a standalone unified diff.
	ChangeLayerUnified ChangeLayer = "unified"
	// ChangeLayerStaged identifies the HEAD-to-index transition.
	ChangeLayerStaged ChangeLayer = "staged"
	// ChangeLayerWorktree identifies the index-to-working-tree transition.
	ChangeLayerWorktree ChangeLayer = "worktree"
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
	SchemaVersion string     `json:"schema_version"`
	ID            string     `json:"id"`
	Status        TaskStatus `json:"status"`
	Phase         Phase      `json:"phase"`
	Mode          Mode       `json:"mode"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	TerminalError string     `json:"terminal_error,omitempty"`
}

// Validate checks Task schema, enums, timestamps, and lifecycle invariants.
func (t Task) Validate() error {
	if err := validateSchema(t.SchemaVersion); err != nil {
		return fmt.Errorf("validate task: %w", err)
	}
	if err := requireIdentifier("id", t.ID); err != nil {
		return fmt.Errorf("validate task: %w", err)
	}
	if !t.Status.valid() {
		return fmt.Errorf("validate task status %q: %w", t.Status, errInvalidValue)
	}
	if !t.Phase.valid() {
		return fmt.Errorf("validate task phase %q: %w", t.Phase, errInvalidValue)
	}
	if !t.Mode.valid() {
		return fmt.Errorf("validate task mode %q: %w", t.Mode, errInvalidValue)
	}
	if t.CreatedAt.IsZero() {
		return fmt.Errorf("validate task created at: %w", errInvalidValue)
	}
	if t.UpdatedAt.IsZero() {
		return fmt.Errorf("validate task updated at: %w", errInvalidValue)
	}
	if t.UpdatedAt.Before(t.CreatedAt) {
		return fmt.Errorf("validate task timestamps: %w", errInvalidValue)
	}
	if t.Status == TaskStatusPending && t.Phase != PhaseCreated {
		return fmt.Errorf("validate task pending phase %q: %w", t.Phase, errInvalidValue)
	}
	if t.Status == TaskStatusRunning && t.Phase == PhaseCreated {
		return fmt.Errorf("validate task running phase %q: %w", t.Phase, errInvalidValue)
	}
	if (t.Status == TaskStatusCompleted) != (t.Phase == PhaseCompleted) {
		return fmt.Errorf("validate task completed status and phase: %w", errInvalidValue)
	}
	switch t.Status {
	case TaskStatusFailed, TaskStatusCanceled:
		if t.TerminalError == "" {
			return fmt.Errorf("validate task terminal error: %w", errInvalidValue)
		}
	default:
		if t.TerminalError != "" {
			return fmt.Errorf("validate task terminal error: %w", errInvalidValue)
		}
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
	Status        SandboxStatus `json:"status"`
	Duration      time.Duration `json:"duration"`
	ExitCode      *int          `json:"exit_code,omitempty"`
	TimedOut      bool          `json:"timed_out"`
	Stdout        string        `json:"stdout,omitempty"`
	Stderr        string        `json:"stderr,omitempty"`
	Truncated     bool          `json:"truncated"`
}

// Validate checks SandboxRun identity, status, duration, exit code, and timeout
// consistency.
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
	if !r.Status.valid() {
		return fmt.Errorf("validate sandbox run status %q: %w", r.Status, errInvalidValue)
	}
	if r.Duration < 0 {
		return fmt.Errorf("validate sandbox run duration: %w", errInvalidValue)
	}
	switch r.Status {
	case SandboxStatusCompleted:
		if r.ExitCode == nil || *r.ExitCode != 0 {
			return fmt.Errorf("validate sandbox run completed exit code: %w", errInvalidValue)
		}
	case SandboxStatusFailed:
		if r.ExitCode == nil || *r.ExitCode == 0 {
			return fmt.Errorf("validate sandbox run failed exit code: %w", errInvalidValue)
		}
	default:
		if r.ExitCode != nil {
			return fmt.Errorf("validate sandbox run exit code for status %q: %w", r.Status, errInvalidValue)
		}
	}
	if (r.Status == SandboxStatusTimedOut) != r.TimedOut {
		return fmt.Errorf("validate sandbox run timed out status: %w", errInvalidValue)
	}
	return nil
}

// GovernanceDecision records one filter or permission decision.
type GovernanceDecision struct {
	SchemaVersion string         `json:"schema_version"`
	TaskID        string         `json:"task_id,omitempty"`
	DecisionID    string         `json:"decision_id"`
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
	if err := requireStrings(
		namedString{name: "tool", value: d.Tool},
		namedString{name: "reason", value: d.Reason},
		namedString{name: "rule", value: d.Rule},
	); err != nil {
		return fmt.Errorf("validate governance decision: %w", err)
	}
	if err := requireIdentifier("decision id", d.DecisionID); err != nil {
		return fmt.Errorf("validate governance decision: %w", err)
	}
	return nil
}

// Finding is the canonical versioned review finding contract.
type Finding struct {
	SchemaVersion  string      `json:"schema_version"`
	TaskID         string      `json:"task_id,omitempty"`
	Severity       Severity    `json:"severity"`
	Category       string      `json:"category"`
	Layer          ChangeLayer `json:"layer"`
	File           string      `json:"file"`
	Line           int         `json:"line"`
	EndLine        int         `json:"end_line,omitempty"`
	SemanticAnchor string      `json:"semantic_anchor"`
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
		return fmt.Errorf("validate finding severity: %w", errInvalidValue)
	}
	if !f.Confidence.valid() {
		return fmt.Errorf("validate finding confidence: %w", errInvalidValue)
	}
	if !f.Source.valid() {
		return fmt.Errorf("validate finding source: %w", errInvalidValue)
	}
	if !f.Disposition.valid() {
		return fmt.Errorf("validate finding disposition: %w", errInvalidValue)
	}
	if !f.Layer.valid() {
		return fmt.Errorf("validate finding layer: %w", errInvalidValue)
	}
	if f.TaskID != "" {
		if err := requireIdentifier("task id", f.TaskID); err != nil {
			return fmt.Errorf("validate finding: %w", err)
		}
	}
	if err := requireStrings(
		namedString{name: "category", value: f.Category},
		namedString{name: "file", value: f.File},
		namedString{name: "title", value: f.Title},
		namedString{name: "evidence", value: f.Evidence},
		namedString{name: "recommendation", value: f.Recommendation},
		namedString{name: "rule id", value: f.RuleID},
		namedString{name: "semantic anchor", value: f.SemanticAnchor},
		namedString{name: "fingerprint", value: f.Fingerprint},
	); err != nil {
		return fmt.Errorf("validate finding: %w", err)
	}
	if f.Line < 1 {
		return fmt.Errorf("validate finding line %d: %w", f.Line, errInvalidValue)
	}
	if f.EndLine != 0 && f.EndLine < f.Line {
		return fmt.Errorf("validate finding end line %d: %w", f.EndLine, errInvalidValue)
	}
	if path.IsAbs(f.File) || path.Clean(f.File) != f.File || strings.ContainsAny(f.File, "\\\x00") {
		return fmt.Errorf("validate finding file: %w", errInvalidValue)
	}
	if !validVersionedName(f.RuleID) {
		return fmt.Errorf("validate finding rule id: %w", errInvalidValue)
	}
	if !validSemanticAnchor(f.SemanticAnchor) {
		return fmt.Errorf("validate finding semantic anchor: %w", errInvalidValue)
	}
	if len(f.Fingerprint) != 64 || f.Fingerprint != f.ExpectedFingerprint() {
		return fmt.Errorf("validate finding fingerprint: %w", errInvalidValue)
	}
	return nil
}

// ExpectedFingerprint returns the canonical SHA-256 finding identity.
func (f Finding) ExpectedFingerprint() string {
	material := strings.Join([]string{
		SchemaVersion,
		f.RuleID,
		string(f.Layer) + ":" + f.File,
		strconv.Itoa(f.Line),
		f.SemanticAnchor,
	}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
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
	if err := requireStrings(
		namedString{name: "task id", value: a.TaskID},
		namedString{name: "name", value: a.Name},
		namedString{name: "reference", value: a.Reference},
		namedString{name: "digest", value: a.Digest},
		namedString{name: "mime type", value: a.MIMEType},
	); err != nil {
		return fmt.Errorf("validate artifact record: %w", err)
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
	if err := nonnegativeInts(
		namedInt{name: "tool invocations", value: m.ToolInvocations},
		namedInt{name: "permission blocks", value: m.PermissionBlocks},
		namedInt{name: "finding total", value: m.FindingTotal},
		namedInt{name: "warning count", value: m.WarningCount},
		namedInt{name: "human review count", value: m.HumanReviewCount},
	); err != nil {
		return fmt.Errorf("validate metrics: %w", err)
	}
	severities := make([]string, 0, len(m.SeverityCounts))
	for severity := range m.SeverityCounts {
		severities = append(severities, string(severity))
	}
	sort.Strings(severities)
	for _, value := range severities {
		severity := Severity(value)
		count := m.SeverityCounts[severity]
		if !severity.valid() || count < 0 {
			return fmt.Errorf("validate metrics severity %q count %d: %w", severity, count, errInvalidValue)
		}
	}
	errorTypes := make([]string, 0, len(m.ErrorTypeCounts))
	for errorType := range m.ErrorTypeCounts {
		errorTypes = append(errorTypes, errorType)
	}
	sort.Strings(errorTypes)
	for _, errorType := range errorTypes {
		count := m.ErrorTypeCounts[errorType]
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

// Validate checks the report schema, nested records, task identity, and metrics
// consistency.
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
	if r.Input.TaskID != r.Task.ID {
		return fmt.Errorf("validate report input task id: %w", errInvalidValue)
	}
	for index, run := range r.SandboxRuns {
		if err := run.Validate(); err != nil {
			return fmt.Errorf("validate report sandbox run %d: %w", index, err)
		}
		if run.TaskID != r.Task.ID {
			return fmt.Errorf("validate report sandbox run %d task id: %w", index, errInvalidValue)
		}
	}
	for index, decision := range r.GovernanceDecisions {
		if err := decision.Validate(); err != nil {
			return fmt.Errorf("validate report governance decision %d: %w", index, err)
		}
		if decision.TaskID != r.Task.ID {
			return fmt.Errorf("validate report governance decision %d task id: %w", index, errInvalidValue)
		}
	}
	severityCounts := make(map[Severity]int)
	warningCount := 0
	humanReviewCount := 0
	for index, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			return fmt.Errorf("validate report finding %d: %w", index, err)
		}
		if finding.TaskID != r.Task.ID {
			return fmt.Errorf("validate report finding %d task id: %w", index, errInvalidValue)
		}
		severityCounts[finding.Severity]++
		switch finding.Disposition {
		case DispositionWarning:
			warningCount++
		case DispositionNeedsHumanReview:
			humanReviewCount++
		}
	}
	for index, artifact := range r.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("validate report artifact %d: %w", index, err)
		}
		if artifact.TaskID != r.Task.ID {
			return fmt.Errorf("validate report artifact %d task id %q: %w", index, artifact.TaskID, errInvalidValue)
		}
	}
	if err := r.Metrics.Validate(); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	if err := requireString("conclusion", r.Conclusion); err != nil {
		return fmt.Errorf("validate report: %w", err)
	}
	if r.Metrics.FindingTotal != len(r.Findings) {
		return fmt.Errorf("validate report finding total %d: %w", r.Metrics.FindingTotal, errInvalidValue)
	}
	for _, severity := range [...]Severity{
		SeverityCritical,
		SeverityHigh,
		SeverityMedium,
		SeverityLow,
		SeverityInfo,
	} {
		if r.Metrics.SeverityCounts[severity] != severityCounts[severity] {
			return fmt.Errorf("validate report severity %s count %d: %w", severity, r.Metrics.SeverityCounts[severity], errInvalidValue)
		}
	}
	if r.Metrics.WarningCount != warningCount {
		return fmt.Errorf("validate report warning count %d: %w", r.Metrics.WarningCount, errInvalidValue)
	}
	if r.Metrics.HumanReviewCount != humanReviewCount {
		return fmt.Errorf("validate report human review count %d: %w", r.Metrics.HumanReviewCount, errInvalidValue)
	}
	return nil
}

func validateSchema(version string) error {
	if version != SchemaVersion {
		return fmt.Errorf("schema version: %w", errInvalidValue)
	}
	return nil
}

func validVersionedName(value string) bool {
	separator := strings.LastIndex(value, "/v")
	if separator < 1 || separator+2 >= len(value) {
		return false
	}
	if first := value[0]; !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	for _, character := range value[:separator] {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '/' || character == '-') {
			return false
		}
	}
	for _, character := range value[separator+2:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value[separator+2] != '0'
}

func validSemanticAnchor(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == ':' || character == '/' || character == '-') {
			return false
		}
	}
	return true
}

func requireString(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required: %w", name, errInvalidValue)
	}
	return nil
}

func requireIdentifier(name, value string) error {
	if err := requireString(name, value); err != nil {
		return err
	}
	if len(value) > 128 {
		return fmt.Errorf("%s: %w", name, errInvalidValue)
	}
	for index, character := range value {
		valid := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			(index > 0 && (character == '.' || character == '_' || character == ':' || character == '-'))
		if !valid {
			return fmt.Errorf("%s: %w", name, errInvalidValue)
		}
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "sk-") || strings.Contains(lower, "-sk-") {
		return fmt.Errorf("%s: %w", name, errInvalidValue)
	}
	return nil
}

type namedString struct {
	name  string
	value string
}

func requireStrings(values ...namedString) error {
	for _, value := range values {
		if err := requireString(value.name, value.value); err != nil {
			return err
		}
	}
	return nil
}

type namedInt struct {
	name  string
	value int
}

func nonnegativeInts(values ...namedInt) error {
	for _, value := range values {
		if value.value < 0 {
			return fmt.Errorf("%s %d: %w", value.name, value.value, errInvalidValue)
		}
	}
	return nil
}

func (s TaskStatus) valid() bool {
	switch s {
	case TaskStatusPending, TaskStatusRunning, TaskStatusCompleted,
		TaskStatusFailed, TaskStatusCanceled:
		return true
	default:
		return false
	}
}

func (s SandboxStatus) valid() bool {
	switch s {
	case SandboxStatusPending, SandboxStatusRunning, SandboxStatusCompleted,
		SandboxStatusFailed, SandboxStatusCanceled, SandboxStatusSkipped,
		SandboxStatusTimedOut, SandboxStatusUnavailable:
		return true
	default:
		return false
	}
}

func (m Mode) valid() bool {
	switch m {
	case ModeRuleOnly, ModeFakeModel, ModeModel:
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

func (l ChangeLayer) valid() bool {
	switch l {
	case ChangeLayerUnified, ChangeLayerStaged, ChangeLayerWorktree:
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
