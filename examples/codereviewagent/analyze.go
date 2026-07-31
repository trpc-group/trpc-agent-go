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
	"sort"
	"strings"
)

func analyze(lines []changedLine) []finding {
	var findings []finding
	hasProductionGo := false
	hasTestGo := false
	for _, line := range lines {
		lowerFile := strings.ToLower(line.File)
		if strings.HasSuffix(lowerFile, ".go") {
			if strings.HasSuffix(lowerFile, "_test.go") {
				hasTestGo = true
			} else {
				hasProductionGo = true
			}
		}
		content := line.Content
		lower := strings.ToLower(content)
		if _, changed := redact(content); changed {
			findings = append(findings, newFinding(line, "P0", "secret_exposure", 0.99, "SEC001",
				"a credential-like value is present in the diff",
				"remove the credential, rotate it, and load it from an approved secret provider"))
		}
		if strings.Contains(lower, "exec.command") && (strings.Contains(lower, `"sh"`) || strings.Contains(lower, `"bash"`)) {
			findings = append(findings, newFinding(line, "P1", "command_injection", 0.96, "SEC002",
				"shell execution accepts a command string at a high-risk boundary",
				"avoid a shell, pass a fixed executable and validated argument vector"))
		}
		if strings.Contains(lower, "go func(") || strings.Contains(lower, "go func ") {
			findings = append(findings, newFinding(line, "P1", "goroutine_lifecycle", 0.88, "CON001",
				"a goroutine is started without an observable cancellation or join contract",
				"bind the goroutine to context cancellation and wait for shutdown"))
		}
		if strings.Contains(lower, "context.background()") {
			findings = append(findings, newFinding(line, "P1", "context_leak", 0.86, "CON002",
				"request work is detached from the caller context",
				"propagate the caller context instead of creating a background context"))
		}
		if strings.Contains(lower, "sql.open(") {
			findings = append(findings, newFinding(line, "P1", "database_lifecycle", 0.91, "DB001",
				"a database handle is created without visible ownership or cleanup",
				"establish connection ownership, validate with PingContext, and close on shutdown"))
		}
		if strings.Contains(lower, "http.get(") || strings.Contains(lower, "os.open(") {
			findings = append(findings, newFinding(line, "P1", "resource_lifecycle", 0.84, "RES001",
				"a closeable resource is created without visible cleanup",
				"check the error and defer Close immediately after successful acquisition"))
		}
	}
	if hasProductionGo && !hasTestGo {
		line := firstProductionLine(lines)
		findings = append(findings, newFinding(line, "P2", "test_coverage", 0.72, "TST001",
			"production Go code changed without a corresponding test-file change",
			"add focused regression coverage or document why existing tests fully cover the change"))
	}
	return dedupeFindings(findings)
}

func newFinding(line changedLine, severity, category string, confidence float64, ruleID, message, suggestion string) finding {
	status := "finding"
	if confidence < 0.8 {
		status = "needs_human_review"
	}
	return finding{
		File: line.File, StartLine: line.Line, EndLine: line.Line, Severity: severity,
		Category: category, Confidence: confidence, Source: "skill:code-review", RuleID: ruleID,
		Status: status, Message: message, Suggestion: suggestion,
	}
}

func firstProductionLine(lines []changedLine) changedLine {
	for _, line := range lines {
		if strings.HasSuffix(strings.ToLower(line.File), ".go") && !strings.HasSuffix(strings.ToLower(line.File), "_test.go") {
			return line
		}
	}
	return changedLine{}
}

func dedupeFindings(values []finding) []finding {
	byKey := make(map[string]finding, len(values))
	for _, value := range values {
		key := fmt.Sprintf("%s:%d:%s", value.File, value.StartLine, value.RuleID)
		current, ok := byKey[key]
		if !ok || value.Confidence > current.Confidence {
			byKey[key] = value
		}
	}
	result := make([]finding, 0, len(byKey))
	for _, value := range byKey {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].File != result[j].File {
			return result[i].File < result[j].File
		}
		if result[i].StartLine != result[j].StartLine {
			return result[i].StartLine < result[j].StartLine
		}
		return result[i].RuleID < result[j].RuleID
	})
	return result
}
