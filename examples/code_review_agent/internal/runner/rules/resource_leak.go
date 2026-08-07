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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
)

// ResourceLeakRule detects resources that are opened without being closed (defer Close).
type ResourceLeakRule struct {
	runner.RuleBase
}

// openResource tracks a file/body/rows resource that was opened and may need closing.
type openResource struct {
	lineNum int
	varName string
	resType string // "file", "body", "rows", "conn"
}

// NewResourceLeakRule creates a new resource leak rule.
func NewResourceLeakRule() *ResourceLeakRule {
	return &ResourceLeakRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_RESOURCE_NO_CLOSE",
			CategoryValue: finding.CategoryResourceLeak,
			DefaultSev:    finding.SeverityHigh,
		},
	}
}

// Check examines file content for resource leak patterns.
func (r *ResourceLeakRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	var opened []openResource

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Detect os.Open / os.Create without defer Close.
		if m := matchResourceOpen(trimmed, lineNum); m != nil {
			opened = append(opened, *m)
		}

		// Detect http.Get / http.Post without body Close.
		if matchHTTPBody(trimmed) {
			opened = append(opened, openResource{lineNum: lineNum, varName: inferVarName(trimmed), resType: "body"})
		}

		// Detect db.Query / db.QueryRow without rows.Close.
		if matchRowsQuery(trimmed) {
			opened = append(opened, openResource{lineNum: lineNum, varName: inferRowsVar(trimmed), resType: "rows"})
		}
	}

	// Check if each opened resource has a corresponding defer Close.
	for _, res := range opened {
		if !hasDeferClose(lines, res.lineNum, res.varName, res.resType) {
			evidence := lines[res.lineNum-1]
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, res.lineNum,
				"Resource opened without guaranteed close",
				evidence,
				"Add 'defer "+res.varName+".Close()' immediately after opening the resource",
				finding.ConfidenceHigh,
			))
		}
	}

	return findings, nil
}

func matchResourceOpen(line string, lineNum int) *openResource {
	var name string
	switch {
	case strings.Contains(line, "os.Open("):
		name, _ = extractAssignment(line, "os.Open(")
	case strings.Contains(line, "os.Create("):
		name, _ = extractAssignment(line, "os.Create(")
	case strings.Contains(line, "os.OpenFile("):
		name, _ = extractAssignment(line, "os.OpenFile(")
	default:
		return nil
	}
	if name == "" {
		return nil
	}
	return &openResource{lineNum: lineNum, varName: name, resType: "file"}
}

func matchHTTPBody(line string) bool {
	return (strings.Contains(line, "http.Get(") || strings.Contains(line, "http.Post(") ||
		strings.Contains(line, "http.PostForm(") || strings.Contains(line, "http.DefaultClient.Do(") ||
		strings.Contains(line, "client.Do(") || strings.Contains(line, `resp, err := http.`)) &&
		strings.Contains(line, ":=")
}

func matchRowsQuery(line string) bool {
	return (strings.Contains(line, ".Query(") || strings.Contains(line, ".QueryRow(") ||
		strings.Contains(line, ".QueryContext(")) &&
		strings.Contains(line, ":=")
}

func inferVarName(line string) string {
	if idx := strings.Index(line, ", err :="); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	return "resp"
}

func inferRowsVar(line string) string {
	if idx := strings.Index(line, ", err :="); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, " := "); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	return "rows"
}

func extractAssignment(line, fn string) (string, bool) {
	if !strings.Contains(line, fn) {
		return "", false
	}
	if idx := strings.Index(line, " := "); idx > 0 {
		varName := strings.TrimSpace(line[:idx])
		if parts := strings.SplitN(varName, ",", 2); len(parts) >= 1 {
			return strings.TrimSpace(parts[0]), true
		}
		return varName, true
	}
	return "", false
}

func hasDeferClose(lines []string, startLine int, varName, resType string) bool {
	end := startLine + 5
	if end > len(lines) {
		end = len(lines)
	}
	for i := startLine; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "defer ") && strings.Contains(trimmed, ".Close(") {
			// Match varName.Close(), varName.Body.Close(), etc.
			if strings.Contains(trimmed, varName+".") || strings.Contains(trimmed, resType+".") {
				return true
			}
		}
	}
	// Also check the entire function body for a deferred close (broader scan).
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "defer ") && strings.Contains(trimmed, ".Close(") {
			if strings.Contains(trimmed, varName+".") || strings.Contains(trimmed, resType+".") {
				return true
			}
		}
	}
	return false
}
