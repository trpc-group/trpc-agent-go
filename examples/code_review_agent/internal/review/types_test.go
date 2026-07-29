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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFindingValidateAcceptsValidFinding(t *testing.T) {
	f := Finding{
		SchemaVersion:  SchemaVersion,
		Severity:       SeverityHigh,
		Category:       "security",
		File:           "internal/server/server.go",
		Line:           42,
		Title:          "unchecked authorization",
		Evidence:       "the handler uses the request before authorization",
		Recommendation: "authorize the request before using it",
		Confidence:     ConfidenceHigh,
		Source:         SourceRule,
		RuleID:         "security/authorization",
		Fingerprint:    "review/v1:example",
		Disposition:    DispositionFinding,
	}

	require.NoError(t, f.Validate())
}

func TestFindingValidateRejectsUnknownEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Finding)
	}{
		{name: "severity", mutate: func(f *Finding) { f.Severity = "urgent" }},
		{name: "confidence", mutate: func(f *Finding) { f.Confidence = "certain" }},
		{name: "source", mutate: func(f *Finding) { f.Source = "scanner" }},
		{name: "disposition", mutate: func(f *Finding) { f.Disposition = "accepted" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validFinding()
			tt.mutate(&f)
			require.Error(t, f.Validate())
		})
	}
}

func TestTaskValidateUsesClosedStatusAndPhaseEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{name: "unknown status", mutate: func(task *Task) { task.Status = "paused" }},
		{name: "unknown phase", mutate: func(task *Task) { task.Phase = "publishing" }},
		{name: "unknown mode", mutate: func(task *Task) { task.Mode = "automatic" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := validTask()
			tt.mutate(&task)
			require.Error(t, task.Validate())
		})
	}
}

func TestTaskValidateLifecycleInvariants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{name: "zero created at", mutate: func(task *Task) { task.CreatedAt = time.Time{} }},
		{name: "zero updated at", mutate: func(task *Task) { task.UpdatedAt = time.Time{} }},
		{name: "updated before created", mutate: func(task *Task) { task.UpdatedAt = task.CreatedAt.Add(-time.Second) }},
		{name: "completed status before completed phase", mutate: func(task *Task) { task.Status = TaskStatusCompleted }},
		{name: "completed phase before completed status", mutate: func(task *Task) { task.Phase = PhaseCompleted }},
		{name: "pending beyond created phase", mutate: func(task *Task) { task.Status = TaskStatusPending }},
		{name: "running in created phase", mutate: func(task *Task) { task.Phase = PhaseCreated }},
		{name: "completed with terminal error", mutate: func(task *Task) {
			task.Status = TaskStatusCompleted
			task.Phase = PhaseCompleted
			task.TerminalError = "unexpected failure"
		}},
		{name: "failed without terminal error", mutate: func(task *Task) { task.Status = TaskStatusFailed }},
		{name: "canceled without terminal error", mutate: func(task *Task) { task.Status = TaskStatusCanceled }},
		{name: "running with terminal error", mutate: func(task *Task) { task.TerminalError = "unexpected failure" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := validTask()
			tt.mutate(&task)
			require.Error(t, task.Validate())
		})
	}
}

func TestTaskValidateAcceptsSupportedModesAndTerminalStates(t *testing.T) {
	for _, mode := range []Mode{ModeRuleOnly, ModeFakeModel, ModeModel} {
		t.Run(string(mode), func(t *testing.T) {
			task := validTask()
			task.Mode = mode
			require.NoError(t, task.Validate())
		})
	}

	completed := validTask()
	completed.Status = TaskStatusCompleted
	completed.Phase = PhaseCompleted
	require.NoError(t, completed.Validate())

	failed := validTask()
	failed.Status = TaskStatusFailed
	failed.TerminalError = "review failed"
	require.NoError(t, failed.Validate())

	canceled := validTask()
	canceled.Status = TaskStatusCanceled
	canceled.TerminalError = "review canceled"
	require.NoError(t, canceled.Validate())

	pending := validTask()
	pending.Status = TaskStatusPending
	pending.Phase = PhaseCreated
	require.NoError(t, pending.Validate())
}

