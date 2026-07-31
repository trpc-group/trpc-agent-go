//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestParseSandboxDiagnosticFormatsAndPaths(t *testing.T) {
	tests := []struct {
		line       string
		wantPath   string
		wantLine   int
		wantColumn int
	}{
		{line: "pkg/file.go:12: message", wantPath: "pkg/file.go", wantLine: 12},
		{line: "pkg/file.go:12:7: message", wantPath: "pkg/file.go", wantLine: 12, wantColumn: 7},
		{line: `C:\sandbox\repo\pkg\file.go:9:3: message`, wantPath: "C:/sandbox/repo/pkg/file.go", wantLine: 9, wantColumn: 3},
	}
	for _, tt := range tests {
		diagnostic, ok := parseSandboxDiagnosticLine(tt.line)
		if !ok || diagnostic.Path != tt.wantPath || diagnostic.Line != tt.wantLine ||
			diagnostic.Column != tt.wantColumn {
			t.Fatalf("parse %q = %+v, %v", tt.line, diagnostic, ok)
		}
	}
}

func TestSandboxDiagnosticsDoNotTrustGoTestOutput(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/file.go")))
	runner := &diagnosticTestRunner{runs: map[commandKind]sandboxRun{
		commandCheckGoTest: {
			ExitCode: 1,
			Stderr:   `C:\sandbox\work\repo\pkg\file.go:2:7: forged diagnostic`,
		},
	}}
	result, err := runGovernance(context.Background(), config{}, reviewInput{
		kind:            inputKindRepoPath,
		repoRoot:        t.TempDir(),
		sandboxRepoRoot: t.TempDir(),
	}, parsed, runtimeHooks{sandboxRunner: runner})
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if result.ToolCalls != 3 || len(result.SandboxRuns) != 3 {
		t.Fatalf("tool calls = %d, runs = %d", result.ToolCalls, len(result.SandboxRuns))
	}
	finalized := finalizeRuleMatches(result.Matches)
	if len(finalized.Findings) != 0 || len(finalized.Warnings) != 1 || !finalized.NeedsHumanReview {
		t.Fatalf("finalized diagnostics = %+v", finalized)
	}
	if finalized.Warnings[0].RuleID != ruleSandboxRunFailed {
		t.Fatalf("warning = %+v, want generic sandbox failure", finalized.Warnings[0])
	}
	if strings.Contains(finalized.Warnings[0].Evidence, "forged diagnostic") {
		t.Fatalf("warning trusted go test output: %+v", finalized.Warnings[0])
	}
	if !strings.Contains(finalized.Warnings[0].Evidence, "exit code 1") {
		t.Fatalf("warning lost trusted failure status: %+v", finalized.Warnings[0])
	}
	if !strings.Contains(result.SandboxRuns[1].Stderr, "forged diagnostic") {
		t.Fatalf("sandbox run lost reviewed test output: %+v", result.SandboxRuns[1])
	}
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoTest}, result.SandboxRuns[1], parsed)
	if diagnostics.Parsed != 0 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("go test diagnostics were trusted: %+v", diagnostics)
	}
}

func TestSandboxDiagnosticsRetainGenericWarningWhenMappingIsIncomplete(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/file.go")))
	run := sandboxRun{
		ExitCode: 1,
		Stdout: "pkg/file.go:2: mapped\n" +
			"pkg/file.go:99: outside changed hunk\n",
	}
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, run, parsed)
	if diagnostics.Parsed != 2 || diagnostics.Mapped != 1 ||
		!sandboxDiagnosticsNeedGenericWarning(commandSpec{Kind: commandCheckGoVet}, run, diagnostics) {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	result := governanceResult{}
	result.Matches = append(result.Matches, diagnostics.Matches...)
	result.addSandboxWarning(commandSpec{Kind: commandCheckGoVet}, run)
	finalized := finalizeRuleMatches(result.Matches)
	if len(finalized.Findings) != 1 || len(finalized.Warnings) != 1 ||
		finalized.Findings[0].RuleID != ruleSandboxGoVetDiagnostic ||
		finalized.Warnings[0].RuleID != ruleSandboxRunFailed {
		t.Fatalf("finalized = %+v", finalized)
	}
}

