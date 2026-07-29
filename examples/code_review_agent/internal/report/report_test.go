//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestFinalizeIsDeterministicAndRedactsText(t *testing.T) {
	report := validReport()
	report.Conclusion = `review completed token="sk-test-super-secret-value-123456"`
	first, err := Finalize(report)
	require.NoError(t, err)
	second, err := Finalize(report)
	require.NoError(t, err)
	require.Equal(t, first.JSON, second.JSON)
	require.Equal(t, first.Markdown, second.Markdown)
	require.NotContains(t, string(first.JSON), "sk-test-super-secret-value-123456")
	require.NotContains(t, string(first.Markdown), "sk-test-super-secret-value-123456")
	require.Equal(t, byte('\n'), first.JSON[len(first.JSON)-1])
	require.Contains(t, string(first.Markdown), "# Code Review Report")
}

func TestFinalizeDoesNotMutateInputAndSortsFindings(t *testing.T) {
	report := validReport()
	secondFinding := report.Findings[0]
	secondFinding.Line = 1
	secondFinding.SemanticAnchor = "earlier-location"
	secondFinding.Fingerprint = secondFinding.ExpectedFingerprint()
	report.Findings = []review.Finding{report.Findings[0], secondFinding}
	report.Metrics.FindingTotal = 2
	report.Metrics.SeverityCounts[review.SeverityHigh] = 2
	original := append([]review.Finding(nil), report.Findings...)
	document, err := Finalize(report)
	require.NoError(t, err)
	require.Equal(t, original, report.Findings)
	require.Equal(t, 1, document.Report.Findings[0].Line)
}

func TestPublishPinsArtifactRevisionsAndDigest(t *testing.T) {
	document, err := Finalize(validReport())
	require.NoError(t, err)
	service := inmemory.NewService()
	session := artifact.SessionInfo{AppName: "code-review", UserID: "local", SessionID: "task-1"}
	first, err := Publish(context.Background(), service, session, "task-1", document)
	require.NoError(t, err)
	second, err := Publish(context.Background(), service, session, "task-1", document)
	require.NoError(t, err)
	require.Contains(t, first.Artifacts[0].Reference, "revision=0")
	require.Contains(t, second.Artifacts[0].Reference, "revision=1")
	digest := sha256.Sum256(document.JSON)
	require.Equal(t, hex.EncodeToString(digest[:]), first.Metadata.Digest)
	require.Equal(t, first.Artifacts[0].Digest, first.Metadata.Digest)
}

func TestPublishRejectsForgedDocumentBytes(t *testing.T) {
	document, err := Finalize(validReport())
	require.NoError(t, err)
	document.JSON = append([]byte(nil), document.JSON...)
	document.JSON[0] = '['
	_, err = Publish(
		context.Background(),
		inmemory.NewService(),
		artifact.SessionInfo{AppName: "code-review", UserID: "local", SessionID: "task-1"},
		"task-1",
		document,
	)
	require.ErrorContains(t, err, "not canonical")
}

