//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"regexp"
	"strings"
)

// IgnoredErrorRule detects ignored error return values using the
// blank identifier `_ =`.
type IgnoredErrorRule struct{}

func (r *IgnoredErrorRule) ID() string       { return "IGNORED_ERROR" }
func (r *IgnoredErrorRule) Category() string { return "error_handling" }
func (r *IgnoredErrorRule) Description() string {
	return "Detects error return values assigned to _ (ignored)"
}

var (
	// _ = someFunc() — likely ignoring an error.
	reIgnoredErr = regexp.MustCompile(`_\s*=\s*\w+\.\w+\(`)
	// _ = json.Marshal or similar that returns error.
	reIgnoredErrBuiltin = regexp.MustCompile(`_\s*=\s*(json\.Marshal|json\.Unmarshal|ioutil\.WriteFile|os\.WriteFile)\(`)
	// err.Check() or err == nil pattern missing.
)

func (r *IgnoredErrorRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reIgnoredErrBuiltin.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityHigh,
			Title:    "Error return value ignored",
			Evidence: content,
			Recommendation: "Do not ignore error return values. Handle the " +
				"error or wrap it with fmt.Errorf and return.",
			Confidence: 0.85,
		})
	} else if reIgnoredErr.MatchString(content) {
		// Lower confidence for general _ = pattern.
		findings = append(findings, Finding{
			Severity: SeverityMedium,
			Title:    "Return value assigned to _ (possibly ignored error)",
			Evidence: content,
			Recommendation: "If this is an error return value, handle it " +
				"instead of assigning to _.",
			Confidence: 0.55,
		})
	}
	return findings
}

// PanicInGoroutineRule detects panic() calls inside goroutines, which
// can crash the entire program.
type PanicInGoroutineRule struct{}

func (r *PanicInGoroutineRule) ID() string       { return "PANIC_IN_GOROUTINE" }
func (r *PanicInGoroutineRule) Category() string { return "error_handling" }
func (r *PanicInGoroutineRule) Description() string {
	return "Detects panic() calls that may crash the program, especially " +
		"in goroutines"
}

var rePanic = regexp.MustCompile(`panic\s*\(`)

func (r *PanicInGoroutineRule) Check(file DiffFile, hunk DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if !rePanic.MatchString(content) {
		return nil
	}

	// Check if this line is inside a goroutine (look back for go func).
	inGoroutine := false
	idx := -1
	for i, l := range hunk.Lines {
		if l.Number == line.Number && l.Type == line.Type {
			idx = i
			break
		}
	}
	if idx >= 0 {
		for j := idx - 1; j >= 0 && j >= idx-20; j-- {
			c := hunk.Lines[j].Content
			if strings.Contains(c, "go func") || strings.Contains(c, "go ") {
				inGoroutine = true
				break
			}
		}
	}

	if inGoroutine {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Title:    "panic() inside a goroutine can crash the program",
			Evidence: content,
			Recommendation: "Do not use panic() in goroutines. Return an error " +
				"and handle it in the caller. If recovery is needed, use " +
				"defer recover().",
			Confidence: 0.9,
		})
	} else {
		findings = append(findings, Finding{
			Severity: SeverityMedium,
			Title:    "panic() call detected",
			Evidence: content,
			Recommendation: "Avoid panic() in production code. Return an error " +
				"and handle it in the caller.",
			Confidence: 0.65,
		})
	}
	return findings
}