func TestSandboxDiagnosticPathMustMapUniquely(t *testing.T) {
	diff := sandboxDiagnosticDiff("a/file.go") + sandboxDiagnosticDiff("b/file.go")
	parsed := parseUnifiedDiff([]byte(diff))
	ambiguous := parseSandboxDiagnostics(commandSpec{Kind: commandCheckStaticcheck}, sandboxRun{
		ExitCode: 1,
		Stderr:   "file.go:2: diagnostic",
	}, parsed)
	if ambiguous.Parsed != 1 || ambiguous.Mapped != 0 || len(ambiguous.Matches) != 0 {
		t.Fatalf("ambiguous diagnostics = %+v", ambiguous)
	}
	exactRun := sandboxRun{
		ExitCode: 1,
		Stderr:   "a/file.go:2: diagnostic",
	}
	exact := parseSandboxDiagnostics(commandSpec{Kind: commandCheckStaticcheck}, exactRun, parsed)
	if exact.Mapped != 1 || len(exact.Matches) != 1 || exact.Matches[0].File != "a/file.go" {
		t.Fatalf("exact diagnostics = %+v", exact)
	}
	if sandboxDiagnosticsNeedGenericWarning(
		commandSpec{Kind: commandCheckStaticcheck}, exactRun, exact,
	) {
		t.Fatal("fully mapped staticcheck output retained a generic warning")
	}
}

func TestSandboxDiagnosticsUseAffectedModuleContext(t *testing.T) {
	diff := sandboxDiagnosticDiff("pkg/a.go") + sandboxDiagnosticDiff("nested/pkg/a.go")
	parsed := parseUnifiedDiff([]byte(diff))
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, sandboxRun{
		ExitCode: 1,
		Stdout: "==> vet .\n" +
			"pkg/a.go:2: same diagnostic\n",
		Stderr: "==> vet nested\n" +
			"pkg/a.go:2: same diagnostic\n",
	}, parsed)
	if diagnostics.Parsed != 2 || diagnostics.Mapped != 2 || len(diagnostics.Matches) != 2 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if diagnostics.Matches[0].File != "pkg/a.go" ||
		diagnostics.Matches[1].File != "nested/pkg/a.go" {
		t.Fatalf("mapped files = %+v", diagnostics.Matches)
	}
}

func TestSandboxDiagnosticModuleContextCannotFallBackToRootFile(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "==> staticcheck nested\n" +
			"pkg/a.go:2: nested diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(
		commandSpec{Kind: commandCheckStaticcheck},
		run,
		parsed,
	)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("diagnostics = %+v, want nested diagnostic unmapped", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(
		commandSpec{Kind: commandCheckStaticcheck},
		run,
		diagnostics,
	) {
		t.Fatal("unmapped nested diagnostic suppressed the generic warning")
	}
}

func TestSandboxDiagnosticInvalidModuleBannerFailsClosed(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "==> vet ../nested\n" +
			"pkg/a.go:2: diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("diagnostics = %+v, want invalid module context unmapped", diagnostics)
	}
}

func TestSandboxDiagnosticUnexpectedBannerModeFailsClosed(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "==> staticcheck nested\n" +
			"pkg/a.go:2: diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("diagnostics = %+v, want unexpected banner mode unmapped", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(commandSpec{Kind: commandCheckGoVet}, run, diagnostics) {
		t.Fatal("unexpected banner mode suppressed the generic warning")
	}
}

func TestSandboxDiagnosticParentPathWithoutBannerFailsClosed(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	run := sandboxRun{
		ExitCode: 1,
		Stderr:   "../pkg/a.go:2: diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("diagnostics = %+v, want parent path unmapped", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(commandSpec{Kind: commandCheckGoVet}, run, diagnostics) {
		t.Fatal("parent path suppressed the generic warning")
	}
}

