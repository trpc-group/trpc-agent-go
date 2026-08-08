//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"embed"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

//go:embed testdata/fixtures/*.patch testdata/holdout/*.patch
var fixtureFS embed.FS

func TestAcceptancePublicFixturesRunCompletePipeline(t *testing.T) {
	started := time.Now()
	fixtures := []struct {
		name        string
		wantRule    string
		sandboxFail bool
		duplicate   bool
	}{
		{name: "clean.patch"},
		{name: "security.patch", wantRule: rules.RuleHardcodedSecret},
		{name: "goroutine.patch", wantRule: rules.RuleGoroutineLifetime},
		{name: "resource.patch", wantRule: rules.RuleResourceClose},
		{name: "transaction.patch", wantRule: rules.RuleTransactionLifecycle},
		{name: "error_handling.patch", wantRule: rules.RuleIgnoredError},
		{name: "missing_test.patch", wantRule: rules.RuleMissingTests},
		{name: "duplicate.patch", wantRule: rules.RuleHardcodedSecret, duplicate: true},
		{name: "sandbox_failure.patch", sandboxFail: true},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			loaded, err := input.Load(ctx, input.Selection{Fixture: fixture.name},
				input.WithFixtureFS(fixtureFS, "testdata/fixtures"))
			require.NoError(t, err)
			loaded.Snapshots = snapshotsFromDiff(t, loaded.Diff)
			taskID := "fixture-" + strings.TrimSuffix(strings.ReplaceAll(fixture.name, "_", "-"), ".patch")
			databasePath := filepath.Join(t.TempDir(), "reviews.db")
			reviewStore, err := store.NewSQLiteStore(ctx, databasePath)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reviewStore.Close()) })
			sandboxResult := acceptanceSandboxResult(taskID, fixture.sandboxFail)
			if fixture.duplicate {
				candidates, reviewErr := (rules.Engine{}).Review(
					loaded.Diff, acceptanceSnapshots(loaded.Snapshots))
				require.NoError(t, reviewErr)
				for _, candidate := range candidates {
					if candidate.RuleID == rules.RuleHardcodedSecret {
						sandboxResult.Candidates = append(sandboxResult.Candidates, candidate)
						break
					}
				}
			}
			outputDirectory := t.TempDir()
			now := acceptanceClock()
			pipeline, err := orchestrator.New(orchestrator.Config{
				TaskID: taskID, Mode: review.ModeRuleOnly, Store: reviewStore,
				Load:  func(context.Context) (input.Loaded, error) { return loaded, nil },
				Rules: rules.Engine{}, Sandbox: acceptanceSandbox{result: sandboxResult},
				Artifacts: inmemory.NewService(),
				ArtifactSession: artifact.SessionInfo{
					AppName: "code-review", UserID: "local", SessionID: taskID,
				},
				OutputDirectory: outputDirectory,
				Now:             now,
			})
			require.NoError(t, err)
			result, err := pipeline.Run(ctx)
			require.NoError(t, err)
			require.Equal(t, review.TaskStatusCompleted, result.Stored.Report.Task.Status)
			require.Len(t, result.Stored.PublicationArtifacts, 2)
			require.FileExists(t, filepath.Join(outputDirectory, report.JSONName))
			require.FileExists(t, filepath.Join(outputDirectory, report.MarkdownName))
			if fixture.wantRule != "" {
				require.Contains(t, findingRuleIDs(result.Stored.Report.Findings), fixture.wantRule)
			}
			if fixture.duplicate {
				require.Equal(t, 1, countRule(result.Stored.Report.Findings, fixture.wantRule))
			}
			if fixture.sandboxFail {
				require.Equal(t, 1,
					result.Stored.Report.Metrics.ErrorTypeCounts["sandbox_failed"])
			}
			jsonBytes := string(result.Document.JSON)
			require.NotContains(t, jsonBytes, "sk-test-public-fixture-secret-1234567890")
			require.NotContains(t, jsonBytes, "fixture-password-value-123456")
		})
	}
	require.Less(t, time.Since(started), 2*time.Minute)
}

func TestAcceptanceRedactionRate(t *testing.T) {
	secrets := []string{
		`api_key="sk-test-redaction-value-1234567890"`,
		`password=hunter2`,
		`Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456`,
		`redis://user:password@example.com/0`,
		`mysql:secret@tcp(localhost:3306)/db`,
		`-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----`,
	}
	detected := 0
	for _, secret := range secrets {
		if safe := redact.String(secret); safe != secret && strings.Contains(safe, "[REDACTED:") {
			detected++
		}
	}
	rate := float64(detected) / float64(len(secrets))
	require.GreaterOrEqual(t, rate, 0.95)
}

