//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxSandboxDiagnosticsPerRun = 50

	ruleSandboxGoTestDiagnostic      = "sandbox.go_test_diagnostic"
	ruleSandboxGoVetDiagnostic       = "sandbox.go_vet_diagnostic"
	ruleSandboxStaticcheckDiagnostic = "sandbox.staticcheck_diagnostic"
)

var goDiagnosticPattern = regexp.MustCompile(`^(.*\.go):([0-9]+)(?::([0-9]+))?:\s*(.+)$`)

type sandboxDiagnosticResult struct {
	Matches  []ruleMatch
	Parsed   int
	Mapped   int
	Overflow bool
}

type parsedSandboxDiagnostic struct {
	Path    string
	Line    int
	Column  int
	Message string
}

type sandboxDiagnosticMetadata struct {
	RuleID         string
	Severity       string
	Category       string
	Title          string
	Recommendation string
}

func parseSandboxDiagnostics(
	spec commandSpec,
	run sandboxRun,
	parsed parsedDiff,
) sandboxDiagnosticResult {
	metadata, ok := diagnosticMetadataForCommand(spec.Kind)
	if !ok {
		return sandboxDiagnosticResult{}
	}
	result := sandboxDiagnosticResult{}
	seen := map[string]bool{}
	for _, output := range []string{run.Stdout, run.Stderr} {
		for _, line := range strings.Split(output, "\n") {
			diagnostic, ok := parseSandboxDiagnosticLine(line)
			if !ok {
				continue
			}
			key := fmt.Sprintf("%s\x00%d\x00%d\x00%s", diagnostic.Path,
				diagnostic.Line, diagnostic.Column, diagnostic.Message)
			if seen[key] {
				continue
			}
			seen[key] = true
			result.Parsed++
			if result.Parsed > maxSandboxDiagnosticsPerRun {
				result.Overflow = true
				continue
			}
			file, ok := mapSandboxDiagnosticFile(diagnostic.Path, parsed)
			if !ok || !lineInNewHunk(file, diagnostic.Line) {
				continue
			}
			result.Mapped++
			evidence := diagnostic.Message
			if diagnostic.Column > 0 {
				evidence = fmt.Sprintf("column %d: %s", diagnostic.Column, diagnostic.Message)
			}
			result.Matches = append(result.Matches, ruleMatch{
				Severity:       metadata.Severity,
				Category:       metadata.Category,
				File:           file.NewPath,
				Line:           diagnostic.Line,
				Title:          metadata.Title,
				Evidence:       evidence,
				Recommendation: metadata.Recommendation,
				Confidence:     0.95,
				Source:         "sandbox",
				RuleID:         metadata.RuleID,
			})
		}
	}
	return result
}

func parseSandboxDiagnosticLine(line string) (parsedSandboxDiagnostic, bool) {
	matches := goDiagnosticPattern.FindStringSubmatch(strings.TrimSpace(line))
	if matches == nil {
		return parsedSandboxDiagnostic{}, false
	}
	lineNumber, err := strconv.Atoi(matches[2])
	if err != nil || lineNumber < 1 {
		return parsedSandboxDiagnostic{}, false
	}
	column := 0
	if matches[3] != "" {
		column, err = strconv.Atoi(matches[3])
		if err != nil || column < 1 {
			return parsedSandboxDiagnostic{}, false
		}
	}
	message := strings.TrimSpace(matches[4])
	if message == "" {
		return parsedSandboxDiagnostic{}, false
	}
	return parsedSandboxDiagnostic{
		Path:    normalizeDiagnosticPath(matches[1]),
		Line:    lineNumber,
		Column:  column,
		Message: message,
	}, true
}

func mapSandboxDiagnosticFile(diagnosticPath string, parsed parsedDiff) (changedFile, bool) {
	diagnosticPath = normalizeDiagnosticPath(diagnosticPath)
	var exact changedFile
	exactMatches := 0
	for _, file := range parsed.Files {
		if file.IsDeleted || strings.TrimSpace(file.NewPath) == "" {
			continue
		}
		if normalizeDiagnosticPath(file.NewPath) == diagnosticPath {
			exact = file
			exactMatches++
		}
	}
	if exactMatches > 0 {
		return exact, exactMatches == 1
	}

	var match changedFile
	matches := 0
	for _, file := range parsed.Files {
		if file.IsDeleted || strings.TrimSpace(file.NewPath) == "" {
			continue
		}
		changedPath := normalizeDiagnosticPath(file.NewPath)
		if !componentSuffixMatch(diagnosticPath, changedPath) {
			continue
		}
		match = file
		matches++
	}
	return match, matches == 1
}

func normalizeDiagnosticPath(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	return value
}

func componentSuffixMatch(left string, right string) bool {
	if left == right {
		return true
	}
	return strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left)
}

func lineInNewHunk(file changedFile, line int) bool {
	for _, hunk := range file.Hunks {
		if hunk.NewCount > 0 && line >= hunk.NewStart && line < hunk.NewStart+hunk.NewCount {
			return true
		}
	}
	return false
}

func diagnosticMetadataForCommand(kind commandKind) (sandboxDiagnosticMetadata, bool) {
	switch kind {
	case commandCheckGoTest:
		return sandboxDiagnosticMetadata{
			RuleID:         ruleSandboxGoTestDiagnostic,
			Severity:       "high",
			Category:       "tests",
			Title:          "Go test reported a changed-file diagnostic",
			Recommendation: "Fix the reported Go test or compile error and rerun go test.",
		}, true
	case commandCheckGoVet:
		return sandboxDiagnosticMetadata{
			RuleID:         ruleSandboxGoVetDiagnostic,
			Severity:       "medium",
			Category:       "correctness",
			Title:          "Go vet reported a changed-file diagnostic",
			Recommendation: "Address the go vet diagnostic and rerun go vet.",
		}, true
	case commandCheckStaticcheck:
		return sandboxDiagnosticMetadata{
			RuleID:         ruleSandboxStaticcheckDiagnostic,
			Severity:       "medium",
			Category:       "quality",
			Title:          "Staticcheck reported a changed-file diagnostic",
			Recommendation: "Address the staticcheck diagnostic and rerun staticcheck.",
		}, true
	default:
		return sandboxDiagnosticMetadata{}, false
	}
}

func sandboxDiagnosticsNeedGenericWarning(
	run sandboxRun,
	diagnostics sandboxDiagnosticResult,
) bool {
	if run.Skipped || run.TimedOut || strings.TrimSpace(run.Error) != "" ||
		len(run.Warnings) > 0 || diagnostics.Overflow {
		return true
	}
	return diagnostics.Parsed == 0 || diagnostics.Mapped != diagnostics.Parsed
}
