//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func WriteReports(outputDir string, report ReviewReport) (string, string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", err
	}
	jsonPath := filepath.Join(outputDir, "review_report.json")
	mdPath := filepath.Join(outputDir, "review_report.md")
	diagnosticsPath := filepath.Join(outputDir, "review_diagnostics.json")
	zhPath := filepath.Join(outputDir, "review_report.zh.md")
	data, err := json.MarshalIndent(redactedReport(report), "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(mdPath, []byte(RenderMarkdown(report)), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(diagnosticsPath, []byte(RenderDiagnosticsJSON(report)), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(zhPath, []byte(RenderMarkdownZH(report)), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, mdPath, nil
}

func RenderDiagnosticsJSON(report ReviewReport) string {
	type diagnostics struct {
		TaskID            string            `json:"task_id"`
		Status            ReviewStatus      `json:"status"`
		Input             DiffSummary       `json:"input"`
		Packages          []GoPackageInfo   `json:"packages"`
		SeverityCounts    map[string]int    `json:"severity_counts"`
		ErrorTypeCounts   map[string]int    `json:"error_type_counts"`
		PermissionSummary PermissionSummary `json:"permission_summary"`
		SandboxSummary    []SandboxRun      `json:"sandbox_summary"`
		ArtifactPolicy    ArtifactPolicy    `json:"artifact_policy"`
		Conclusion        string            `json:"conclusion"`
	}
	data, err := json.MarshalIndent(diagnostics{
		TaskID:            report.Task.ID,
		Status:            report.Task.Status,
		Input:             report.Input,
		Packages:          report.Packages,
		SeverityCounts:    report.Metrics.SeverityCounts,
		ErrorTypeCounts:   report.Metrics.ErrorTypeCounts,
		PermissionSummary: report.PermissionSummary,
		SandboxSummary:    report.SandboxRuns,
		ArtifactPolicy:    report.ArtifactPolicy,
		Conclusion:        report.Conclusion,
	}, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return redactSecrets(string(data)) + "\n"
}

func RenderMarkdownZH(report ReviewReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 代码评审报告\n\n")
	fmt.Fprintf(&b, "- 任务: `%s`\n", report.Task.ID)
	fmt.Fprintf(&b, "- 状态: `%s`\n", report.Task.Status)
	fmt.Fprintf(&b, "- 变更文件: `%d`\n", report.Input.FilesChanged)
	fmt.Fprintf(&b, "- Go 文件: `%d`\n", report.Input.GoFiles)
	fmt.Fprintf(&b, "- 高置信问题: `%d`\n", len(report.Findings))
	fmt.Fprintf(&b, "- 人工复核项: `%d`\n\n", len(report.NeedsHumanReview))
	fmt.Fprintf(&b, "## 严重级别统计\n\n")
	for _, sev := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow} {
		fmt.Fprintf(&b, "- %s: %d\n", sev, report.Metrics.SeverityCounts[string(sev)])
	}
	fmt.Fprintf(&b, "\n## 高置信 Findings\n\n")
	if len(report.Findings) == 0 {
		fmt.Fprintf(&b, "未发现高置信问题。\n\n")
	} else {
		for _, f := range report.Findings {
			renderFindingZH(&b, f)
		}
	}
	fmt.Fprintf(&b, "## 人工复核项\n\n")
	if len(report.NeedsHumanReview) == 0 {
		fmt.Fprintf(&b, "无人工复核项。\n\n")
	} else {
		for _, f := range report.NeedsHumanReview {
			renderFindingZH(&b, f)
		}
	}
	fmt.Fprintf(&b, "## 治理和沙箱\n\n")
	fmt.Fprintf(&b, "- Permission allow: `%d`\n", report.PermissionSummary.AllowCount)
	fmt.Fprintf(&b, "- Permission deny: `%d`\n", report.PermissionSummary.DenyCount)
	fmt.Fprintf(&b, "- Permission ask: `%d`\n", report.PermissionSummary.AskCount)
	fmt.Fprintf(&b, "- 工具调用: `%d`\n", report.Metrics.ToolCallCount)
	fmt.Fprintf(&b, "- 沙箱耗时: `%dms`\n\n", report.Metrics.SandboxDurationMS)
	fmt.Fprintf(&b, "## 结论\n\n%s\n", report.Conclusion)
	return redactSecrets(b.String())
}

