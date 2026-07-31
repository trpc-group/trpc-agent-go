//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipGoTestDefaultsAndExplicitOptIn(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		wantSkip bool
	}{
		{
			name:     "default skip",
			args:     []string{"--fixture", "clean", "--dry-run"},
			wantSkip: true,
		},
		{
			name: "explicit opt in",
			args: []string{
				"--fixture", "clean", "--dry-run", "--skip-go-test=false",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, code, err := parseConfig(tt.args, func(string) string { return "" })
			if err != nil || code != 0 {
				t.Fatalf("parseConfig() = code %d, error %v", code, err)
			}
			if cfg.skipGoTest != tt.wantSkip {
				t.Fatalf("skipGoTest = %v, want %v", cfg.skipGoTest, tt.wantSkip)
			}

			code, stdout, stderr := runForTest(t, tt.args, nil, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
			}
			var summary reviewSummary
			mustUnmarshalSummary(t, stdout, &summary)
			if summary.SkipGoTest != tt.wantSkip {
				t.Fatalf("summary.skip_go_test = %v, want %v", summary.SkipGoTest, tt.wantSkip)
			}
			report := readReportFromSummary(t, summary)
			if report.Runtime.SkipGoTest != tt.wantSkip {
				t.Fatalf("report.runtime.skip_go_test = %v, want %v",
					report.Runtime.SkipGoTest, tt.wantSkip)
			}
		})
	}
}

