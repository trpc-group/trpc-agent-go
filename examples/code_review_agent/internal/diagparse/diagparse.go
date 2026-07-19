//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package diagparse converts sandbox tool output (go vet, staticcheck)
// into structured rules.Finding values so that sandbox diagnostics flow
// into the review report alongside rule and LLM findings.
//
// Borrowed from competitor PR #2243, which surfaces go vet / staticcheck
// output as first-class findings instead of opaque stderr blobs.
//
// Supported output formats:
//
//   - go vet:     "# <package>\n<file>:<line>:<col>: <message>"
//   - staticcheck: "<file>:<line>:<col>: <category> <message> (SAxxxx)"
//
// Both tools write diagnostics to stderr. The parser scans stdout and
// stderr line by line; lines that do not match the expected pattern
// (e.g. "# package" headers, blank lines, build output) are skipped.
package diagparse

import (
	"bytes"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// Rule IDs emitted by this package.
const (
	// RuleGoVet is the rule ID for findings parsed from `go vet` output.
	RuleGoVet = "DIAG-001"
	// RuleStaticcheck is the rule ID for findings parsed from `staticcheck` output.
	RuleStaticcheck = "DIAG-002"
)

// FromRun converts a single sandbox run's stdout/stderr into findings.
// The command is used to select the parser: "go vet" output is parsed
// by parseGoVet, "staticcheck" output by parseStaticcheck. Unknown
// commands yield no findings. Both stdout and stderr are scanned
// because some tool invocations write diagnostics to stdout.
func FromRun(command string, stdout, stderr []byte) []rules.Finding {
	cmd := normaliseCommand(command)
	switch {
	case strings.HasPrefix(cmd, "go vet") || strings.HasPrefix(cmd, "go-vet"):
		return append(parseGoVet(stderr), parseGoVet(stdout)...)
	case strings.HasPrefix(cmd, "staticcheck"):
		return append(parseStaticcheck(stderr), parseStaticcheck(stdout)...)
	case strings.Contains(cmd, "run_go_vet"):
		return append(parseGoVet(stderr), parseGoVet(stdout)...)
	case strings.Contains(cmd, "run_staticcheck"):
		return append(parseStaticcheck(stderr), parseStaticcheck(stdout)...)
	default:
		return nil
	}
}

// FromRuns applies FromRun to a slice of run records. A run record is
// represented as (command, stdout, stderr). This signature keeps the
// package decoupled from the pipeline's sandboxRunRecord type.
func FromRuns(runs []RunInput) []rules.Finding {
	var out []rules.Finding
	for _, r := range runs {
		out = append(out, FromRun(r.Command, r.Stdout, r.Stderr)...)
	}
	return out
}

// RunInput is the minimal input shape FromRuns needs. The pipeline
// adapts its sandboxRunRecord to this type before calling FromRuns.
type RunInput struct {
	Command string
	Stdout  []byte
	Stderr  []byte
}

// normaliseCommand strips leading whitespace and collapses internal
// runs of whitespace so prefix checks are robust to argument spacing.
func normaliseCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	// Collapse internal whitespace runs so "go  vet ./..." matches
	// the "go vet" prefix check.
	fields := strings.Fields(cmd)
	return strings.Join(fields, " ")
}

// parseGoVet scans go vet output. Each diagnostic line has the form:
//
//	file.go:line:col: message
//
// or (older Go versions without column):
//
//	file.go:line: message
//
// Lines starting with '#' are package headers and are skipped.
func parseGoVet(out []byte) []rules.Finding {
	if len(out) == 0 {
		return nil
	}
	var findings []rules.Finding
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimRight(string(line), "\r")
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		f, ok := parseDiagnosticLine(s, RuleGoVet, "go vet", "correctness")
		if !ok {
			continue
		}
		findings = append(findings, f)
	}
	return findings
}

// parseStaticcheck scans staticcheck output. Each diagnostic line has
// the form:
//
//	file.go:line:col: <category> <message> (SAxxxx)
//
// The category/check code (SAxxxx) is extracted into the Evidence field
// so reviewers can see which staticcheck check fired.
func parseStaticcheck(out []byte) []rules.Finding {
	if len(out) == 0 {
		return nil
	}
	var findings []rules.Finding
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimRight(string(line), "\r")
		if s == "" {
			continue
		}
		f, ok := parseDiagnosticLine(s, RuleStaticcheck, "staticcheck", "correctness")
		if !ok {
			continue
		}
		findings = append(findings, f)
	}
	return findings
}

// parseDiagnosticLine parses a single "file:line:col: message" line
// into a rules.Finding. The column component is optional. ruleID,
// toolName and category are set by the caller. Confidence is fixed at
// 0.90 because tool diagnostics are deterministic.
func parseDiagnosticLine(s, ruleID, toolName, category string) (rules.Finding, bool) {
	// Split into "location" and "message" on the first ": " after the
	// file path. We can't just split on ":" because Windows paths use
	// "C:\..." and the message itself may contain colons.
	colonSpace := strings.Index(s, ": ")
	if colonSpace < 0 {
		return rules.Finding{}, false
	}
	location := s[:colonSpace]
	message := strings.TrimSpace(s[colonSpace+2:])
	if message == "" {
		return rules.Finding{}, false
	}

	file, line := splitLocation(location)
	if file == "" || line <= 0 {
		return rules.Finding{}, false
	}

	return rules.Finding{
		RuleID:         ruleID,
		Severity:       "medium",
		Category:       category,
		File:           file,
		Line:           line,
		Title:          truncate(toolName+": "+message, 160),
		Evidence:       truncate(s, 200),
		Recommendation: "Address the " + toolName + " diagnostic or suppress it with a directive if it is a false positive",
		Confidence:     0.90,
		Source:         "diag:" + ruleID,
	}, true
}

// splitLocation splits a "file:line[:col]" location string into the
// file path and line number. The column is ignored — the finding is
// anchored to the line, which is enough for the review report. Windows
// drive letters (e.g. "C:\path") are handled by treating the first
// colon followed by a digit as the line separator (the drive-letter
// colon is followed by a backslash, so it is skipped).
func splitLocation(loc string) (string, int) {
	// Find the first ":" followed by a digit. That is the line
	// separator; everything before it is the file path, and the
	// digits after it (up to the next ":" or end) are the line number.
	for i := 0; i < len(loc); i++ {
		if loc[i] != ':' {
			continue
		}
		if i+1 >= len(loc) || loc[i+1] < '0' || loc[i+1] > '9' {
			continue
		}
		// Parse leading digits after the colon as the line number.
		lineStr := loc[i+1:]
		digitsEnd := len(lineStr)
		for j, c := range lineStr {
			if c < '0' || c > '9' {
				digitsEnd = j
				break
			}
		}
		n, err := strconv.Atoi(lineStr[:digitsEnd])
		if err != nil || n <= 0 {
			continue
		}
		return loc[:i], n
	}
	return "", 0
}

// truncate caps s at n characters, appending "..." if truncation
// occurred. Keeps diagnostic titles short enough for a report cell.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
