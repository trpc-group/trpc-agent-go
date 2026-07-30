//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildReviewMessageHonorsUTF8ByteBudget(t *testing.T) {
	parsed := parsedInput{
		ChangedFiles: []ChangedFile{{Path: "你好.go"}},
		ChangedHunks: []ChangedHunk{{
			ID:   "你好.go:1:1",
			File: "你好.go",
			Body: "+" + strings.Repeat("界", 100),
		}},
	}
	limits := Limits{MaxMessageBytes: 256, MaxFiles: 1, MaxHunks: 1, MaxHunkBytes: 64}
	message := buildReviewMessage(InputKindDiffFile, ReviewModePatchOnly, nil, parsed, limits)
	if len(message) > limits.MaxMessageBytes {
		t.Fatalf("message length = %d, want at most %d", len(message), limits.MaxMessageBytes)
	}
	if !utf8.ValidString(message) {
		t.Fatal("message truncation produced invalid UTF-8")
	}
}

func TestBuildReviewMessageDescribesWorkspaceWithoutWorkflowInstructions(t *testing.T) {
	parsed := parsedInput{
		ChangedFiles: []ChangedFile{{Path: "calculator.go"}},
	}

	patchOnly := buildReviewMessage(InputKindDiffFile, ReviewModePatchOnly, nil, parsed, Limits{})
	if !strings.Contains(
		patchOnly,
		"repository snapshot: unavailable; do not claim full-file or executable-repo evidence",
	) {
		t.Fatalf("patch-only message does not describe unavailable repository context:\n%s", patchOnly)
	}

	repoBacked := buildReviewMessage(InputKindRepoPath, ReviewModeRepoBacked, nil, parsed, Limits{})
	if !strings.Contains(repoBacked, "repository snapshot: work/inputs/repo") {
		t.Fatalf("repo-backed message does not describe repository context:\n%s", repoBacked)
	}
	for _, message := range []string{patchOnly, repoBacked} {
		for _, workflowInstruction := range []string{
			"workspace_exec",
			"submit_review_results",
		} {
			if strings.Contains(message, workflowInstruction) {
				t.Fatalf(
					"review input duplicates system workflow instruction %q:\n%s",
					workflowInstruction,
					message,
				)
			}
		}
	}
}

func TestBuildReviewMessageBoundsSecretSignals(t *testing.T) {
	parsed := parsedInput{
		SecretSignals: []SecretSignal{
			{File: "a.go", Line: 1, Kind: "token", RuleID: "SECRET-A", Evidence: "first"},
			{File: "b.go", Line: 2, Kind: "token", RuleID: "SECRET-B", Evidence: "second"},
			{File: "c.go", Line: 3, Kind: "token", RuleID: "SECRET-C", Evidence: "third"},
		},
	}
	message := buildReviewMessage(
		InputKindDiffFile,
		ReviewModePatchOnly,
		nil,
		parsed,
		Limits{MaxFiles: 2},
	)
	if !strings.Contains(message, "SECRET-A") || !strings.Contains(message, "SECRET-B") {
		t.Fatalf("message omitted bounded secret signals:\n%s", message)
	}
	if strings.Contains(message, "SECRET-C") {
		t.Fatalf("message contains a secret signal beyond the limit:\n%s", message)
	}
	if !strings.Contains(message, "1 additional secret signals omitted") {
		t.Fatalf("message has no secret-signal omission summary:\n%s", message)
	}
}

func TestBuildReviewMessageExplainsScopeAndNavigationFields(t *testing.T) {
	parsed := parsedInput{
		ChangedFiles: []ChangedFile{{
			Path:               "internal/calculator.go",
			HasCompleteContext: true,
		}},
		ChangedHunks: []ChangedHunk{{
			ID:             "internal/calculator.go:8:1",
			File:           "internal/calculator.go",
			Body:           "+func Subtract(a, b int) int { return a - b }",
			CandidateLines: []int{11},
		}},
		GoPackages: []GoPackage{{
			Directory:   "internal",
			PackageName: "calculator",
			ModulePath:  "example.com/review",
			ModuleRoot:  "",
			Complete:    true,
		}, {
			Directory:   "internal/worker",
			PackageName: "worker",
			ModulePath:  "example.com/review",
			ModuleRoot:  "",
			Complete:    true,
		}, {
			Directory:   "testdata/nested",
			PackageName: "nested",
			ModulePath:  "example.com/nested",
			ModuleRoot:  "testdata/nested",
			Complete:    true,
		}},
	}
	message := buildReviewMessage(
		InputKindRepoPath,
		ReviewModeRepoBacked,
		[]string{"internal/calculator.go", "internal/calculator_test.go"},
		parsed,
		Limits{},
	)

	for _, want := range []string{
		"Review scope (requested paths):\n- internal/calculator.go\n- internal/calculator_test.go",
		"complete_file_available=true",
		"package_context_complete=true",
		"module_dir=work/inputs/repo",
		"Hunk previews:",
		"candidate_lines identify added or modified new-file lines; they are not confirmed findings.",
		"candidate_lines=[11]",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message does not contain %q:\n%s", want, message)
		}
	}
	for _, old := range []string{"full_context=", " complete=", "Selected hunks:"} {
		if strings.Contains(message, old) {
			t.Fatalf("message still contains ambiguous label %q:\n%s", old, message)
		}
	}
	for _, coupledInstruction := range []string{
		"Required Skill baseline commands:",
		"run-go-checks.sh",
		"exactly once",
	} {
		if strings.Contains(message, coupledInstruction) {
			t.Fatalf("message contains Skill workflow instruction %q:\n%s", coupledInstruction, message)
		}
	}
}