func TestGovernanceDecisionValidateUsesClosedEnums(t *testing.T) {
	decision := GovernanceDecision{
		SchemaVersion: SchemaVersion,
		Kind:          DecisionKindPermission,
		Tool:          "go_test",
		Action:        DecisionActionAllow,
		Reason:        "fixed command is allowed",
		Rule:          "allow-go-test",
	}
	require.NoError(t, decision.Validate())

	decision.Kind = "approval"
	require.Error(t, decision.Validate())

	decision.Kind = DecisionKindPermission
	decision.Action = "prompt"
	require.Error(t, decision.Validate())
}

func TestSandboxRunValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SandboxRun)
		wantErr bool
	}{
		{name: "completed", mutate: func(run *SandboxRun) {}},
		{name: "failed", mutate: func(run *SandboxRun) {
			run.Status = SandboxStatusFailed
			run.ExitCode = intPointer(1)
		}},
		{name: "timed out", mutate: func(run *SandboxRun) {
			run.Status = SandboxStatusTimedOut
			run.ExitCode = nil
			run.TimedOut = true
		}},
		{name: "skipped", mutate: func(run *SandboxRun) {
			run.Status = SandboxStatusSkipped
			run.ExitCode = nil
		}},
		{name: "negative duration", mutate: func(run *SandboxRun) { run.Duration = -time.Second }, wantErr: true},
		{name: "completed without exit code", mutate: func(run *SandboxRun) { run.ExitCode = nil }, wantErr: true},
		{name: "completed with nonzero exit", mutate: func(run *SandboxRun) { run.ExitCode = intPointer(1) }, wantErr: true},
		{name: "failed without exit code", mutate: func(run *SandboxRun) {
			run.Status = SandboxStatusFailed
			run.ExitCode = nil
		}, wantErr: true},
		{name: "failed with zero exit", mutate: func(run *SandboxRun) { run.Status = SandboxStatusFailed }, wantErr: true},
		{name: "skipped with exit code", mutate: func(run *SandboxRun) { run.Status = SandboxStatusSkipped }, wantErr: true},
		{name: "timed out status without flag", mutate: func(run *SandboxRun) {
			run.Status = SandboxStatusTimedOut
			run.ExitCode = nil
		}, wantErr: true},
		{name: "timeout flag without status", mutate: func(run *SandboxRun) { run.TimedOut = true }, wantErr: true},
		{name: "unknown status", mutate: func(run *SandboxRun) { run.Status = "aborted" }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := validSandboxRun()
			tt.mutate(&run)
			if tt.wantErr {
				require.Error(t, run.Validate())
				return
			}
			require.NoError(t, run.Validate())
		})
	}
}

func TestSandboxRunExitCodeJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name             string
		run              SandboxRun
		wantExitCode     *int
		wantExitCodeJSON bool
	}{
		{
			name:             "omitted",
			run:              SandboxRun{Status: SandboxStatusSkipped},
			wantExitCode:     nil,
			wantExitCodeJSON: false,
		},
		{
			name:             "zero",
			run:              SandboxRun{Status: SandboxStatusCompleted, ExitCode: intPointer(0)},
			wantExitCode:     intPointer(0),
			wantExitCodeJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.run)
			require.NoError(t, err)
			if tt.wantExitCodeJSON {
				require.Contains(t, string(data), `"exit_code":0`)
			} else {
				require.NotContains(t, string(data), `"exit_code"`)
			}

			var decoded SandboxRun
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Equal(t, tt.wantExitCode, decoded.ExitCode)
		})
	}
}

func TestMetricsValidateSchemaVersion(t *testing.T) {
	metrics := Metrics{SchemaVersion: SchemaVersion}
	require.NoError(t, metrics.Validate())

	metrics.SchemaVersion = "review/v2"
	require.Error(t, metrics.Validate())
}

