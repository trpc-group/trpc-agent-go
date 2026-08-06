//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestSandboxDiagnosticsUnaccountedOutputRetainsGenericWarning(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	spec := commandSpec{
		Kind: commandCheckGoVet,
		DiagnosticModules: map[string]string{
			testRootModuleToken:   ".",
			testNestedModuleToken: "nested",
		},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"root.go:2: mapped diagnostic\n" +
			sandboxModuleBanner("vet", testNestedModuleToken) + "\n" +
			"go: errors parsing go.mod\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 1 ||
		!diagnostics.UnaccountedOutput || len(diagnostics.Matches) != 1 {
		t.Fatalf("diagnostics = %+v, want mapped diagnostic plus unaccounted output", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("unaccounted output suppressed the generic warning")
	}
	result := governanceResult{}
	result.addSandboxDiagnosticWarning(spec, run, diagnostics)
	finalized := finalizeRuleMatches(result.Matches)
	if len(finalized.Warnings) != 1 ||
		strings.Contains(finalized.Warnings[0].Evidence, "go: errors parsing go.mod") {
		t.Fatalf("unaccounted output leaked into warning evidence: %+v", finalized)
	}
}

func TestSandboxDiagnosticsPackageHeaderIsStrictlyRecognized(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "/* module ignored.example/fake */\nmodule example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(root, "root.go"), "package root\n\nfunc Added() {}\n")
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	for _, header := range []string{
		"# example.com/root",
		"# example.com/root/pkg",
		"# example.com/root [example.com/root.test]",
		"# example.com/root_test [example.com/root.test]",
		"# [example.com/root]",
		"# [example.com/root.test]",
	} {
		t.Run("valid/"+strings.ReplaceAll(header, "/", "_"), func(t *testing.T) {
			valid := parseSandboxDiagnosticsWithSnapshot(spec, sandboxRun{
				ExitCode: 1,
				Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
					header + "\n" +
					"root.go:2: mapped diagnostic\n",
			}, parsed, root)
			if valid.UnaccountedOutput || valid.Parsed != 1 || valid.Mapped != 1 ||
				sandboxDiagnosticsNeedGenericWarning(spec, sandboxRun{ExitCode: 1}, valid) {
				t.Fatalf("valid package header was not fully accounted for: %+v", valid)
			}
		})
	}
	for _, header := range []string{
		"# arbitrary failure text",
		"#  example.com/root",
		"# example.com/root extra",
		"# example.com/root [example.com/other.test]",
		"# external.example/pkg",
		"# [external.example/pkg]",
		"# [example.com/root extra]",
		"# [example.com/root.test] extra",
	} {
		t.Run("invalid/"+strings.ReplaceAll(header, "/", "_"), func(t *testing.T) {
			invalid := parseSandboxDiagnosticsWithSnapshot(spec, sandboxRun{
				ExitCode: 1,
				Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
					header + "\n" +
					"root.go:2: mapped diagnostic\n",
			}, parsed, root)
			if !invalid.UnaccountedOutput || invalid.Mapped != 1 ||
				!sandboxDiagnosticsNeedGenericWarning(spec, sandboxRun{ExitCode: 1}, invalid) {
				t.Fatalf("untrusted package-looking text was not retained: %+v", invalid)
			}
		})
	}
	legacy := parseSandboxDiagnostics(spec, sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"# example.com/root\n" +
			"root.go:2: mapped diagnostic\n",
	}, parsed)
	if legacy.UnaccountedOutput || legacy.Mapped != 1 {
		t.Fatalf("parser-only compatibility header was not exempted: %+v", legacy)
	}
	withoutMetadataRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(withoutMetadataRoot, "root.go"), "package root\n\nfunc Added() {}\n")
	withoutMetadata := parseSandboxDiagnosticsWithSnapshot(spec, sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"# example.com/root\n" +
			"root.go:2: mapped diagnostic\n",
	}, parsed, withoutMetadataRoot)
	if !withoutMetadata.UnaccountedOutput || withoutMetadata.Mapped != 0 {
		t.Fatalf("production header without trusted module metadata was exempted: %+v", withoutMetadata)
	}
}