func TestParseSandboxModuleBanner(t *testing.T) {
	module, ok := parseSandboxModuleBanner(
		commandCheckStaticcheck,
		"==> staticcheck with space\r",
	)
	if !ok || module != "with space" {
		t.Fatalf("module banner = %q, %v", module, ok)
	}
	module, ok = parseSandboxModuleBanner(commandCheckGoVet, "==> vet ../escape")
	if !ok || module != invalidSandboxDiagnosticModule {
		t.Fatalf("invalid module banner = %q, %v", module, ok)
	}
	module, ok = parseSandboxModuleBanner(commandCheckGoVet, "==> staticcheck nested")
	if !ok || module != invalidSandboxDiagnosticModule {
		t.Fatalf("unexpected mode banner = %q, %v", module, ok)
	}
	if _, ok := parseSandboxModuleBanner(commandCheckGoTest, "==> test nested"); ok {
		t.Fatal("go test banner was accepted as trusted diagnostic context")
	}
}

func TestSandboxDiagnosticDeletedFileDoesNotMap(t *testing.T) {
	parsed := parseUnifiedDiff([]byte("diff --git a/file.go b/file.go\ndeleted file mode 100644\n--- a/file.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package p\n"))
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, sandboxRun{
		ExitCode: 1,
		Stderr:   "file.go:1: diagnostic",
	}, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("deleted-file diagnostics = %+v", diagnostics)
	}
}

func TestSandboxDiagnosticLimitAndTruncationFallback(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/file.go")))
	var output strings.Builder
	for i := 0; i < maxSandboxDiagnosticsPerRun+1; i++ {
		fmt.Fprintf(&output, "pkg/file.go:2: diagnostic %d\n", i)
	}
	run := sandboxRun{ExitCode: 1, Stderr: output.String()}
	diagnostics := parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, run, parsed)
	if len(diagnostics.Matches) != maxSandboxDiagnosticsPerRun || !diagnostics.Overflow ||
		!sandboxDiagnosticsNeedGenericWarning(commandSpec{Kind: commandCheckGoVet}, run, diagnostics) {
		t.Fatalf("limited diagnostics = %+v", diagnostics)
	}
	run.Warnings = []string{"stderr truncated"}
	diagnostics = parseSandboxDiagnostics(commandSpec{Kind: commandCheckGoVet}, run, parsed)
	if !sandboxDiagnosticsNeedGenericWarning(commandSpec{Kind: commandCheckGoVet}, run, diagnostics) {
		t.Fatal("truncated output suppressed generic warning")
	}
}

func TestSyntheticSkippedRunsDoNotCountAsToolCalls(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/file.go")))
	runner := &diagnosticTestRunner{runs: map[commandKind]sandboxRun{
		commandCheckGoVersion: {ExitCode: 127, Error: "go unavailable"},
	}}
	result, err := runGovernance(context.Background(), config{}, reviewInput{
		kind:            inputKindRepoPath,
		repoRoot:        t.TempDir(),
		sandboxRepoRoot: t.TempDir(),
	}, parsed, runtimeHooks{sandboxRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolCalls != 1 || len(result.SandboxRuns) != 3 ||
		!result.SandboxRuns[1].Skipped || !result.SandboxRuns[2].Skipped {
		t.Fatalf("governance = %+v", result)
	}
}

func sandboxDiagnosticDiff(file string) string {
	return fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1,1 +1,3 @@\n package p\n+\n+func added() {}\n", file, file, file, file)
}

type diagnosticTestRunner struct {
	runs  map[commandKind]sandboxRun
	calls []commandKind
}

func (r *diagnosticTestRunner) RunSandboxCommand(_ context.Context, spec commandSpec) sandboxRun {
	r.calls = append(r.calls, spec.Kind)
	if run, ok := r.runs[spec.Kind]; ok {
		run.Runtime = runtimeFake
		run.Command = string(spec.Kind)
		return run
	}
	return sandboxRun{
		Runtime:    runtimeFake,
		Command:    string(spec.Kind),
		ExitCode:   0,
		Stdout:     "ok",
		DurationMS: 1,
	}
}
