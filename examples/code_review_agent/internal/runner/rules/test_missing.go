//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
)

var (
	exportedFuncPattern = regexp.MustCompile(`^func\s+([A-Z]\w*)\s*\(`)
	exportedTypePattern = regexp.MustCompile(`^type\s+([A-Z]\w*)\s+`)
	// Matches test functions so we can exclude them.
	testFuncPattern = regexp.MustCompile(`^func\s+(Test\w*)\s*\(\s*t\s*\*\s*testing\.`)
	// Matches exported functions with receivers (methods).
	exportedMethodPattern = regexp.MustCompile(`^func\s+\(\s*\w+\s+\*?\w+\s*\)\s+([A-Z]\w*)\s*\(`)
)

// TestMissingRule detects exported functions/types without corresponding tests.
type TestMissingRule struct {
	runner.RuleBase
}

// NewTestMissingRule creates a new test missing rule.
func NewTestMissingRule() *TestMissingRule {
	return &TestMissingRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_TEST_MISSING_FUNC",
			CategoryValue: finding.CategoryMissingTest,
			DefaultSev:    finding.SeverityMedium,
		},
	}
}

// Check examines a Go file for exported symbols missing corresponding tests.
// The caller should pass the non-test file content and provide associated test
// file names through the file info or additional context.
func (r *TestMissingRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}
	if file.IsTestFile {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	// Find exported functions and types (excluding test functions).
	var exportedSymbols []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip test functions.
		if testFuncPattern.MatchString(trimmed) {
			continue
		}

		if m := exportedFuncPattern.FindStringSubmatch(trimmed); len(m) > 1 {
			exportedSymbols = append(exportedSymbols, m[1])
		}
		if m := exportedMethodPattern.FindStringSubmatch(trimmed); len(m) > 1 {
			exportedSymbols = append(exportedSymbols, m[1])
		}
	}

	if len(exportedSymbols) == 0 {
		return nil, nil
	}

	// Check if expected test functions exist in the content or need a test file.
	testFileName := strings.TrimSuffix(file.File, ".go") + "_test.go"

	// Report missing test functions for exported symbols.
	for _, sym := range exportedSymbols {
		expectedTestName := "Test" + sym
		if !strings.Contains(content, expectedTestName) {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, 1,
				"Exported symbol without corresponding test: "+sym,
				"func "+sym+"(...) has no test function "+expectedTestName,
				"Add test function "+expectedTestName+" in "+testFileName,
				finding.ConfidenceMedium,
			))
		}
	}

	return findings, nil
}

// TestFileMissingRule detects that a test file exists for a non-test Go file.
type TestFileMissingRule struct {
	runner.RuleBase
}

// NewTestFileMissingRule creates a new test file missing rule.
func NewTestFileMissingRule() *TestFileMissingRule {
	return &TestFileMissingRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_TEST_FILE_MISSING",
			CategoryValue: finding.CategoryMissingTest,
			DefaultSev:    finding.SeverityMedium,
		},
	}
}

// Check examines whether a non-test source file has a corresponding test file.
// This rule needs the full list of changed files to cross-reference.
// It returns findings at file level.
func (r *TestFileMissingRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") || file.IsTestFile {
		return nil, nil
	}

	testFile := strings.TrimSuffix(file.File, ".go") + "_test.go"

	// The rule checks if the file has exported symbols but no test content.
	// The cross-reference against other changed files is done by the agent.
	hasExports := fileHasExportedFuncs(content)

	if hasExports && !strings.Contains(content, "func Test") {
		return []finding.Finding{
			runner.NewFinding(
				&r.RuleBase, file.File, 1,
				"No test functions found for file with exported symbols",
				"File "+file.File+" has exported symbols but no test file detected",
				"Create "+testFile+" with test functions for exported symbols",
				finding.ConfidenceMedium,
			),
		}, nil
	}

	return nil, nil
}

// fileHasExportedFuncs scans content line by line for exported function/type declarations.
func fileHasExportedFuncs(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if exportedFuncPattern.MatchString(trimmed) || exportedTypePattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}