func TestHoldoutRecallAndFalsePositiveThresholds(t *testing.T) {
	cases := []struct {
		name     string
		wantRule string
		highRisk bool
	}{
		{name: "credential.patch", wantRule: rules.RuleHardcodedSecret, highRisk: true},
		{name: "shell.patch", wantRule: rules.RuleDangerousCommand, highRisk: true},
		{name: "clean_secret.patch"},
		{name: "clean_shell.patch"},
		{name: "clean_context.patch"},
		{name: "clean_resource.patch"},
		{name: "clean_transaction.patch"},
	}
	highRiskTotal := 0
	highRiskDetected := 0
	cleanTotal := 0
	falsePositives := 0
	for _, test := range cases {
		loaded, err := input.Load(context.Background(), input.Selection{Fixture: test.name},
			input.WithFixtureFS(fixtureFS, "testdata/holdout"))
		require.NoError(t, err)
		loaded.Snapshots = snapshotsFromDiff(t, loaded.Diff)
		candidates, err := (rules.Engine{}).Review(
			loaded.Diff, acceptanceSnapshots(loaded.Snapshots))
		require.NoError(t, err)
		canonical, err := findings.Normalize(
			"holdout-"+strings.TrimSuffix(strings.ReplaceAll(test.name, "_", "-"), ".patch"),
			loaded.Diff,
			candidates,
		)
		require.NoError(t, err)
		if test.highRisk {
			highRiskTotal++
			for _, finding := range canonical {
				if finding.RuleID == test.wantRule &&
					(finding.Severity == review.SeverityHigh ||
						finding.Severity == review.SeverityCritical) {
					highRiskDetected++
					break
				}
			}
			continue
		}
		cleanTotal++
		for _, finding := range canonical {
			if finding.Disposition == review.DispositionFinding {
				falsePositives++
				break
			}
		}
	}
	recall := float64(highRiskDetected) / float64(highRiskTotal)
	falsePositiveRate := float64(falsePositives) / float64(cleanTotal)
	require.GreaterOrEqual(t, recall, 0.80)
	require.LessOrEqual(t, falsePositiveRate, 0.15)
}

type acceptanceSandbox struct {
	result sandbox.Result
}

func (s acceptanceSandbox) Run(context.Context, sandbox.Request) (sandbox.Result, error) {
	return s.result, nil
}

func acceptanceSandboxResult(taskID string, failed bool) sandbox.Result {
	exitCode := 0
	status := review.SandboxStatusCompleted
	if failed {
		exitCode = 1
		status = review.SandboxStatusFailed
	}
	return sandbox.Result{Runs: []review.SandboxRun{{
		SchemaVersion: review.SchemaVersion,
		TaskID:        taskID, Command: "go test ./...", Status: status,
		Duration: 10 * time.Millisecond, ExitCode: &exitCode,
	}}}
}

func snapshotsFromDiff(t *testing.T, diff input.Diff) []input.Snapshot {
	t.Helper()
	var snapshots []input.Snapshot
	for _, file := range diff.Files {
		if file.NewPath == "" || file.Binary {
			continue
		}
		lines := make(map[int]string)
		maximum := 0
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.NewNumber == nil || line.Kind == input.LineDeleted {
					continue
				}
				lines[*line.NewNumber] = line.Text
				if *line.NewNumber > maximum {
					maximum = *line.NewNumber
				}
			}
		}
		var source strings.Builder
		for line := 1; line <= maximum; line++ {
			text, exists := lines[line]
			require.Truef(t, exists, "fixture %s must include complete source line %d", file.NewPath, line)
			source.WriteString(text)
			source.WriteByte('\n')
		}
		layer := file.Layer
		if layer == "" {
			layer = review.ChangeLayerUnified
		}
		snapshots = append(snapshots, input.Snapshot{
			Layer: layer, Path: file.NewPath, Content: []byte(source.String()),
		})
	}
	return snapshots
}

func acceptanceSnapshots(source []input.Snapshot) rules.Snapshots {
	result := make(rules.Snapshots, len(source))
	for _, snapshot := range source {
		result[rules.SnapshotKey{Layer: snapshot.Layer, Path: snapshot.Path}] = snapshot.Content
	}
	return result
}

func acceptanceClock() func() time.Time {
	next := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		result := next
		next = next.Add(time.Millisecond)
		return result
	}
}

func findingRuleIDs(values []review.Finding) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.RuleID)
	}
	sort.Strings(result)
	return result
}

func countRule(values []review.Finding, ruleID string) int {
	count := 0
	for _, value := range values {
		if value.RuleID == ruleID {
			count++
		}
	}
	return count
}