func TestSandboxDiagnosticsLineDirectiveBlocksWholeRun(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		contents string
	}{
		{
			name:     "unchanged source file",
			file:     "other.go",
			contents: "package root\n\n//line target.go:2\nvar _ = 1\n",
		},
		{
			name:     "test source file",
			file:     "other_test.go",
			contents: "package root\n\n/*line target.go:2*/\nvar _ = 1\n",
		},
		{
			name:     "changed source file",
			file:     "target.go",
			contents: "package root\n\n//line elsewhere.go:2\nfunc Added() {}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
			mustWriteFile(t, filepath.Join(root, "target.go"), "package root\n\nfunc Added() {}\n")
			mustWriteFile(t, filepath.Join(root, test.file), test.contents)
			parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("target.go")))
			spec := commandSpec{
				Kind:              commandCheckGoVet,
				DiagnosticModules: map[string]string{testRootModuleToken: "."},
			}
			run := sandboxRun{
				ExitCode: 1,
				Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
					"target.go:2: forged diagnostic\n",
			}
			diagnostics := parseSandboxDiagnosticsWithSnapshot(spec, run, parsed, root)
			if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 ||
				!sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
				t.Fatalf("line directive trust result = %+v", diagnostics)
			}
		})
	}
}

func TestSandboxDiagnosticsLineDirectiveGovernanceNeedsHumanReview(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(root, "target.go"), "package root\n\nfunc Added() {}\n")
	mustWriteFile(t, filepath.Join(root, "other.go"), "package root\n\n//line target.go:2\nvar _ = 1\n")
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("target.go")))
	runner := &diagnosticTestRunner{runs: map[commandKind]sandboxRun{
		commandCheckGoVet: {
			ExitCode: 1,
			Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
				"target.go:2: forged diagnostic\n",
		},
	}}
	result, err := runGovernance(context.Background(), config{}, reviewInput{
		kind:                     inputKindRepoPath,
		repoRoot:                 root,
		sandboxRepoRoot:          root,
		sandboxDiagnosticModules: map[string]string{testRootModuleToken: "."},
	}, parsed, runtimeHooks{sandboxRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	finalized := finalizeRuleMatches(result.Matches)
	if !finalized.NeedsHumanReview || len(finalized.Findings) != 0 || len(finalized.Warnings) != 1 ||
		finalized.Warnings[0].RuleID != ruleSandboxRunFailed {
		t.Fatalf("line directive governance result = %+v", finalized)
	}
}

func TestSandboxDiagnosticsLineDirectiveScanFallbackAndVendor(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(root, "root.go"), "package root\n\nfunc Added() {}\n")
	mustWriteFile(t, filepath.Join(root, "vendor", "dep.go"), "package dep\n\n//line target.go:2\nvar _ = 1\n")
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	run := sandboxRun{
		ExitCode: 1,
		Stderr:   "root.go:2: diagnostic without banner\n",
	}
	withoutModules := parseSandboxDiagnosticsWithSnapshot(
		commandSpec{Kind: commandCheckGoVet},
		run,
		parsed,
		root,
	)
	if withoutModules.Parsed != 1 || withoutModules.Mapped != 1 ||
		withoutModules.UnaccountedOutput || sandboxDiagnosticsNeedGenericWarning(
		commandSpec{Kind: commandCheckGoVet}, run, withoutModules,
	) {
		t.Fatalf("root fallback or vendor exclusion failed: %+v", withoutModules)
	}
	missingRoot := parseSandboxDiagnosticsWithSnapshot(
		commandSpec{Kind: commandCheckGoVet},
		run,
		parsed,
		filepath.Join(root, "missing"),
	)
	if missingRoot.Mapped != 0 || len(missingRoot.Matches) != 0 ||
		!sandboxDiagnosticsNeedGenericWarning(commandSpec{Kind: commandCheckGoVet}, run, missingRoot) {
		t.Fatalf("missing snapshot root did not fail closed: %+v", missingRoot)
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
		kind:                     inputKindRepoPath,
		repoRoot:                 t.TempDir(),
		sandboxRepoRoot:          t.TempDir(),
		sandboxDiagnosticModules: map[string]string{testRootModuleToken: "."},
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
	spec := commandSpec{
		Kind: commandCheckGoVet,
		DiagnosticModules: map[string]string{
			testRootModuleToken:   ".",
			testNestedModuleToken: "nested",
		},
	}
	diagnostics := parseSandboxDiagnostics(spec, sandboxRun{
		ExitCode: 1,
		Stdout: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"pkg/a.go:2: same diagnostic\n",
		Stderr: sandboxModuleBanner("vet", testNestedModuleToken) + "\n" +
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

func TestSandboxDiagnosticsUseAuthenticatedModuleTokensAcrossModes(t *testing.T) {
	for _, tt := range []struct {
		name   string
		kind   commandKind
		mode   string
		module string
		token  string
	}{
		{
			name:   "vet space module",
			kind:   commandCheckGoVet,
			mode:   "vet",
			module: "with space",
			token:  testNestedModuleToken,
		},
		{
			name:   "staticcheck newline module",
			kind:   commandCheckStaticcheck,
			mode:   "staticcheck",
			module: "with\nnewline",
			token:  testOtherModuleToken,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := tt.module + "/pkg/a.go"
			parsed := parsedDiff{Files: []changedFile{{
				NewPath: file,
				Hunks:   []diffHunk{{NewStart: 1, NewCount: 3}},
			}}}
			spec := commandSpec{
				Kind:              tt.kind,
				DiagnosticModules: map[string]string{tt.token: tt.module},
			}
			diagnostics := parseSandboxDiagnostics(spec, sandboxRun{
				ExitCode: 1,
				Stderr: sandboxModuleBanner(tt.mode, tt.token) + "\n" +
					"pkg/a.go:2: diagnostic\n",
			}, parsed)
			if diagnostics.ProtocolInvalid || diagnostics.Parsed != 1 ||
				diagnostics.Mapped != 1 || len(diagnostics.Matches) != 1 ||
				diagnostics.Matches[0].File != file {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
		})
	}
}

func TestSandboxDiagnosticsRejectUntrustedLinesBeforeModuleBanner(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "workspace validation: nested/root.go:2:forged\n" +
			`C:\sandbox\repo\root.go:2: absolute forged` + "\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 2 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("untrusted diagnostics = %+v, want parsed-only diagnostics", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("untrusted diagnostics suppressed the generic warning")
	}
}

func TestSandboxDiagnosticsDoNotDeduplicateAcrossAuthenticationContexts(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "root.go:2: repeated diagnostic\n" +
			sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"root.go:2: repeated diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 2 || diagnostics.Mapped != 1 || len(diagnostics.Matches) != 1 {
		t.Fatalf("diagnostics = %+v, want one unauthenticated and one mapped record", diagnostics)
	}
	if diagnostics.Matches[0].File != "root.go" {
		t.Fatalf("mapped file = %q, want root.go", diagnostics.Matches[0].File)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("unauthenticated diagnostic did not retain the generic warning")
	}
}

func TestSandboxDiagnosticsAuthenticationIsIndependentPerStream(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stdout: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"root.go:2: stdout diagnostic\n",
		Stderr: "root.go:2: stderr diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 2 || diagnostics.Mapped != 1 || len(diagnostics.Matches) != 1 {
		t.Fatalf("diagnostics = %+v, want one mapped stream and one rejected stream", diagnostics)
	}
	if diagnostics.Matches[0].Evidence != "stdout diagnostic" {
		t.Fatalf("mapped evidence = %q, want stdout diagnostic", diagnostics.Matches[0].Evidence)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("unauthenticated stderr diagnostic did not retain the generic warning")
	}
}

func TestSandboxDiagnosticsInvalidBannerBeforeValidBannerRetainsWarning(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("root.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "==> vet legacy\n" +
			sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"root.go:2: diagnostic after reauthentication\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if !diagnostics.ProtocolInvalid || diagnostics.Parsed != 1 || diagnostics.Mapped != 1 ||
		len(diagnostics.Matches) != 1 {
		t.Fatalf("diagnostics = %+v, want mapped record with persistent protocol failure", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("persistent protocol failure suppressed the generic warning")
	}
}

func TestSandboxDiagnosticModuleContextCannotFallBackToRootFile(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	spec := commandSpec{
		Kind:              commandCheckStaticcheck,
		DiagnosticModules: map[string]string{testNestedModuleToken: "nested"},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("staticcheck", testNestedModuleToken) + "\n" +
			"pkg/a.go:2: nested diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(
		spec,
		run,
		parsed,
	)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("diagnostics = %+v, want nested diagnostic unmapped", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(
		spec,
		run,
		diagnostics,
	) {
		t.Fatal("unmapped nested diagnostic suppressed the generic warning")
	}
}

func TestSandboxDiagnosticInvalidModuleBannerFailsClosed(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: "==> vet ../nested\n" +
			"pkg/a.go:2: diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 ||
		!diagnostics.ProtocolInvalid {
		t.Fatalf("diagnostics = %+v, want invalid module context unmapped", diagnostics)
	}
}

func TestSandboxDiagnosticUnexpectedBannerModeFailsClosed(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testNestedModuleToken: "nested"},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("staticcheck", testNestedModuleToken) + "\n" +
			"pkg/a.go:2: diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 ||
		!diagnostics.ProtocolInvalid {
		t.Fatalf("diagnostics = %+v, want unexpected banner mode unmapped", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
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
	staticcheckSpec := commandSpec{
		Kind: commandCheckStaticcheck,
		DiagnosticModules: map[string]string{
			testOtherModuleToken: "with space\nmodule",
		},
	}
	module, ok := parseSandboxModuleBanner(
		staticcheckSpec,
		sandboxModuleBanner("staticcheck", testOtherModuleToken)+"\r",
	)
	if !ok || module != "with space\nmodule" {
		t.Fatalf("module banner = %q, %v", module, ok)
	}
	vetSpec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testNestedModuleToken: "nested"},
	}
	module, ok = parseSandboxModuleBanner(vetSpec, "==> vet ../escape")
	if !ok || module != invalidSandboxDiagnosticModule {
		t.Fatalf("invalid module banner = %q, %v", module, ok)
	}
	for _, line := range []string{
		"==>work nested",
		sandboxModuleBanner("vet", testNestedModuleToken) + " extra",
	} {
		module, ok = parseSandboxModuleBanner(vetSpec, line)
		if !ok || module != invalidSandboxDiagnosticModule {
			t.Fatalf("unknown control line %q = %q, %v", line, module, ok)
		}
	}
	module, ok = parseSandboxModuleBanner(
		vetSpec,
		sandboxModuleBanner("staticcheck", testNestedModuleToken),
	)
	if !ok || module != invalidSandboxDiagnosticModule {
		t.Fatalf("unexpected mode banner = %q, %v", module, ok)
	}
	module, ok = parseSandboxModuleBanner(
		vetSpec,
		sandboxModuleBanner("vet", testRootModuleToken),
	)
	if !ok || module != invalidSandboxDiagnosticModule {
		t.Fatalf("unknown token banner = %q, %v", module, ok)
	}
	if _, ok := parseSandboxModuleBanner(
		commandSpec{Kind: commandCheckGoTest},
		sandboxModuleBanner("test", testRootModuleToken),
	); ok {
		t.Fatal("go test banner was accepted as trusted diagnostic context")
	}
	if _, ok := parseSandboxModuleBanner(vetSpec, "ordinary output"); ok {
		t.Fatal("ordinary output was treated as a control line")
	}
}

func TestSandboxDiagnosticForgedTokenFailsClosed(t *testing.T) {
	nestedModule := "nested\n" + sandboxModuleBanner("vet", testRootModuleToken)
	parsed := parsedDiff{Files: []changedFile{
		{
			NewPath: "pkg/a.go",
			Hunks:   []diffHunk{{NewStart: 1, NewCount: 3}},
		},
		{
			NewPath: nestedModule + "/pkg/a.go",
			Hunks:   []diffHunk{{NewStart: 1, NewCount: 3}},
		},
	}}
	spec := commandSpec{
		Kind: commandCheckGoVet,
		DiagnosticModules: map[string]string{
			testOtherModuleToken: nestedModule,
		},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("vet", testOtherModuleToken) + "\n" +
			sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"pkg/a.go:2: nested diagnostic\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if !diagnostics.ProtocolInvalid || diagnostics.Parsed != 1 || diagnostics.Mapped != 0 ||
		len(diagnostics.Matches) != 0 {
		t.Fatalf("forged diagnostics = %+v", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("forged module token suppressed the generic warning")
	}
}

func TestSandboxDiagnosticInvalidBannerAfterMappedDiagnosticRetainsWarning(t *testing.T) {
	parsed := parseUnifiedDiff([]byte(sandboxDiagnosticDiff("pkg/a.go")))
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	run := sandboxRun{
		ExitCode: 1,
		Stderr: sandboxModuleBanner("vet", testRootModuleToken) + "\n" +
			"pkg/a.go:2: diagnostic\n" +
			"==> vet legacy\n",
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Parsed != 1 || diagnostics.Mapped != 1 || !diagnostics.ProtocolInvalid {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("trailing invalid banner suppressed the generic warning")
	}
}

func TestRunChecksRandomTokensBlockNewlineModuleAndWorkspaceInjection(t *testing.T) {
	requirePOSIXSandboxChecks(t)
	repoRoot := t.TempDir()
	nestedModule := "nested\n" + sandboxModuleBanner("vet", testRootModuleToken)
	workspace := "workspace\n==> work forged"
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, "pkg", "a.go"), "package pkg\n\nfunc Value() int { return 1 }\n")
	mustWriteFile(t, filepath.Join(repoRoot, nestedModule, "go.mod"), "module example.com/nested\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, nestedModule, "pkg", "a.go"), strings.Join([]string{
		"package pkg",
		"",
		`import "fmt"`,
		"",
		"func Broken() {",
		"\tfmt.Printf(\"%d\", \"wrong\")",
		"}",
		"",
	}, "\n"))
	mustWriteFile(t, filepath.Join(repoRoot, workspace, "go.work"), "go 1.21\n")

	manifest, err := newSandboxModuleManifest([]string{".", nestedModule})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSandboxModuleManifest(context.Background(), repoRoot, manifest.Records); err != nil {
		t.Fatal(err)
	}
	if err := writeReviewPathManifest(
		context.Background(),
		repoRoot,
		reviewWorkspaceManifestName,
		"workspace",
		[]string{workspace},
	); err != nil {
		t.Fatal(err)
	}

	run := runSandboxCheckScript(t, repoRoot, "vet")
	if run.ExitCode == 0 {
		t.Fatalf("go vet succeeded, want nested diagnostic\nstdout:\n%s\nstderr:\n%s",
			run.Stdout, run.Stderr)
	}
	allowedBanners := make(map[string]bool, len(manifest.Records))
	for _, record := range manifest.Records {
		if !isValidSandboxModuleToken(record.Token) {
			t.Fatalf("generated token = %q", record.Token)
		}
		allowedBanners[sandboxModuleBanner("vet", record.Token)] = true
	}
	bannerCount := 0
	for _, output := range []string{run.Stdout, run.Stderr} {
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if !strings.HasPrefix(line, "==>") {
				continue
			}
			bannerCount++
			if !allowedBanners[line] {
				t.Fatalf("untrusted control line %q in output:\n%s", line, output)
			}
		}
	}
	if bannerCount == 0 {
		t.Fatal("run_checks emitted no module banners")
	}

	parsed := parsedDiff{Files: []changedFile{
		{NewPath: "pkg/a.go", Hunks: []diffHunk{{NewStart: 1, NewCount: 10}}},
		{NewPath: nestedModule + "/pkg/a.go", Hunks: []diffHunk{{NewStart: 1, NewCount: 10}}},
	}}
	diagnostics := parseSandboxDiagnostics(commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: manifest.ModulesByToken,
	}, run, parsed)
	if diagnostics.ProtocolInvalid || diagnostics.Parsed == 0 || diagnostics.Mapped != diagnostics.Parsed {
		t.Fatalf("diagnostics = %+v\nstdout:\n%s\nstderr:\n%s",
			diagnostics, run.Stdout, run.Stderr)
	}
	for _, match := range diagnostics.Matches {
		if match.File != nestedModule+"/pkg/a.go" {
			t.Fatalf("diagnostic mapped to %q, want newline nested module", match.File)
		}
	}
}