func TestSkipGoTestRecordsSyntheticRunWithoutPermissionOrExecution(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(minimalDiff()))
	for _, tt := range []struct {
		name              string
		enableStaticcheck bool
		wantPlanned       int
		wantCalls         []commandKind
	}{
		{
			name:        "vet remains enabled",
			wantPlanned: 3,
			wantCalls:   []commandKind{commandCheckGoVersion, commandCheckGoVet},
		},
		{
			name:              "staticcheck remains enabled",
			enableStaticcheck: true,
			wantPlanned:       4,
			wantCalls: []commandKind{
				commandCheckGoVersion,
				commandCheckGoVet,
				commandCheckStaticcheck,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingSandboxRunner{}
			factoryCalls := 0
			result, err := runGovernance(
				context.Background(),
				config{
					effectiveRuntime:  runtimeFake,
					skipGoTest:        true,
					enableStaticcheck: tt.enableStaticcheck,
				},
				reviewInput{
					kind:            inputKindRepoPath,
					repoRoot:        t.TempDir(),
					sandboxRepoRoot: t.TempDir(),
				},
				parsed,
				runtimeHooks{sandboxRunnerFactory: func(context.Context) (sandboxRunner, error) {
					factoryCalls++
					return runner, nil
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.CommandsPlanned != tt.wantPlanned ||
				result.CommandsAllowed != len(tt.wantCalls) ||
				result.ToolCalls != len(tt.wantCalls) ||
				len(result.FilterDecisions) != len(tt.wantCalls) ||
				len(result.PermissionDecisions) != len(tt.wantCalls) {
				t.Fatalf("governance counts = %+v, want planned %d and %d executed commands",
					result, tt.wantPlanned, len(tt.wantCalls))
			}
			if len(runner.calls) != len(tt.wantCalls) {
				t.Fatalf("runner calls = %+v, want %v", runner.calls, tt.wantCalls)
			}
			if factoryCalls != len(tt.wantCalls) {
				t.Fatalf("runner factory calls = %d, want %d", factoryCalls, len(tt.wantCalls))
			}
			for i, want := range tt.wantCalls {
				if runner.calls[i].Kind != want {
					t.Fatalf("runner call %d = %s, want %s", i, runner.calls[i].Kind, want)
				}
			}
			if len(result.SandboxRuns) != tt.wantPlanned {
				t.Fatalf("sandbox runs = %+v, want %d planned slots", result.SandboxRuns, tt.wantPlanned)
			}
			skipped := result.SandboxRuns[1]
			if skipped.Command != string(commandCheckGoTest) || !skipped.Skipped ||
				skipped.ExitCode != -1 || skipped.Error != "" ||
				len(skipped.Warnings) != 1 ||
				!strings.Contains(skipped.Warnings[0], "--skip-go-test=false") {
				t.Fatalf("go test skipped run = %+v", skipped)
			}
			if len(result.Matches) != 1 || result.Matches[0].RuleID != ruleSandboxRunSkipped ||
				!strings.Contains(result.Matches[0].Evidence, "--skip-go-test=false") {
				t.Fatalf("go test skip warnings = %+v", result.Matches)
			}
			if finalized := finalizeRuleMatches(result.Matches); !finalized.NeedsHumanReview {
				t.Fatalf("finalized skip warning = %+v, want human review", finalized)
			}
		})
	}
}

func TestSkipGoTestFalseExecutesOriginalCommandPath(t *testing.T) {
	runner := &recordingSandboxRunner{}
	result, err := runGovernance(
		context.Background(),
		config{effectiveRuntime: runtimeFake, skipGoTest: false},
		reviewInput{
			kind:            inputKindRepoPath,
			repoRoot:        t.TempDir(),
			sandboxRepoRoot: t.TempDir(),
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []commandKind{commandCheckGoVersion, commandCheckGoTest, commandCheckGoVet}
	if result.CommandsPlanned != len(want) || result.CommandsAllowed != len(want) ||
		result.ToolCalls != len(want) || len(result.Matches) != 0 || len(runner.calls) != len(want) {
		t.Fatalf("governance = %+v runner calls = %+v", result, runner.calls)
	}
	for i, kind := range want {
		if runner.calls[i].Kind != kind || result.SandboxRuns[i].Skipped {
			t.Fatalf("command %d = %+v run = %+v, want %s executed",
				i, runner.calls[i], result.SandboxRuns[i], kind)
		}
	}
}

func TestSkipGoTestDoesNotWarnWithoutEligibleRepositoryChecks(t *testing.T) {
	runner := &recordingSandboxRunner{}
	result, err := runGovernance(
		context.Background(),
		config{effectiveRuntime: runtimeFake, skipGoTest: true},
		reviewInput{kind: inputKindFixture},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandsPlanned != 1 || len(runner.calls) != 1 ||
		runner.calls[0].Kind != commandCheckGoVersion || len(result.Matches) != 0 {
		t.Fatalf("governance = %+v runner calls = %+v", result, runner.calls)
	}
}

func TestSkipGoTestDoesNotWarnWhenRepositorySnapshotIsUnavailable(t *testing.T) {
	runner := &recordingSandboxRunner{}
	result, err := runGovernance(
		context.Background(),
		config{effectiveRuntime: runtimeFake, skipGoTest: true},
		reviewInput{
			kind:      inputKindRepoPath,
			repoRoot:  t.TempDir(),
			repoFiles: []string{"file.go"},
		},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{sandboxRunner: runner},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandsPlanned != 1 || len(runner.calls) != 1 ||
		runner.calls[0].Kind != commandCheckGoVersion {
		t.Fatalf("governance = %+v runner calls = %+v", result, runner.calls)
	}
	if len(result.Matches) != 1 || result.Matches[0].RuleID != ruleSandboxSnapshotUnavailable {
		t.Fatalf("warnings = %+v, want only snapshot unavailable", result.Matches)
	}
}

func TestSkipGoTestRepositoryReportAndSQLiteRoundTrip(t *testing.T) {
	requireSQLiteDriver(t)

	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/review\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, "review.go"), "package review\n\nconst Value = 1\n")
	mustRunGit(t, repoRoot, "add", "go.mod", "review.go")
	mustCommitGit(t, repoRoot)
	mustWriteFile(t, filepath.Join(repoRoot, "review.go"), "package review\n\nconst Value = 2\n")

	ctx := context.Background()
	store, err := openSQLiteReviewStore(ctx, filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close review store: %v", err)
		}
	})

	runner := &recordingSandboxRunner{}
	code, stdout, stderr := runForTestWithHooks(t, []string{
		"--repo-path", repoRoot,
		"--runtime", runtimeFake,
	}, nil, runGitCommand, runtimeHooks{
		reviewStore:   store,
		sandboxRunner: runner,
		taskID:        "skip-go-test-round-trip",
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if len(runner.calls) != 2 || runner.calls[0].Kind != commandCheckGoVersion ||
		runner.calls[1].Kind != commandCheckGoVet {
		t.Fatalf("runner calls = %+v, want go version and go vet", runner.calls)
	}

	var summary reviewSummary
	mustUnmarshalSummary(t, stdout, &summary)
	if !summary.SkipGoTest || summary.CommandsPlanned != 3 || summary.CommandsAllowed != 2 ||
		summary.Conclusion != reviewConclusionNeedsHumanReview {
		t.Fatalf("summary = %+v", summary)
	}
	report := readReportFromSummary(t, summary)
	assertDefaultGoTestSkipReport(t, report)

	loaded, err := store.LoadReview(ctx, summary.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultGoTestSkipReport(t, loaded)
}

func assertDefaultGoTestSkipReport(t *testing.T, report reviewReport) {
	t.Helper()
	if !report.Runtime.SkipGoTest || len(report.Governance.SandboxRuns) != 3 {
		t.Fatalf("report runtime/governance = %+v/%+v", report.Runtime, report.Governance)
	}
	run := report.Governance.SandboxRuns[1]
	if run.Command != string(commandCheckGoTest) || !run.Skipped || run.ExitCode != -1 {
		t.Fatalf("go test run = %+v", run)
	}
	count := 0
	for _, warning := range report.Warnings {
		if warning.RuleID == ruleSandboxRunSkipped {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("sandbox.run_skipped warnings = %d, report warnings = %+v", count, report.Warnings)
	}
}