func TestWriteLocalAtomicallyReplacesReports(t *testing.T) {
	document, err := Finalize(validReport())
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, WriteLocal(directory, document))
	require.NoError(t, os.WriteFile(filepath.Join(directory, JSONName), []byte("old"), 0o600))
	require.NoError(t, WriteLocal(directory, document))
	jsonBytes, err := os.ReadFile(filepath.Join(directory, JSONName))
	require.NoError(t, err)
	require.Equal(t, document.JSON, jsonBytes)
	matches, err := filepath.Glob(filepath.Join(directory, ".review-report-*"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestWriteLocalRejectsForgedDocument(t *testing.T) {
	document, err := Finalize(validReport())
	require.NoError(t, err)
	document.Markdown = []byte("secret password=hunter2")
	err = WriteLocal(t.TempDir(), document)
	require.ErrorContains(t, err, "not canonical")
}

func TestWriteLocalRestoresJSONWhenMarkdownReplacementFails(t *testing.T) {
	document, err := Finalize(validReport())
	require.NoError(t, err)
	directory := t.TempDir()
	jsonPath := filepath.Join(directory, JSONName)
	markdownPath := filepath.Join(directory, MarkdownName)
	require.NoError(t, os.WriteFile(jsonPath, []byte("old-json"), 0o600))
	require.NoError(t, os.Mkdir(markdownPath, 0o700))
	err = WriteLocal(directory, document)
	require.Error(t, err)
	jsonBytes, readErr := os.ReadFile(jsonPath)
	require.NoError(t, readErr)
	require.Equal(t, []byte("old-json"), jsonBytes)
}

func TestFinalizeEscapesMarkdownLinksAndImages(t *testing.T) {
	report := validReport()
	report.Conclusion = `![track](https://attacker.example/pixel) [click](javascript:alert(1))`
	report.Findings[0].Title = `[title](https://attacker.example)`
	report.Findings[0].Evidence = `![evidence](https://attacker.example/e)`
	document, err := Finalize(report)
	require.NoError(t, err)
	markdown := string(document.Markdown)
	require.NotContains(t, markdown, "](")
	require.NotContains(t, markdown, "![")
}

func TestFinalizeMarkdownIncludesGovernanceMetricsAndSandboxSummaries(t *testing.T) {
	value := validReport()
	exitCode := 1
	value.SandboxRuns = []review.SandboxRun{{
		SchemaVersion: review.SchemaVersion,
		TaskID:        "task-1",
		Command:       "go test ./...",
		Status:        review.SandboxStatusFailed,
		Duration:      time.Second,
		ExitCode:      &exitCode,
	}}
	value.GovernanceDecisions = []review.GovernanceDecision{{
		SchemaVersion: review.SchemaVersion,
		TaskID:        "task-1",
		DecisionID:    "decision-1",
		Kind:          review.DecisionKindPermission,
		Tool:          "workspace_exec",
		Action:        review.DecisionActionDeny,
		Reason:        "command is not allowed",
		Rule:          "deny-command",
	}}
	value.Metrics.TotalDuration = 2 * time.Second
	value.Metrics.SandboxDuration = time.Second
	value.Metrics.ToolInvocations = 1
	value.Metrics.PermissionBlocks = 1
	value.Metrics.ErrorTypeCounts = map[string]int{"sandbox_failed": 1}
	document, err := Finalize(value)
	require.NoError(t, err)
	markdown := string(document.Markdown)
	for _, heading := range []string{"## Monitoring Summary", "## Sandbox Runs", "## Governance Decisions"} {
		require.Contains(t, markdown, heading)
	}
	require.Contains(t, markdown, "go test")
	require.Contains(t, markdown, "workspace&#95;exec")
}

func TestFinalizeRejectsDuplicateAndOutOfScopeFindings(t *testing.T) {
	t.Run("duplicate fingerprint", func(t *testing.T) {
		report := validReport()
		report.Findings = append(report.Findings, report.Findings[0])
		report.Metrics.FindingTotal = 2
		report.Metrics.SeverityCounts[review.SeverityHigh] = 2
		_, err := Finalize(report)
		require.ErrorContains(t, err, "duplicate fingerprint")
	})
	t.Run("file outside changed set", func(t *testing.T) {
		report := validReport()
		report.Findings[0].File = "other.go"
		report.Findings[0].Fingerprint = report.Findings[0].ExpectedFingerprint()
		_, err := Finalize(report)
		require.ErrorContains(t, err, "changed files")
	})
}

func TestFinalizeRejectsSecretBearingIdentityAndOversizedCollections(t *testing.T) {
	t.Run("changed file identity", func(t *testing.T) {
		report := validReport()
		report.Input.ChangedFiles = []string{"password=hunter2.go"}
		report.Findings[0].File = "password=hunter2.go"
		report.Findings[0].Fingerprint = report.Findings[0].ExpectedFingerprint()
		_, err := Finalize(report)
		require.ErrorContains(t, err, "identity")
	})
	t.Run("finding count", func(t *testing.T) {
		report := validReport()
		report.Findings = make([]review.Finding, 5001)
		_, err := Finalize(report)
		require.ErrorContains(t, err, "finding limit")
	})
}

func TestPublishReturnsPartialStateAndValidatesSessionIdentity(t *testing.T) {
	document, err := Finalize(validReport())
	require.NoError(t, err)
	service := &failingArtifactService{Service: inmemory.NewService(), failName: MarkdownName}
	session := artifact.SessionInfo{AppName: "code-review", UserID: "local", SessionID: "task-1"}
	partial, err := Publish(context.Background(), service, session, "task-1", document)
	require.Error(t, err)
	require.Len(t, partial.Artifacts, 1)
	require.Equal(t, JSONName, partial.Artifacts[0].Name)

	_, err = Publish(
		context.Background(),
		inmemory.NewService(),
		artifact.SessionInfo{AppName: "code/review", UserID: "local", SessionID: "task-2"},
		"task-1",
		document,
	)
	require.ErrorContains(t, err, "artifact identity")
}

type failingArtifactService struct {
	artifact.Service
	failName string
}

func (s *failingArtifactService) SaveArtifact(
	ctx context.Context,
	session artifact.SessionInfo,
	name string,
	value *artifact.Artifact,
) (int, error) {
	if name == s.failName {
		return 0, errors.New("artifact save failed")
	}
	return s.Service.SaveArtifact(ctx, session, name, value)
}

func validReport() review.Report {
	created := time.Date(2026, time.July, 29, 1, 2, 3, 0, time.UTC)
	finding := review.Finding{
		SchemaVersion:  review.SchemaVersion,
		TaskID:         "task-1",
		Severity:       review.SeverityHigh,
		Category:       "security",
		Layer:          review.ChangeLayerUnified,
		File:           "internal/server.go",
		Line:           42,
		SemanticAnchor: "authorization-before-use",
		Title:          "authorize before use",
		Evidence:       "request data is used before authorization",
		Recommendation: "authorize the request first",
		Confidence:     review.ConfidenceHigh,
		Source:         review.SourceRule,
		RuleID:         "security/authorization/v1",
		Disposition:    review.DispositionFinding,
	}
	finding.Fingerprint = finding.ExpectedFingerprint()
	return review.Report{
		SchemaVersion: review.SchemaVersion,
		Task: review.Task{
			SchemaVersion: review.SchemaVersion,
			ID:            "task-1",
			Status:        review.TaskStatusCompleted,
			Phase:         review.PhaseCompleted,
			Mode:          review.ModeRuleOnly,
			CreatedAt:     created,
			UpdatedAt:     created.Add(time.Second),
		},
		Input: review.ReviewInput{
			SchemaVersion: review.SchemaVersion,
			TaskID:        "task-1",
			Source:        review.InputSourceDiffFile,
			Digest:        "0123456789abcdef",
			ChangedFiles:  []string{"internal/server.go"},
		},
		Findings: []review.Finding{finding},
		Metrics: review.Metrics{
			SchemaVersion:   review.SchemaVersion,
			FindingTotal:    1,
			SeverityCounts:  map[review.Severity]int{review.SeverityHigh: 1},
			ErrorTypeCounts: map[string]int{},
		},
		Conclusion: "review completed",
	}
}