func TestReviewInputValidate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*ReviewInput)
		wantError string
	}{
		{name: "valid", mutate: func(input *ReviewInput) {}},
		{name: "unknown schema", mutate: func(input *ReviewInput) { input.SchemaVersion = "review/v2" }, wantError: "schema version"},
		{name: "missing task id", mutate: func(input *ReviewInput) { input.TaskID = "" }, wantError: "task id"},
		{name: "unknown source", mutate: func(input *ReviewInput) { input.Source = "remote" }, wantError: "source"},
		{name: "missing digest", mutate: func(input *ReviewInput) { input.Digest = "" }, wantError: "digest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validReviewInput()
			tt.mutate(&input)
			if tt.wantError == "" {
				require.NoError(t, input.Validate())
				return
			}
			require.ErrorContains(t, input.Validate(), tt.wantError)
		})
	}
}

func TestArtifactRecordValidate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*ArtifactRecord)
		wantError string
	}{
		{name: "valid", mutate: func(artifact *ArtifactRecord) {}},
		{name: "unknown schema", mutate: func(artifact *ArtifactRecord) { artifact.SchemaVersion = "review/v2" }, wantError: "schema version"},
		{name: "missing task id", mutate: func(artifact *ArtifactRecord) { artifact.TaskID = "" }, wantError: "task id"},
		{name: "missing name", mutate: func(artifact *ArtifactRecord) { artifact.Name = "" }, wantError: "name"},
		{name: "missing reference", mutate: func(artifact *ArtifactRecord) { artifact.Reference = "" }, wantError: "reference"},
		{name: "missing digest", mutate: func(artifact *ArtifactRecord) { artifact.Digest = "" }, wantError: "digest"},
		{name: "missing MIME type", mutate: func(artifact *ArtifactRecord) { artifact.MIMEType = "" }, wantError: "mime type"},
		{name: "negative size", mutate: func(artifact *ArtifactRecord) { artifact.Size = -1 }, wantError: "size"},
		{name: "ordered required fields", mutate: func(artifact *ArtifactRecord) {
			artifact.TaskID = ""
			artifact.Name = ""
			artifact.Reference = ""
			artifact.Digest = ""
			artifact.MIMEType = ""
		}, wantError: "task id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := validArtifactRecord()
			tt.mutate(&artifact)
			if tt.wantError == "" {
				require.NoError(t, artifact.Validate())
				return
			}
			require.ErrorContains(t, artifact.Validate(), tt.wantError)
		})
	}
}

func TestReportValidate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Report)
		wantError string
	}{
		{name: "valid", mutate: func(report *Report) {}},
		{name: "unknown schema", mutate: func(report *Report) { report.SchemaVersion = "review/v2" }, wantError: "schema version"},
		{name: "invalid input", mutate: func(report *Report) { report.Input.Digest = "" }, wantError: "review input"},
		{name: "missing conclusion", mutate: func(report *Report) { report.Conclusion = "" }, wantError: "conclusion"},
		{name: "input task mismatch", mutate: func(report *Report) { report.Input.TaskID = "task-2" }, wantError: "input task id"},
		{name: "sandbox task mismatch", mutate: func(report *Report) { report.SandboxRuns[0].TaskID = "task-2" }, wantError: "sandbox run 0 task id"},
		{name: "decision task mismatch", mutate: func(report *Report) { report.GovernanceDecisions[0].TaskID = "task-2" }, wantError: "governance decision 0 task id"},
		{name: "finding task mismatch", mutate: func(report *Report) { report.Findings[0].TaskID = "task-2" }, wantError: "finding 0 task id"},
		{name: "artifact task mismatch", mutate: func(report *Report) { report.Artifacts[0].TaskID = "task-2" }, wantError: "artifact 0 task id"},
		{name: "finding total mismatch", mutate: func(report *Report) { report.Metrics.FindingTotal-- }, wantError: "finding total"},
		{name: "severity count mismatch", mutate: func(report *Report) { report.Metrics.SeverityCounts[SeverityHigh]-- }, wantError: "severity high"},
		{name: "warning count mismatch", mutate: func(report *Report) { report.Metrics.WarningCount-- }, wantError: "warning count"},
		{name: "human review count mismatch", mutate: func(report *Report) { report.Metrics.HumanReviewCount-- }, wantError: "human review count"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := validReport()
			tt.mutate(&report)
			if tt.wantError == "" {
				require.NoError(t, report.Validate())
				return
			}
			require.ErrorContains(t, report.Validate(), tt.wantError)
		})
	}
}