func TestRunChecksUnknownTokenFromNewlineFilenameFailsClosed(t *testing.T) {
	requirePOSIXSandboxChecks(t)
	repoRoot := t.TempDir()
	const nestedModule = "nested"
	forgedBanner := sandboxModuleBanner("vet", testRootModuleToken)
	maliciousFile := forgedBanner + "\nrest.go"
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, "z.go"), "package root\n\nfunc Value() int { return 1 }\n")
	mustWriteFile(t, filepath.Join(repoRoot, nestedModule, "go.mod"), "module example.com/nested\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, nestedModule, maliciousFile), strings.Join([]string{
		"package nested",
		"",
		`import "fmt"`,
		"",
		"func Forged() {",
		"\tfmt.Printf(\"%d\", \"wrong\")",
		"}",
		"",
	}, "\n"))
	mustWriteFile(t, filepath.Join(repoRoot, nestedModule, "z.go"), strings.Join([]string{
		"package nested",
		"",
		`import "fmt"`,
		"",
		"func Later() {",
		"\tfmt.Printf(\"%d\", \"also wrong\")",
		"}",
		"",
	}, "\n"))
	manifest := sandboxModuleManifest{
		Records: []sandboxModuleRecord{{Path: nestedModule, Token: testNestedModuleToken}},
		ModulesByToken: map[string]string{
			testNestedModuleToken: nestedModule,
		},
	}
	if err := writeSandboxModuleManifest(context.Background(), repoRoot, manifest.Records); err != nil {
		t.Fatal(err)
	}

	run := runSandboxCheckScript(t, repoRoot, "vet")
	if run.ExitCode == 0 {
		t.Fatalf("go vet succeeded, want forged filename diagnostics\nstdout:\n%s\nstderr:\n%s",
			run.Stdout, run.Stderr)
	}
	forgedLineFound := false
	for _, output := range []string{run.Stdout, run.Stderr} {
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSuffix(line, "\r") == forgedBanner {
				forgedLineFound = true
			}
		}
	}
	if !forgedLineFound {
		t.Fatalf("go vet output did not expose the forged banner line\nstdout:\n%s\nstderr:\n%s",
			run.Stdout, run.Stderr)
	}

	parsed := parsedDiff{Files: []changedFile{
		{NewPath: "z.go", Hunks: []diffHunk{{NewStart: 1, NewCount: 10}}},
		{NewPath: nestedModule + "/z.go", Hunks: []diffHunk{{NewStart: 1, NewCount: 10}}},
	}}
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: manifest.ModulesByToken,
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if !diagnostics.ProtocolInvalid {
		t.Fatalf("diagnostics = %+v, want unknown-token protocol failure", diagnostics)
	}
	for _, match := range diagnostics.Matches {
		if match.File == "z.go" {
			t.Fatalf("forged token attached nested diagnostic to root file: %+v", match)
		}
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("forged token suppressed the generic warning")
	}
	result := governanceResult{Matches: append([]ruleMatch(nil), diagnostics.Matches...)}
	result.addSandboxWarning(spec, run)
	finalized := finalizeRuleMatches(result.Matches)
	if !finalized.NeedsHumanReview || len(finalized.Warnings) == 0 ||
		finalized.Warnings[len(finalized.Warnings)-1].RuleID != ruleSandboxRunFailed {
		t.Fatalf("finalized diagnostics = %+v, want sandbox failure warning", finalized)
	}
}