func renderFindingZH(b *strings.Builder, f Finding) {
	fmt.Fprintf(b, "### %s: %s\n\n", f.Severity, f.Title)
	fmt.Fprintf(b, "- 规则: `%s`\n", f.RuleID)
	fmt.Fprintf(b, "- 分类: `%s`\n", f.Category)
	fmt.Fprintf(b, "- 位置: `%s:%d`\n", f.File, f.Line)
	fmt.Fprintf(b, "- 置信度: `%.2f`\n", f.Confidence)
	fmt.Fprintf(b, "- 证据: `%s`\n", redactSecrets(f.Evidence))
	fmt.Fprintf(b, "- 建议: %s\n\n", redactSecrets(f.Recommendation))
}

func RenderMarkdown(report ReviewReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", report.Task.ID)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Task.Status)
	fmt.Fprintf(&b, "- Files changed: `%d`\n", report.Input.FilesChanged)
	fmt.Fprintf(&b, "- Go files changed: `%d`\n", report.Input.GoFiles)
	fmt.Fprintf(&b, "- Findings: `%d`\n", len(report.Findings))
	fmt.Fprintf(&b, "- Needs human review: `%d`\n\n", len(report.NeedsHumanReview))

	renderPackageSummary(&b, report)
	fmt.Fprintf(&b, "## Severity Summary\n\n")
	for _, sev := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow} {
		fmt.Fprintf(&b, "- %s: %d\n", sev, report.Metrics.SeverityCounts[string(sev)])
	}
	fmt.Fprintf(&b, "\n")
	renderCategorySummary(&b, report)
	fmt.Fprintf(&b, "## Findings\n\n")
	if len(report.Findings) == 0 {
		fmt.Fprintf(&b, "No high-confidence findings.\n\n")
	} else {
		for _, f := range report.Findings {
			renderFinding(&b, f)
		}
	}
	renderFixRecommendations(&b, report)
	fmt.Fprintf(&b, "## Human Review Items\n\n")
	if len(report.NeedsHumanReview) == 0 {
		fmt.Fprintf(&b, "No items require human review.\n\n")
	} else {
		for _, f := range report.NeedsHumanReview {
			renderFinding(&b, f)
		}
	}
	fmt.Fprintf(&b, "## Warnings\n\n")
	if len(report.Warnings) == 0 {
		fmt.Fprintf(&b, "No low-confidence warnings.\n\n")
	} else {
		for _, f := range report.Warnings {
			renderFinding(&b, f)
		}
	}
	fmt.Fprintf(&b, "## Governance\n\n")
	fmt.Fprintf(&b, "- Permission allow decisions: `%d`\n", report.PermissionSummary.AllowCount)
	fmt.Fprintf(&b, "- Permission deny decisions: `%d`\n", report.Metrics.PermissionDenyCount)
	fmt.Fprintf(&b, "- Permission ask decisions: `%d`\n", report.Metrics.PermissionAskCount)
	fmt.Fprintf(&b, "- Permission needs human review decisions: `%d`\n", report.PermissionSummary.NeedsHumanReviewCount)
	fmt.Fprintf(&b, "- Artifact policy: retained `%d`, rejected `%d`, max `%d` files, max `%d` bytes per file\n",
		report.ArtifactPolicy.RetainedCount,
		report.ArtifactPolicy.RejectedCount,
		report.ArtifactPolicy.MaxArtifacts,
		report.ArtifactPolicy.MaxBytesPerFile,
	)
	for _, d := range report.Permissions {
		fmt.Fprintf(&b, "- `%s`: action=`%s`, disposition=`%s`",
			d.Command, d.Action, firstNonEmpty(d.Disposition, permissionDisposition(d.Action)))
		if d.Reason != "" {
			fmt.Fprintf(&b, " - %s", d.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "\n## Sandbox\n\n")
	if len(report.SandboxRuns) == 0 {
		fmt.Fprintf(&b, "No sandbox commands were executed.\n\n")
	} else {
		for _, run := range report.SandboxRuns {
			fmt.Fprintf(&b, "- `%s %s`: %s, exit=%d, timeout=%t, duration=%dms\n",
				run.Command, strings.Join(run.Args, " "), run.Status,
				run.ExitCode, run.TimedOut, run.DurationMS)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Metrics\n\n")
	fmt.Fprintf(&b, "- Total duration: `%dms`\n", report.Metrics.TotalDurationMS)
	fmt.Fprintf(&b, "- Sandbox duration: `%dms`\n", report.Metrics.SandboxDurationMS)
	fmt.Fprintf(&b, "- Tool calls: `%d`\n", report.Metrics.ToolCallCount)
	fmt.Fprintf(&b, "- Error types: `%v`\n\n", report.Metrics.ErrorTypeCounts)
	fmt.Fprintf(&b, "## Conclusion\n\n%s\n", report.Conclusion)
	return redactSecrets(b.String())
}

func renderPackageSummary(b *strings.Builder, report ReviewReport) {
	fmt.Fprintf(b, "## Package Summary\n\n")
	if len(report.Packages) == 0 {
		fmt.Fprintf(b, "No Go package information recorded.\n\n")
		return
	}
	fmt.Fprintf(b, "| Package path | Package name | Files |\n")
	fmt.Fprintf(b, "| --- | --- | ---: |\n")
	for _, pkg := range report.Packages {
		name := pkg.PackageName
		if name == "" {
			name = "unknown"
		}
		fmt.Fprintf(b, "| `%s` | `%s` | %d |\n", pkg.PackagePath, name, len(pkg.Files))
	}
	fmt.Fprintf(b, "\n")
}

func renderCategorySummary(b *strings.Builder, report ReviewReport) {
	counts := map[string]int{}
	all := append([]Finding{}, report.Findings...)
	all = append(all, report.Warnings...)
	all = append(all, report.NeedsHumanReview...)
	for _, f := range all {
		if f.Category != "" {
			counts[f.Category]++
		}
	}
	fmt.Fprintf(b, "## Category Summary\n\n")
	if len(counts) == 0 {
		fmt.Fprintf(b, "No finding categories recorded.\n\n")
		return
	}
	fmt.Fprintf(b, "| Category | Count |\n")
	fmt.Fprintf(b, "| --- | ---: |\n")
	for _, category := range sortedCategoryKeys(counts) {
		fmt.Fprintf(b, "| %s | %d |\n", category, counts[category])
	}
	fmt.Fprintf(b, "\n")
}

func renderFixRecommendations(b *strings.Builder, report ReviewReport) {
	fmt.Fprintf(b, "## Fix Recommendations\n\n")
	all := append([]Finding{}, report.Findings...)
	all = append(all, report.NeedsHumanReview...)
	seen := map[string]bool{}
	for _, f := range all {
		if f.Recommendation == "" {
			continue
		}
		key := f.RuleID + ":" + f.Recommendation
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(b, "- `%s`: %s\n", f.RuleID, redactSecrets(f.Recommendation))
	}
	if len(seen) == 0 {
		fmt.Fprintf(b, "No executable recommendations recorded.\n")
	}
	fmt.Fprintf(b, "\n")
}

func sortedCategoryKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderFinding(b *strings.Builder, f Finding) {
	fmt.Fprintf(b, "### %s: %s\n\n", f.Severity, f.Title)
	fmt.Fprintf(b, "- Rule: `%s`\n", f.RuleID)
	fmt.Fprintf(b, "- Category: `%s`\n", f.Category)
	fmt.Fprintf(b, "- Location: `%s:%d`\n", f.File, f.Line)
	fmt.Fprintf(b, "- Confidence: `%.2f`\n", f.Confidence)
	fmt.Fprintf(b, "- Evidence: `%s`\n", redactSecrets(f.Evidence))
	fmt.Fprintf(b, "- Recommendation: %s\n\n", redactSecrets(f.Recommendation))
}

func redactedReport(in ReviewReport) ReviewReport {
	out := in
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	if out.Warnings == nil {
		out.Warnings = []Finding{}
	}
	if out.NeedsHumanReview == nil {
		out.NeedsHumanReview = []Finding{}
	}
	if out.SandboxRuns == nil {
		out.SandboxRuns = []SandboxRun{}
	}
	if out.Permissions == nil {
		out.Permissions = []PermissionDecisionRecord{}
	}
	if out.Artifacts == nil {
		out.Artifacts = []ArtifactRecord{}
	}
	if out.Packages == nil {
		out.Packages = []GoPackageInfo{}
	}
	if out.Metrics.SeverityCounts == nil {
		out.Metrics.SeverityCounts = map[string]int{}
	}
	if out.Metrics.ErrorTypeCounts == nil {
		out.Metrics.ErrorTypeCounts = map[string]int{}
	}
	redactFindings(out.Findings)
	redactFindings(out.Warnings)
	redactFindings(out.NeedsHumanReview)
	for i := range out.SandboxRuns {
		out.SandboxRuns[i].Stdout = redactSecrets(out.SandboxRuns[i].Stdout)
		out.SandboxRuns[i].Stderr = redactSecrets(out.SandboxRuns[i].Stderr)
	}
	return out
}

func redactFindings(findings []Finding) {
	for i := range findings {
		findings[i].Evidence = redactSecrets(findings[i].Evidence)
		findings[i].Recommendation = redactSecrets(findings[i].Recommendation)
	}
}