func validTask() Task {
	createdAt := time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	return Task{
		SchemaVersion: SchemaVersion,
		ID:            "task-1",
		Status:        TaskStatusRunning,
		Phase:         PhaseRules,
		Mode:          ModeRuleOnly,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt.Add(time.Second),
	}
}

func validSandboxRun() SandboxRun {
	return SandboxRun{
		SchemaVersion: SchemaVersion,
		TaskID:        "task-1",
		Command:       "go test",
		Status:        SandboxStatusCompleted,
		Duration:      time.Second,
		ExitCode:      intPointer(0),
	}
}

func validReviewInput() ReviewInput {
	return ReviewInput{
		SchemaVersion: SchemaVersion,
		TaskID:        "task-1",
		Source:        InputSourceDiffFile,
		Digest:        "sha256:input",
		ChangedFiles:  []string{"main.go"},
	}
}

func validArtifactRecord() ArtifactRecord {
	return ArtifactRecord{
		SchemaVersion: SchemaVersion,
		TaskID:        "task-1",
		Name:          "review_report.json",
		Reference:     "artifact://review_report.json/1",
		Digest:        "sha256:report",
		MIMEType:      "application/json",
		Size:          128,
	}
}

func validReport() Report {
	task := validTask()
	input := validReviewInput()
	run := validSandboxRun()
	decision := GovernanceDecision{
		SchemaVersion: SchemaVersion,
		TaskID:        task.ID,
		Kind:          DecisionKindPermission,
		Tool:          "go_test",
		Action:        DecisionActionAllow,
		Reason:        "fixed command is allowed",
		Rule:          "allow-go-test",
	}
	artifact := validArtifactRecord()

	high := validFinding()
	high.TaskID = task.ID
	warning := validFinding()
	warning.TaskID = task.ID
	warning.Severity = SeverityLow
	warning.Disposition = DispositionWarning
	humanReview := validFinding()
	humanReview.TaskID = task.ID
	humanReview.Severity = SeverityMedium
	humanReview.Disposition = DispositionNeedsHumanReview

	return Report{
		SchemaVersion:       SchemaVersion,
		Task:                task,
		Input:               input,
		SandboxRuns:         []SandboxRun{run},
		GovernanceDecisions: []GovernanceDecision{decision},
		Findings:            []Finding{high, warning, humanReview},
		Artifacts:           []ArtifactRecord{artifact},
		Metrics: Metrics{
			SchemaVersion: SchemaVersion,
			FindingTotal:  3,
			SeverityCounts: map[Severity]int{
				SeverityHigh:   1,
				SeverityMedium: 1,
				SeverityLow:    1,
			},
			WarningCount:     1,
			HumanReviewCount: 1,
		},
		Conclusion: "review completed",
	}
}

func intPointer(value int) *int {
	return &value
}

func validFinding() Finding {
	return Finding{
		SchemaVersion:  SchemaVersion,
		Severity:       SeverityHigh,
		Category:       "security",
		File:           "internal/server/server.go",
		Line:           42,
		Title:          "unchecked authorization",
		Evidence:       "the handler uses the request before authorization",
		Recommendation: "authorize the request before using it",
		Confidence:     ConfidenceHigh,
		Source:         SourceRule,
		RuleID:         "security/authorization",
		Fingerprint:    "review/v1:example",
		Disposition:    DispositionFinding,
	}
}