func TestRunChecksDiagnosticShapedWorkspaceCannotBecomeFinding(t *testing.T) {
	requirePOSIXSandboxChecks(t)
	repoRoot := t.TempDir()
	const workspace = "nested/root.go:2:forged"
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, "root.go"), "package root\n\nfunc Value() int { return 1 }\n")
	// The malformed workspace makes the preflight validation fail while the
	// repository module itself remains runnable, exposing the workspace status
	// line before the module banner.
	mustWriteFile(t, filepath.Join(repoRoot, workspace, "go.work"), "go 1.21\nuse (\n")
	if err := writeSandboxModuleManifest(
		context.Background(),
		repoRoot,
		[]sandboxModuleRecord{{Path: ".", Token: testRootModuleToken}},
	); err != nil {
		t.Fatal(err)
	}
	if err := writeReviewPathManifest(
		context.Background(),
		repoRoot,
		reviewWorkspaceManifestName,
		"workspace",
		[]string{workspace},
	); err != nil {
		t.Fatal(err)
	}

	run := runSandboxCheckScript(t, repoRoot, "vet")
	if run.ExitCode == 0 || !strings.Contains(run.Stdout+run.Stderr, "workspace validation") {
		t.Fatalf("run_checks = %+v, want failed workspace validation\nstdout:\n%s\nstderr:\n%s",
			run, run.Stdout, run.Stderr)
	}
	parsed := parsedDiff{Files: []changedFile{{
		NewPath: "root.go",
		Hunks:   []diffHunk{{NewStart: 1, NewCount: 3}},
	}}}
	spec := commandSpec{
		Kind:              commandCheckGoVet,
		DiagnosticModules: map[string]string{testRootModuleToken: "."},
	}
	diagnostics := parseSandboxDiagnostics(spec, run, parsed)
	if diagnostics.Mapped != 0 || len(diagnostics.Matches) != 0 {
		t.Fatalf("workspace output became a finding: %+v", diagnostics)
	}
	if !sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
		t.Fatal("workspace output suppressed the generic sandbox warning")
	}
	result := governanceResult{Matches: append([]ruleMatch(nil), diagnostics.Matches...)}
	result.addSandboxWarning(spec, run)
	finalized := finalizeRuleMatches(result.Matches)
	if !finalized.NeedsHumanReview || len(finalized.Warnings) == 0 ||
		finalized.Warnings[len(finalized.Warnings)-1].RuleID != ruleSandboxRunFailed {
		t.Fatalf("finalized workspace diagnostics = %+v, want needs_human_review sandbox warning", finalized)
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
		kind:                     inputKindRepoPath,
		repoRoot:                 t.TempDir(),
		sandboxRepoRoot:          t.TempDir(),
		sandboxDiagnosticModules: map[string]string{testRootModuleToken: "."},
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

func requirePOSIXSandboxChecks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("newline paths require a POSIX filesystem")
	}
	for _, toolName := range []string{"bash", "go"} {
		if _, err := exec.LookPath(toolName); err != nil {
			t.Skipf("%s unavailable: %v", toolName, err)
		}
	}
}

func runSandboxCheckScript(t *testing.T, repoRoot string, mode string) sandboxRun {
	t.Helper()
	scriptPath, err := filepath.Abs(filepath.Join("skills", "code-review", "scripts", "run_checks.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.ToSlash(scriptPath), mode)
	cmd.Env = append(os.Environ(), "REVIEW_REPO_DIR="+filepath.ToSlash(repoRoot))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("run_checks failed to start: %v", err)
	}
	return sandboxRun{
		Command:  string(commandCheckGoVet),
		ExitCode: cmd.ProcessState.ExitCode(),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
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
