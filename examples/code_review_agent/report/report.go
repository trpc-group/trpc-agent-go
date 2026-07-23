//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report renders review reports to JSON and Markdown.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// WriteJSON writes review_report.json and returns the redacted JSON text.
func WriteJSON(outDir string, rep *review.Report) (string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", err
	}
	path := filepath.Join(outDir, "review_report.json")
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", "", err
	}
	text := safety.Redact(string(raw))
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return path, text, nil
}

// WriteMarkdown writes review_report.md and returns the redacted markdown.
func WriteMarkdown(outDir string, rep *review.Report) (string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", err
	}
	path := filepath.Join(outDir, "review_report.md")
	md := safety.Redact(renderMarkdown(rep))
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return "", "", err
	}
	return path, md, nil
}

func renderMarkdown(rep *review.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- **Task ID**: `%s`\n", rep.TaskID)
	fmt.Fprintf(&b, "- **Status**: %s\n", rep.Status)
	fmt.Fprintf(&b, "- **Mode**: %s\n", rep.Mode)
	fmt.Fprintf(&b, "- **Executor**: %s\n", rep.Executor)
	fmt.Fprintf(&b, "- **Generated**: %s\n", rep.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- **Input**: %s (%s)\n\n", rep.Input.Summary, rep.Input.Kind)
	fmt.Fprintf(&b, "## Conclusion\n\n%s\n\n", rep.Conclusion)

	fmt.Fprintf(&b, "## Severity Summary\n\n")
	if len(rep.Metrics.SeverityDist) == 0 {
		fmt.Fprintf(&b, "_No high-confidence findings._\n\n")
	} else {
		for sev, n := range rep.Metrics.SeverityDist {
			fmt.Fprintf(&b, "- %s: %d\n", sev, n)
		}
		fmt.Fprintf(&b, "\n")
	}

	writeFindings(&b, "Findings", rep.Findings)
	writeFindings(&b, "Warnings / Needs Human Review", rep.Warnings)

	fmt.Fprintf(&b, "## Governance\n\n")
	if len(rep.Governance.PermissionDecisions) == 0 {
		fmt.Fprintf(&b, "_No permission intercepts._\n\n")
	} else {
		for _, d := range rep.Governance.PermissionDecisions {
			fmt.Fprintf(&b, "- `%s` → **%s**: %s\n", d.Command, d.Action, d.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	if rep.Governance.ExecutorFallback != "" {
		fmt.Fprintf(&b, "- Executor fallback: `%s`\n", rep.Governance.ExecutorFallback)
	}
	if rep.Governance.AgentAssistNote != "" {
		fmt.Fprintf(&b, "- Agent assist: %s\n", rep.Governance.AgentAssistNote)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Sandbox Runs\n\n")
	if len(rep.SandboxRuns) == 0 {
		fmt.Fprintf(&b, "_No sandbox runs._\n\n")
	} else {
		for _, r := range rep.SandboxRuns {
			fmt.Fprintf(&b, "- `%s` status=%s exit=%d duration=%dms truncated=%v",
				r.Command, r.Status, r.ExitCode, r.DurationMS, r.Truncated)
			if r.Error != "" {
				fmt.Fprintf(&b, " error=%s", r.Error)
			}
			fmt.Fprintf(&b, "\n")
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Metrics\n\n")
	fmt.Fprintf(&b, "- total_ms: %d\n", rep.Metrics.TotalDurationMS)
	fmt.Fprintf(&b, "- sandbox_ms: %d\n", rep.Metrics.SandboxDurationMS)
	fmt.Fprintf(&b, "- tool_calls: %d\n", rep.Metrics.ToolCallCount)
	fmt.Fprintf(&b, "- permission_denies: %d\n", rep.Metrics.PermissionDenyCount)
	fmt.Fprintf(&b, "- permission_asks: %d\n", rep.Metrics.PermissionAskCount)
	fmt.Fprintf(&b, "- findings: %d\n", rep.Metrics.FindingCount)
	fmt.Fprintf(&b, "- warnings: %d\n\n", rep.Metrics.WarningCount)

	fmt.Fprintf(&b, "## Artifacts\n\n")
	for _, a := range rep.Artifacts {
		fmt.Fprintf(&b, "- %s → `%s`\n", a.Name, a.PathOrRef)
	}
	fmt.Fprintf(&b, "\n")
	return b.String()
}

func writeFindings(b *strings.Builder, title string, list []review.Finding) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if len(list) == 0 {
		fmt.Fprintf(b, "_None._\n\n")
		return
	}
	for i, f := range list {
		fmt.Fprintf(b, "### %d. [%s] %s\n\n", i+1, strings.ToUpper(f.Severity), f.Title)
		fmt.Fprintf(b, "- rule: `%s` (%s)\n", f.RuleID, f.Category)
		fmt.Fprintf(b, "- location: `%s:%d`\n", f.File, f.Line)
		fmt.Fprintf(b, "- confidence: %.2f / source: %s\n", f.Confidence, f.Source)
		fmt.Fprintf(b, "- evidence: `%s`\n", f.Evidence)
		fmt.Fprintf(b, "- recommendation: %s\n\n", f.Recommendation)
	}
}
